package unifiedservice

import (
	"context"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/middleware"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
)

type stubPaginationService struct {
	service.QueryService
	resp *model.FetchQueryResponse
	err  error
}

func (s stubPaginationService) FetchQuery(_ context.Context, _ *security.Caller, _ *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	return s.resp, s.err
}

func (s stubPaginationService) FetchNextBatch(_ context.Context, _ *security.Caller, _, _, _ string, _ int) (*model.FetchQueryResponse, error) {
	return s.resp, s.err
}

type stubStreamingService struct {
	service.StreamingService
}

func newTestUnifiedService(resp *model.FetchQueryResponse, err error) UnifiedQueryService {
	return NewUnifiedQueryService(
		stubPaginationService{resp: resp, err: err},
		stubStreamingService{},
		nil,
	)
}

// Every executed fetch query lands in the history with the identity carried
// in the context, for both success and failure (#103).
func TestFetchQueryRecordsHistory(t *testing.T) {
	unified := newTestUnifiedService(&model.FetchQueryResponse{QueryID: "q-1"}, nil)

	if _, err := unified.FetchQuery(service.WithQueryUser(context.Background(), "alice"), nil, &model.FetchQueryRequest{
		DataSourceID: "ds-1",
		SQL:          "SELECT 1",
	}); err != nil {
		t.Fatalf("FetchQuery failed: %v", err)
	}

	page, err := unified.GetQueryHistory(context.Background(), service.QueryHistoryFilter{User: "alice"})
	if err != nil {
		t.Fatalf("GetQueryHistory failed: %v", err)
	}
	if page.Total != 1 || len(page.Records) != 1 {
		t.Fatalf("history page = %d/%d, want 1/1", page.Total, len(page.Records))
	}
	record := page.Records[0]
	if record.QueryID != "q-1" || record.DataSourceID != "ds-1" || record.SQL != "SELECT 1" || record.Status != "success" {
		t.Errorf("record = %+v, want q-1/ds-1/SELECT 1/success", record)
	}
	if record.User != "alice" {
		t.Errorf("record.User = %q, want alice", record.User)
	}

	// The record is scoped: another user cannot see it.
	if page := historyList(t, unified, "bob"); page.Total != 0 {
		t.Errorf("bob sees %d of alice's records, want 0", page.Total)
	}
}

func historyList(t *testing.T, unified UnifiedQueryService, user string) *service.QueryHistoryPage {
	t.Helper()
	page, err := unified.GetQueryHistory(context.Background(), service.QueryHistoryFilter{User: user})
	if err != nil {
		t.Fatalf("GetQueryHistory failed: %v", err)
	}
	return page
}

// Failed queries are recorded as errors, and callers without identity land in
// the anonymous bucket rather than leaking into other users' views (#103).
func TestFetchQueryRecordsErrorsAndAnonymousCallers(t *testing.T) {
	unified := newTestUnifiedService(nil, errors.New("engine unavailable"))

	if _, err := unified.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: "ds-1",
		SQL:          "SELECT 1",
	}); err == nil {
		t.Fatal("FetchQuery unexpectedly succeeded")
	}

	anonymous := historyList(t, unified, "anonymous")
	if anonymous.Total != 1 || anonymous.Records[0].Status != "error" {
		t.Fatalf("anonymous page = %+v, want one error record", anonymous)
	}

	// The error record stays invisible to identified users.
	if page := historyList(t, unified, "alice"); page.Total != 0 {
		t.Errorf("alice sees %d anonymous records, want 0", page.Total)
	}
}

func TestFetchQueryRecordsPrometheusMetricsOnce(t *testing.T) {
	middleware.InitMetrics()

	row := []interface{}{int64(1)}
	response := &model.FetchQueryResponse{Entries: []*[]interface{}{&row, &row}}
	unified := newTestUnifiedService(response, nil)
	if _, err := unified.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: "high-cardinality-source-id",
		SQL:          "SELECT 1",
	}); err != nil {
		t.Fatalf("FetchQuery failed: %v", err)
	}

	body := scrapePrometheusMetrics(t)
	if got := prometheusMetricValue(t, body, `data_fabric_query_total{execution_mode="pagination",status="success"}`); got != 1 {
		t.Errorf("successful query total = %v, want 1", got)
	}
	if got := prometheusMetricValue(t, body, `data_fabric_query_rows_read_total{execution_mode="pagination"}`); got != 2 {
		t.Errorf("query rows read = %v, want 2", got)
	}
	if strings.Contains(body, "high-cardinality-source-id") || strings.Contains(body, "datasource_id=") {
		t.Fatal("query metrics exposed a data-source ID label")
	}

	failed := newTestUnifiedService(nil, errors.New("backend-specific detail"))
	if _, err := failed.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: "another-source-id",
		SQL:          "SELECT 1",
	}); err == nil {
		t.Fatal("FetchQuery unexpectedly succeeded")
	}
	body = scrapePrometheusMetrics(t)
	if got := prometheusMetricValue(t, body, `data_fabric_query_total{execution_mode="pagination",status="error"}`); got != 1 {
		t.Errorf("failed query total = %v, want 1", got)
	}
	if got := prometheusMetricValue(t, body, `data_fabric_query_errors_total{error_type="execution_error",execution_mode="pagination"}`); got != 1 {
		t.Errorf("query errors = %v, want 1", got)
	}
}

