package unifiedservice

import (
	"context"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
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
	result, err := uqs.executeFetchQuery(ctx, caller, req)

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

// executeFetchQuery dispatches to the configured engine.
func (uqs *unifiedQueryService) executeFetchQuery(ctx context.Context, caller *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	// Determine execution mode based on configuration
	executionMode := uqs.getExecutionMode()

	switch executionMode {
	case "streaming":
		return uqs.streamingService.FetchWithStreaming(ctx, caller, req)
	case "pagination":
		return uqs.paginationService.FetchQuery(ctx, caller, req)
	case "auto":
		// Default to pagination for compatibility
		return uqs.paginationService.FetchQuery(ctx, caller, req)
	default:
		// Default to pagination
		return uqs.paginationService.FetchQuery(ctx, caller, req)
	}
}

// FetchNextBatch fetches the next batch using the pagination service
func (uqs *unifiedQueryService) FetchNextBatch(ctx context.Context, caller *security.Caller, queryID, slug, token string, batchSize int) (*model.FetchQueryResponse, error) {
	// Determine execution mode based on configuration
	executionMode := uqs.getExecutionMode()

	switch executionMode {
	case "streaming":
		return uqs.streamingService.FetchNextBatchWithStreaming(ctx, caller, queryID, slug, token, batchSize)
	case "pagination":
		return uqs.paginationService.FetchNextBatch(ctx, caller, queryID, slug, token, batchSize)
	case "auto":
		// Default to pagination for compatibility
		return uqs.paginationService.FetchNextBatch(ctx, caller, queryID, slug, token, batchSize)
	default:
		// Default to pagination
		return uqs.paginationService.FetchNextBatch(ctx, caller, queryID, slug, token, batchSize)
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
