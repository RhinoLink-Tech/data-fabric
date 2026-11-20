package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
)

type countModeService struct {
	unifiedservice.UnifiedQueryService
	called bool
	mode   model.QueryCountMode
}

func (s *countModeService) FetchQuery(_ context.Context, _ *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	s.called = true
	s.mode = req.CountMode
	if req.CountMode == model.QueryCountNone {
		more := false
		return &model.FetchQueryResponse{CountMode: model.QueryCountNone, HasMore: &more}, nil
	}
	return &model.FetchQueryResponse{TotalCount: 3}, nil
}
func TestRESTCountModeInputAndUnknownResponse(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		valid       bool
		mode        model.QueryCountMode
	}{
		{"omitted", "", true, ""}, {"exact", `,"count_mode":"exact"`, true, model.QueryCountExact},
		{"none", `,"count_mode":"none"`, true, model.QueryCountNone}, {"invalid", `,"count_mode":"foo"`, false, ""},
		{"null", `,"count_mode":null`, false, ""}, {"number", `,"count_mode":3`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &countModeService{}
			c := NewQueryController(service)
			body := `{"dataSourceId":"11111111-1111-4111-8111-111111111111","sql":"SELECT 1"` + tc.field + `}`
			ctx, rec := queryErrorContext(t, http.MethodPost, "/api/v1/fetch", body)
			c.FetchQuery(ctx)
			if !tc.valid {
				if rec.Code != http.StatusBadRequest || service.called {
					t.Fatalf("status=%d called=%v body=%s", rec.Code, service.called, rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusOK || !service.called || service.mode != tc.mode {
				t.Fatalf("status=%d mode=%q body=%s", rec.Code, service.mode, rec.Body.String())
			}
			var result struct {
				Data map[string]interface{} `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if tc.mode == model.QueryCountNone && (result.Data["countMode"] != "none" || result.Data["totalCount"] != nil || result.Data["hasMore"] != false) {
				t.Fatalf("data=%v", result.Data)
			}
		})
	}
}
