package unifiedservice

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/middleware"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	service "github.com/RhinoLink-Tech/data-fabric/internal/service"
)

type UnifiedQueryService interface {
	ValidateQuery(ctx context.Context, caller *security.Caller, req *model.QueryRequest) error
	GetQueryStats(ctx context.Context) (*model.QueryStats, error)
	FetchQuery(ctx context.Context, caller *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error)
	FetchNextBatch(ctx context.Context, caller *security.Caller, queryID, slug, token string, batchSize int) (*model.FetchQueryResponse, error)
	GetQueryHistory(ctx context.Context, filter service.QueryHistoryFilter) (*service.QueryHistoryPage, error)
}

type unifiedQueryService struct {
	paginationService service.QueryService // The original QueryService implementation
	streamingService  service.StreamingService
	config            *config.Config
	history           *service.QueryHistory
}

// NewUnifiedQueryService creates a new unified query service that chooses between
// pagination and streaming based on configuration
func NewUnifiedQueryService(
	paginationService service.QueryService,
	streamingService service.StreamingService,
	config *config.Config,
) UnifiedQueryService {
	return &unifiedQueryService{
		paginationService: paginationService,
		streamingService:  streamingService,
		config:            config,
		history:           service.NewQueryHistory(1000),
	}
}

// ValidateQuery validates the query using the pagination service's validator
func (uqs *unifiedQueryService) ValidateQuery(ctx context.Context, caller *security.Caller, req *model.QueryRequest) error {
	return uqs.paginationService.ValidateQuery(ctx, caller, req)
}

// GetQueryStats returns query statistics
func (uqs *unifiedQueryService) GetQueryStats(ctx context.Context) (*model.QueryStats, error) {
	return uqs.paginationService.GetQueryStats(ctx)
}

// FetchQuery executes a fetch query and records it in the query history
// (#103). Recording happens for both engines at this chokepoint, so HTTP and
// MCP traffic are covered alike; continuation fetches extend an already
// recorded query and are not separate records.
func (uqs *unifiedQueryService) FetchQuery(ctx context.Context, caller *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	started := time.Now()
	result, executionMode, err := uqs.executeFetchQuery(ctx, caller, req)

	status := "success"
	var rowsRead int64
	if err != nil {
		status = "error"
		middleware.RecordQueryError(executionMode, "execution_error")
	} else if result != nil {
		rowsRead = int64(len(result.Entries))
	}
	middleware.RecordQueryMetrics(executionMode, status, time.Since(started), rowsRead)

	record := service.QueryRecord{
		DataSourceID: req.DataSourceID,
		SQL:          req.SQL,
		Status:       "success",
		DurationMs:   time.Since(started).Milliseconds(),
		User:         service.QueryUserFromContext(ctx),
	}
	if err != nil {
		record.Status = "error"
	} else if result != nil {
		record.QueryID = result.QueryID
	}
	uqs.history.Add(record)

	return result, err
}

const (
	executionModeAuto       = "auto"
	executionModeStreaming  = "streaming"
	executionModePagination = "pagination"

	// Large first batches are more likely to benefit from keeping a server-side
	// cursor open than from repeating the query with LIMIT/OFFSET.
	autoStreamingBatchThreshold = 1000
)

var smallResultLimitPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)\bLIMIT\s+(?:\d+\s*,\s*)?(\d+)\b`),
	regexp.MustCompile(`(?is)\bFETCH\s+(?:FIRST|NEXT)\s+(\d+)\s+ROWS?\s+ONLY\b`),
	regexp.MustCompile(`(?is)\bTOP\s*\(?\s*(\d+)\s*\)?`),
}

type streamingCapabilityResolver interface {
	SupportsStreaming(ctx context.Context, caller *security.Caller, dataSourceID string) (bool, error)
}

// executeFetchQuery selects an engine and reports the engine that actually
// handled the request. Auto mode only falls back before query execution starts.
func (uqs *unifiedQueryService) executeFetchQuery(ctx context.Context, caller *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, string, error) {
	mode, err := uqs.selectExecutionMode(ctx, caller, req)
	if err != nil {
		return nil, mode, err
	}

	var result *model.FetchQueryResponse
	switch mode {
	case executionModeStreaming:
		if uqs.streamingService == nil {
			return nil, mode, fmt.Errorf("streaming service is not configured")
		}
		streamReq := req
		if req != nil && req.Type != 2 {
			copy := *req
			copy.Type = 2
			streamReq = &copy
		}
		result, err = uqs.streamingService.FetchWithStreaming(ctx, caller, streamReq)
		if err != nil && strings.EqualFold(strings.TrimSpace(uqs.getExecutionMode()), executionModeAuto) &&
			errors.Is(err, service.ErrMaxConcurrentStreams) {
			// The streaming service returns this sentinel before opening a
			// query, so retrying via pagination cannot duplicate execution.
			mode = executionModePagination
			result, err = uqs.paginationService.FetchQuery(ctx, caller, req)
		}
	case executionModePagination:
		result, err = uqs.paginationService.FetchQuery(ctx, caller, req)
	default:
		mode = executionModePagination
		result, err = uqs.paginationService.FetchQuery(ctx, caller, req)
	}

	markExecutionMode(result, mode)
	return result, mode, err
}

// selectExecutionMode applies the documented auto policy. Explicit config modes
// override request hints; auto mode keeps small or explicitly limited results
// on pagination and uses streaming for requested or large result batches only
// when the source driver's capability advertises support.
func (uqs *unifiedQueryService) selectExecutionMode(ctx context.Context, caller *security.Caller, req *model.FetchQueryRequest) (string, error) {
	configured := strings.ToLower(strings.TrimSpace(uqs.getExecutionMode()))
	switch configured {
	case executionModeStreaming, executionModePagination:
		return configured, nil
	case executionModeAuto:
		if req == nil || req.Type == 1 {
			return executionModePagination, nil
		}
		streamRequested := req.Type == 2
		if !streamRequested {
			if !uqs.preferStreaming() || req.BatchSize < autoStreamingBatchThreshold || hasSmallResultLimit(req.SQL) {
				return executionModePagination, nil
			}
			streamRequested = true
		}
		if !streamRequested || uqs.streamingService == nil {
			return executionModePagination, nil
		}
		resolver, ok := uqs.streamingService.(streamingCapabilityResolver)
		if !ok {
			return executionModePagination, nil
		}
		supported, err := resolver.SupportsStreaming(ctx, caller, req.DataSourceID)
		if err != nil {
			return executionModePagination, err
		}
		if supported {
			return executionModeStreaming, nil
		}
		return executionModePagination, nil
	default:
		return executionModePagination, nil
	}
}

func (uqs *unifiedQueryService) preferStreaming() bool {
	return uqs.config == nil || uqs.config.Query.PreferStreaming
}

func hasSmallResultLimit(sql string) bool {
	for _, pattern := range smallResultLimitPatterns {
		match := pattern.FindStringSubmatch(sql)
		if len(match) < 2 {
			continue
		}
		limit, err := strconv.Atoi(match[1])
		if err == nil && limit <= autoStreamingBatchThreshold {
			return true
		}
	}
	return false
}

func markExecutionMode(response *model.FetchQueryResponse, mode string) {
	if response != nil {
		response.ExecutionMode = mode
	}
}

// FetchNextBatch routes continuations using the forced configured mode. In auto
// mode it probes the streaming session first, then falls back to pagination
// only when no streaming session exists.
func (uqs *unifiedQueryService) FetchNextBatch(ctx context.Context, caller *security.Caller, queryID, slug, token string, batchSize int) (*model.FetchQueryResponse, error) {
	switch strings.ToLower(strings.TrimSpace(uqs.getExecutionMode())) {
	case executionModeStreaming:
		if uqs.streamingService == nil {
			return nil, fmt.Errorf("streaming service is not configured")
		}
		result, err := uqs.streamingService.FetchNextBatchWithStreaming(ctx, caller, queryID, slug, token, batchSize)
		markExecutionMode(result, executionModeStreaming)
		return result, err
	case executionModeAuto:
		if uqs.streamingService != nil {
			result, err := uqs.streamingService.FetchNextBatchWithStreaming(ctx, caller, queryID, slug, token, batchSize)
			if err == nil {
				markExecutionMode(result, executionModeStreaming)
				return result, nil
			}
			if !errors.Is(err, service.ErrQuerySessionUnavailable) {
				return nil, err
			}
		}
		result, err := uqs.paginationService.FetchNextBatch(ctx, caller, queryID, slug, token, batchSize)
		markExecutionMode(result, executionModePagination)
		return result, err
	default:
		result, err := uqs.paginationService.FetchNextBatch(ctx, caller, queryID, slug, token, batchSize)
		markExecutionMode(result, executionModePagination)
		return result, err
	}
}

// GetQueryHistory returns the caller's slice of the recorded query history
// (#103). The caller identity comes from the filter the controller fills in.
func (uqs *unifiedQueryService) GetQueryHistory(ctx context.Context, filter service.QueryHistoryFilter) (*service.QueryHistoryPage, error) {
	return uqs.history.List(filter), nil
}

// getExecutionMode determines the execution mode based on configuration
func (uqs *unifiedQueryService) getExecutionMode() string {
	if uqs.config != nil && uqs.config.Query.ExecutionMode != "" {
		return uqs.config.Query.ExecutionMode
	}
	// Default to auto mode
	return "auto"
}
