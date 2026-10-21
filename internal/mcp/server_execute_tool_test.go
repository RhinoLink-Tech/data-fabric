package mcp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	unifiedservice "github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
)

// stubQueryService captures the FetchQueryRequest the tool handler built and
// replays a canned response or error. Other interface methods stay nil via
// the embedded interface; no test calls them.
type stubQueryService struct {
	unifiedservice.UnifiedQueryService

	captured *model.FetchQueryRequest
	response *model.FetchQueryResponse
	err      error
}

func (s *stubQueryService) FetchQuery(_ context.Context, _ *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	copied := *req
	s.captured = &copied
	if s.err != nil {
		return nil, s.err
	}
	if s.response != nil {
		return s.response, nil
	}
	return &model.FetchQueryResponse{
		QueryID: "query-1",
		Slug:    "slug-1",
		Token:   "token-1",
		Entries: []*[]interface{}{{"row-value"}},
	}, nil
}

func executeServer(service *stubQueryService) *MCPServer {
	return NewMCPServer(service, listingRepoStub{}, &stubTableDescriber{})
}

func executeArguments(overrides map[string]any) map[string]any {
	arguments := map[string]any{
		"datasource_id": "ds-1",
		"sql":           "SELECT customer FROM orders",
	}
	for key, value := range overrides {
		arguments[key] = value
	}
	return arguments
}

// execute_sql_query applies documented defaults and clamps to the documented
// bounds before calling the query service; parameters pass through untouched
// for the service to normalize (#181).
func TestExecuteSQLQueryAppliesDefaultsAndBounds(t *testing.T) {
	tests := []struct {
		name        string
		overrides   map[string]any
		wantBatch   int
		wantTimeout int
		wantRequest *model.FetchQueryRequest
	}{
		{
			name:        "defaults",
			overrides:   nil,
			wantBatch:   10000,
			wantTimeout: 60,
		},
		{
			name:        "zero batch size falls back to default",
			overrides:   map[string]any{"batch_size": 0},
			wantBatch:   10000,
			wantTimeout: 60,
		},
		{
			name:        "negative batch size falls back to default",
			overrides:   map[string]any{"batch_size": -5},
			wantBatch:   10000,
			wantTimeout: 60,
		},
		{
			name:        "oversized batch size is clamped",
			overrides:   map[string]any{"batch_size": 20000},
			wantBatch:   10000,
			wantTimeout: 60,
		},
		{
			name:        "small batch size is honored",
			overrides:   map[string]any{"batch_size": 500},
			wantBatch:   500,
			wantTimeout: 60,
		},
		{
			name:        "timeout default",
			overrides:   map[string]any{"timeout": 0},
			wantBatch:   10000,
			wantTimeout: 60,
		},
		{
			name:        "timeout is clamped to 1800",
			overrides:   map[string]any{"timeout": 9999},
			wantBatch:   10000,
			wantTimeout: 1800,
		},
		{
			name:        "timeout is honored",
			overrides:   map[string]any{"timeout": 300},
			wantBatch:   10000,
			wantTimeout: 300,
		},
		{
			name: "parameters pass through for the service to bind",
			overrides: map[string]any{
				"parameters":     []interface{}{float64(1), "text", nil, true},
				"source_dialect": "mysql",
			},
			wantBatch:   10000,
			wantTimeout: 60,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &stubQueryService{}
			srv := executeServer(service)

			result, err := srv.handleExecuteSQLQuery(context.Background(), callToolRequest("execute_sql_query", executeArguments(tt.overrides)))
			if err != nil {
				t.Fatalf("handleExecuteSQLQuery() error = %v", err)
			}
			if result.IsError {
				t.Fatalf("handleExecuteSQLQuery() reported tool error: %s", describeTableText(t, result))
			}
			if service.captured == nil {
				t.Fatal("query service was never called")
			}
			got := service.captured
			if got.BatchSize != tt.wantBatch {
				t.Errorf("batch size = %d, want %d", got.BatchSize, tt.wantBatch)
			}
			if got.Timeout != tt.wantTimeout {
				t.Errorf("timeout = %d, want %d", got.Timeout, tt.wantTimeout)
			}
			if got.DataSourceID != "ds-1" || got.SQL != "SELECT customer FROM orders" || got.Type != 2 {
				t.Errorf("request identity = %q/%q/type %d, want ds-1/SELECT customer FROM orders/type 2", got.DataSourceID, got.SQL, got.Type)
			}
			if tt.overrides["parameters"] != nil {
				want := []interface{}{float64(1), "text", nil, true}
				if !reflect.DeepEqual(got.Parameters, want) {
					t.Errorf("parameters = %#v, want %#v", got.Parameters, want)
				}
			}
			if want, ok := tt.overrides["source_dialect"]; ok && got.SourceDialect != want {
				t.Errorf("source dialect = %q, want %v", got.SourceDialect, want)
			}
		})
	}
}

