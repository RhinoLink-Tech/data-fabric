package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	unifiedservice "github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
)

type captureContinuationSize struct {
	unifiedservice.UnifiedQueryService
	size int
}

func (s *captureContinuationSize) FetchNextBatch(_ context.Context, _ *security.Caller, _, _, _ string, size int) (*model.FetchQueryResponse, error) {
	s.size = size
	return &model.FetchQueryResponse{}, nil
}
func TestMCPContinuationForwardsOptionalSize(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
		want int
	}{{"omitted", nil, 0}, {"explicit", map[string]any{"batch_size": float64(2)}, 2}, {"invalid retains old fallback", map[string]any{"batch_size": float64(-1)}, 10000}} {
		t.Run(tc.name, func(t *testing.T) {
			q := &captureContinuationSize{}
			server := NewMCPServer(q, nil, nil)
			result, err := server.handleFetchNextBatch(context.Background(), callToolRequest("fetch_next_batch", tc.args))
			if err != nil || result.IsError || q.size != tc.want {
				t.Fatalf("size=%d want=%d result=%+v err=%v", q.size, tc.want, result, err)
			}
			text := describeTableText(t, result)
			if strings.Contains(text, "appliedBatchSize") || strings.Contains(text, "timeoutLimitSeconds") {
				t.Fatalf("continuation must not report initial-request limits: %s", text)
			}
		})
	}
}

func (s *captureContinuationSize) FetchQuery(_ context.Context, _ *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	s.size = req.BatchSize
	return &model.FetchQueryResponse{}, nil
}
func TestMCPInitialQueryKeepsDefaultBatchSize(t *testing.T) {
	q := &captureContinuationSize{}
	server := NewMCPServer(q, nil, nil)
	result, err := server.handleExecuteSQLQuery(context.Background(), callToolRequest("execute_sql_query", map[string]any{"datasource_id": "source", "sql": "SELECT 1"}))
	if err != nil || result.IsError || q.size != 10000 {
		t.Fatalf("size=%d result=%+v err=%v", q.size, result, err)
	}
}