func scrapePrometheusMetrics(t *testing.T) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	middleware.PrometheusHandler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	if recorder.Code != 200 {
		t.Fatalf("metrics handler status = %d, want 200", recorder.Code)
	}
	return recorder.Body.String()
}

func prometheusMetricValue(t *testing.T, exposition, seriesPrefix string) float64 {
	t.Helper()
	for _, line := range strings.Split(exposition, "\n") {
		if strings.HasPrefix(line, seriesPrefix+" ") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				t.Fatalf("malformed metric sample: %q", line)
			}
			value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			if err != nil {
				t.Fatalf("parse metric value from %q: %v", line, err)
			}
			return value
		}
	}
	t.Fatalf("metric series %q not found", seriesPrefix)
	return 0
}

type routingPaginationStub struct {
	service.QueryService
	fetchResponse *model.FetchQueryResponse
	fetchErr      error
	nextResponse  *model.FetchQueryResponse
	nextErr       error
	fetchCalls    int
	nextCalls     int
	lastFetchType int
}

func (s *routingPaginationStub) FetchQuery(_ context.Context, _ *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	s.fetchCalls++
	if req != nil {
		s.lastFetchType = req.Type
	}
	return s.fetchResponse, s.fetchErr
}

func (s *routingPaginationStub) FetchNextBatch(context.Context, *security.Caller, string, string, string, int) (*model.FetchQueryResponse, error) {
	s.nextCalls++
	return s.nextResponse, s.nextErr
}

type routingStreamingStub struct {
	service.StreamingService
	supported     bool
	supportErr    error
	response      *model.FetchQueryResponse
	fetchErr      error
	nextResponse  *model.FetchQueryResponse
	nextErr       error
	supportCalls  int
	fetchCalls    int
	nextCalls     int
	lastFetchType int
}

func (s *routingStreamingStub) SupportsStreaming(context.Context, *security.Caller, string) (bool, error) {
	s.supportCalls++
	return s.supported, s.supportErr
}

func (s *routingStreamingStub) FetchWithStreaming(_ context.Context, _ *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	s.fetchCalls++
	s.lastFetchType = req.Type
	return s.response, s.fetchErr
}

func (s *routingStreamingStub) FetchNextBatchWithStreaming(context.Context, *security.Caller, string, string, string, int) (*model.FetchQueryResponse, error) {
	s.nextCalls++
	return s.nextResponse, s.nextErr
}

func newRoutingService(cfg *config.Config, pagination *routingPaginationStub, streaming *routingStreamingStub) *unifiedQueryService {
	return &unifiedQueryService{
		paginationService: pagination,
		streamingService:  streaming,
		config:            cfg,
		history:           service.NewQueryHistory(100),
	}
}