// A non-array parameters value is rejected at the tool level before the query
// service is involved (#181, re-anchoring the behavior after later refactors
// of the handler).
func TestExecuteSQLQueryRejectsNonArrayParameters(t *testing.T) {
	service := &stubQueryService{}
	srv := executeServer(service)

	result, err := srv.handleExecuteSQLQuery(context.Background(), callToolRequest("execute_sql_query", executeArguments(map[string]any{"parameters": "1"})))
	if err != nil {
		t.Fatalf("handleExecuteSQLQuery() error = %v, want a tool-level error", err)
	}
	if !result.IsError {
		t.Fatalf("result is a success response, want isError: %s", describeTableText(t, result))
	}
	if text := describeTableText(t, result); !strings.Contains(text, "parameters must be an array") {
		t.Fatalf("tool error text = %q, want the parameters guidance", text)
	}
	if service.captured != nil {
		t.Error("query service was called despite invalid parameters")
	}
}

// Service failures surface through the sanitized message mapping: the client
// gets stable, actionable text and never the internal error details (#181).
func TestExecuteSQLQuerySanitizesServiceErrors(t *testing.T) {
	service := &stubQueryService{err: fmt.Errorf("dial tcp 10.0.0.5:3306: connection refused (user root@db-internal, dsn root:pw@db-internal:3306/sample)")}
	srv := executeServer(service)

	result, err := srv.handleExecuteSQLQuery(context.Background(), callToolRequest("execute_sql_query", executeArguments(nil)))
	if err != nil {
		t.Fatalf("handleExecuteSQLQuery() error = %v, want a tool-level error", err)
	}
	if !result.IsError {
		t.Fatalf("result is a success response, want isError")
	}
	text := describeTableText(t, result)
	for _, leak := range []string{"10.0.0.5", "root@db-internal", "dsn", "pw@"} {
		if strings.Contains(text, leak) {
			t.Errorf("sanitized message leaks %q: %s", leak, text)
		}
	}
	if !strings.Contains(text, "The data-source query failed") {
		t.Fatalf("tool error text = %q, want the stable generic failure message", text)
	}
}

// A denied caller gets the authorization message, also sanitized (#181).
func TestExecuteSQLQueryMapsAccessDenied(t *testing.T) {
	service := &stubQueryService{err: errors.Join(errors.New("wrap"), security.ErrDataSourceAccessDenied)}
	srv := executeServer(service)

	result, err := srv.handleExecuteSQLQuery(context.Background(), callToolRequest("execute_sql_query", executeArguments(nil)))
	if err != nil {
		t.Fatalf("handleExecuteSQLQuery() error = %v, want a tool-level error", err)
	}
	if !result.IsError {
		t.Fatal("result is a success response, want isError")
	}
	if text := describeTableText(t, result); !strings.Contains(text, "You do not have access to this data source") {
		t.Fatalf("tool error text = %q, want the access-denied message", text)
	}
}

// A successful query round-trips the service response as JSON content (#181).
func TestExecuteSQLQuerySuccessRoundTrip(t *testing.T) {
	service := &stubQueryService{response: &model.FetchQueryResponse{
		QueryID: "query-42",
		Entries: []*[]interface{}{{"Ada Lovelace", 250.0}},
	}}
	srv := executeServer(service)

	result, err := srv.handleExecuteSQLQuery(context.Background(), callToolRequest("execute_sql_query", executeArguments(nil)))
	if err != nil {
		t.Fatalf("handleExecuteSQLQuery() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("handleExecuteSQLQuery() reported tool error: %s", describeTableText(t, result))
	}
	if text := describeTableText(t, result); !strings.Contains(text, `"queryId":"query-42"`) || !strings.Contains(text, "Ada Lovelace") {
		t.Fatalf("response text = %q, want the query id and row payload", text)
	}
}
