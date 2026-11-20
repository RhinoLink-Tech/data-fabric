package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestMCPCountModeInputAndUnknownResponse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  map[string]any
		valid bool
		want  model.QueryCountMode
	}{
		{"omitted", nil, true, ""},
		{"exact", map[string]any{"count_mode": "exact"}, true, model.QueryCountExact},
		{"none", map[string]any{"count_mode": "none"}, true, model.QueryCountNone},
		{"invalid", map[string]any{"count_mode": "other"}, false, ""},
		{"null", map[string]any{"count_mode": nil}, false, ""},
		{"number", map[string]any{"count_mode": 1}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			more := false
			response := &model.FetchQueryResponse{TotalCount: 3}
			if tc.want == model.QueryCountNone {
				response.CountMode = model.QueryCountNone
				response.HasMore = &more
			}
			service := &stubQueryService{response: response}
			result, err := executeServer(service).handleExecuteSQLQuery(context.Background(), callToolRequest("execute_sql_query", executeArguments(tc.args)))
			if err != nil || result.IsError == tc.valid {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if !tc.valid {
				if service.captured != nil {
					t.Fatal("invalid mode reached query service")
				}
				return
			}
			if service.captured == nil || service.captured.CountMode != tc.want {
				t.Fatalf("forwarded=%+v want=%q", service.captured, tc.want)
			}
			var got map[string]interface{}
			if err := json.Unmarshal([]byte(describeTableText(t, result)), &got); err != nil {
				t.Fatal(err)
			}
			if tc.want == model.QueryCountNone && (got["countMode"] != "none" || got["totalCount"] != nil || got["hasMore"] != false) {
				t.Fatalf("response=%v", got)
			}
		})
	}
}