func TestAutoModeChoosesExecutionPath(t *testing.T) {
	tests := []struct {
		name       string
		configMode string
		prefer     bool
		request    model.FetchQueryRequest
		supported  bool
		wantMode   string
		wantStream bool
		wantProbe  bool
	}{
		{name: "small batch uses pagination", prefer: true, request: model.FetchQueryRequest{BatchSize: 999, SQL: "SELECT * FROM t"}, wantMode: executionModePagination},
		{name: "large batch uses supported streaming driver", prefer: true, request: model.FetchQueryRequest{BatchSize: 1000, SQL: "SELECT * FROM t"}, supported: true, wantMode: executionModeStreaming, wantStream: true, wantProbe: true},
		{name: "small explicit result limit uses pagination", prefer: true, request: model.FetchQueryRequest{BatchSize: 10000, SQL: "SELECT * FROM t LIMIT 100"}, supported: true, wantMode: executionModePagination},
		{name: "unsupported driver falls back", prefer: true, request: model.FetchQueryRequest{BatchSize: 10000, SQL: "SELECT * FROM t"}, wantMode: executionModePagination, wantProbe: true},
		{name: "requested stream falls back for unsupported driver", prefer: true, request: model.FetchQueryRequest{Type: 2, BatchSize: 10, SQL: "SELECT * FROM t"}, wantMode: executionModePagination, wantProbe: true},
		{name: "stream type requests streaming", prefer: false, request: model.FetchQueryRequest{Type: 2, BatchSize: 10, SQL: "SELECT * FROM t"}, supported: true, wantMode: executionModeStreaming, wantStream: true, wantProbe: true},
		{name: "sync type keeps pagination", prefer: true, request: model.FetchQueryRequest{Type: 1, BatchSize: 10000, SQL: "SELECT * FROM t"}, supported: true, wantMode: executionModePagination},
		{name: "preference disabled uses pagination", prefer: false, request: model.FetchQueryRequest{BatchSize: 10000, SQL: "SELECT * FROM t"}, supported: true, wantMode: executionModePagination},
		{name: "explicit streaming config overrides request hint", configMode: executionModeStreaming, request: model.FetchQueryRequest{Type: 1, BatchSize: 10, SQL: "SELECT * FROM t"}, wantMode: executionModeStreaming, wantStream: true},
		{name: "explicit pagination config overrides request hint", configMode: executionModePagination, request: model.FetchQueryRequest{Type: 2, BatchSize: 10000, SQL: "SELECT * FROM t"}, supported: true, wantMode: executionModePagination},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pagination := &routingPaginationStub{fetchResponse: &model.FetchQueryResponse{QueryID: "page-query"}}
			streaming := &routingStreamingStub{supported: tt.supported, response: &model.FetchQueryResponse{QueryID: "stream-query"}}
			cfg := &config.Config{Query: config.QueryConfig{ExecutionMode: tt.configMode, PreferStreaming: tt.prefer}}
			unified := newRoutingService(cfg, pagination, streaming)

			req := tt.request
			req.DataSourceID = "source-1"
			result, err := unified.FetchQuery(context.Background(), &security.Caller{UserID: "alice"}, &req)
			if err != nil {
				t.Fatalf("FetchQuery() error = %v", err)
			}
			if result.ExecutionMode != tt.wantMode {
				t.Errorf("ExecutionMode = %q, want %q", result.ExecutionMode, tt.wantMode)
			}
			if (streaming.fetchCalls == 1) != tt.wantStream {
				t.Errorf("stream calls = %d, wantStream=%v", streaming.fetchCalls, tt.wantStream)
			}
			if (streaming.supportCalls == 1) != tt.wantProbe {
				t.Errorf("capability probes = %d, wantProbe=%v", streaming.supportCalls, tt.wantProbe)
			}
			if tt.wantStream && streaming.lastFetchType != 2 {
				t.Errorf("stream request type = %d, want 2 so a continuation session is retained", streaming.lastFetchType)
			}
			if (pagination.fetchCalls == 1) == tt.wantStream {
				t.Errorf("pagination calls = %d, want exactly one path", pagination.fetchCalls)
			}
		})
	}
}

func TestAutoModeFallsBackOnlyBeforeStreamingStarts(t *testing.T) {
	cfg := &config.Config{Query: config.QueryConfig{ExecutionMode: executionModeAuto, PreferStreaming: true}}
	req := &model.FetchQueryRequest{DataSourceID: "source-1", Type: 2, BatchSize: 100, SQL: "SELECT * FROM t"}

	t.Run("stream capacity reached before query", func(t *testing.T) {
		pagination := &routingPaginationStub{fetchResponse: &model.FetchQueryResponse{QueryID: "page-query"}}
		streaming := &routingStreamingStub{
			supported: true,
			fetchErr:  errors.Join(errors.New("stream wrapper"), service.ErrMaxConcurrentStreams),
		}
		unified := newRoutingService(cfg, pagination, streaming)
		result, err := unified.FetchQuery(context.Background(), &security.Caller{UserID: "alice"}, req)
		if err != nil {
			t.Fatalf("FetchQuery() error = %v", err)
		}
		if result.ExecutionMode != executionModePagination || pagination.fetchCalls != 1 {
			t.Fatalf("result/mode/calls = %+v/%q/%d, want pagination fallback", result, result.ExecutionMode, pagination.fetchCalls)
		}
	})

	t.Run("runtime stream error is not retried", func(t *testing.T) {
		pagination := &routingPaginationStub{fetchResponse: &model.FetchQueryResponse{QueryID: "page-query"}}
		streaming := &routingStreamingStub{supported: true, fetchErr: errors.New("backend failed after stream start")}
		unified := newRoutingService(cfg, pagination, streaming)
		if _, err := unified.FetchQuery(context.Background(), &security.Caller{UserID: "alice"}, req); err == nil {
			t.Fatal("FetchQuery() error = nil, want streaming failure")
		}
		if pagination.fetchCalls != 0 {
			t.Fatalf("pagination retry count = %d, want 0 after stream execution started", pagination.fetchCalls)
		}
	})

	t.Run("capability lookup error is returned", func(t *testing.T) {
		pagination := &routingPaginationStub{}
		streaming := &routingStreamingStub{supportErr: errors.New("metadata unavailable")}
		unified := newRoutingService(cfg, pagination, streaming)
		if _, err := unified.FetchQuery(context.Background(), &security.Caller{UserID: "alice"}, req); err == nil {
			t.Fatal("FetchQuery() error = nil, want capability lookup error")
		}
		if streaming.fetchCalls != 0 || pagination.fetchCalls != 0 {
			t.Fatalf("query calls = streaming:%d pagination:%d, want none", streaming.fetchCalls, pagination.fetchCalls)
		}
	})
}

func TestFetchNextBatchAutoDetectsSessionEngine(t *testing.T) {
	t.Run("streaming session", func(t *testing.T) {
		pagination := &routingPaginationStub{}
		streaming := &routingStreamingStub{nextResponse: &model.FetchQueryResponse{QueryID: "stream-query"}}
		unified := newRoutingService(&config.Config{Query: config.QueryConfig{ExecutionMode: executionModeAuto}}, pagination, streaming)
		result, err := unified.FetchNextBatch(context.Background(), &security.Caller{UserID: "alice"}, "q", "slug", "token", 100)
		if err != nil || result.ExecutionMode != executionModeStreaming || pagination.nextCalls != 0 {
			t.Fatalf("result/error/pagination calls = %+v/%v/%d, want streaming session", result, err, pagination.nextCalls)
		}
	})

	t.Run("pagination session fallback", func(t *testing.T) {
		pagination := &routingPaginationStub{nextResponse: &model.FetchQueryResponse{QueryID: "page-query"}}
		streaming := &routingStreamingStub{nextErr: service.ErrQuerySessionUnavailable}
		unified := newRoutingService(&config.Config{Query: config.QueryConfig{ExecutionMode: executionModeAuto}}, pagination, streaming)
		result, err := unified.FetchNextBatch(context.Background(), &security.Caller{UserID: "alice"}, "q", "slug", "token", 100)
		if err != nil || result.ExecutionMode != executionModePagination || pagination.nextCalls != 1 {
			t.Fatalf("result/error/pagination calls = %+v/%v/%d, want pagination fallback", result, err, pagination.nextCalls)
		}
	})

	t.Run("invalid streaming token is not hidden by fallback", func(t *testing.T) {
		pagination := &routingPaginationStub{}
		streaming := &routingStreamingStub{nextErr: service.ErrInvalidQuerySession}
		unified := newRoutingService(&config.Config{Query: config.QueryConfig{ExecutionMode: executionModeAuto}}, pagination, streaming)
		if _, err := unified.FetchNextBatch(context.Background(), &security.Caller{UserID: "alice"}, "q", "slug", "token", 100); !errors.Is(err, service.ErrInvalidQuerySession) {
			t.Fatalf("FetchNextBatch() error = %v, want invalid-session error", err)
		}
		if pagination.nextCalls != 0 {
			t.Fatalf("pagination fallback count = %d, want 0 for invalid streaming credentials", pagination.nextCalls)
		}
	})
}

func TestSmallResultLimitDetection(t *testing.T) {
	tests := []struct {
		sql  string
		want bool
	}{
		{"SELECT * FROM t LIMIT 50", true},
		{"SELECT * FROM t LIMIT 20, 50", true},
		{"SELECT * FROM t FETCH FIRST 10 ROWS ONLY", true},
		{"SELECT TOP (5) * FROM t", true},
		{"SELECT * FROM t LIMIT 5000", false},
		{"SELECT * FROM t", false},
	}
	for _, tt := range tests {
		if got := hasSmallResultLimit(tt.sql); got != tt.want {
			t.Errorf("hasSmallResultLimit(%q) = %v, want %v", tt.sql, got, tt.want)
		}
	}
}
