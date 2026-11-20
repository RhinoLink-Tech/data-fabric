package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
)

type initialLimitsService struct {
	unifiedservice.UnifiedQueryService
	request  model.FetchQueryRequest
	deadline time.Time
}

func (s *initialLimitsService) FetchQuery(ctx context.Context, _ *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	s.request = *req
	s.deadline, _ = ctx.Deadline()
	return &model.FetchQueryResponse{QueryID: "q1"}, nil
}

func TestFetchQueryReportsNormalizedInitialLimits(t *testing.T) {
	for _, tc := range []struct {
		name, fields   string
		batch, timeout int
	}{
		{"omitted", "", 10000, 60},
		{"valid", `,"batch_size":25,"timeout":12`, 25, 12},
		{"minimum", `,"batch_size":1,"timeout":1`, 1, 1},
		{"maximum", `,"batch_size":10000,"timeout":1800`, 10000, 1800},
		{"zero", `,"batch_size":0,"timeout":0`, 10000, 60},
		{"negative", `,"batch_size":-8,"timeout":-9`, 10000, 60},
		{"oversized", `,"batch_size":10001,"timeout":1801`, 10000, 1800},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &initialLimitsService{}
			controller := NewQueryController(stub)
			body := `{"dataSourceId":"11111111-1111-4111-8111-111111111111","sql":"SELECT 1"` + tc.fields + `}`
			ctx, recorder := queryErrorContext(t, http.MethodPost, "/api/v1/fetch", body)
			controller.FetchQuery(ctx)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
			}
			var got struct {
				Data struct {
					AppliedBatchSize    int `json:"appliedBatchSize"`
					TimeoutLimitSeconds int `json:"timeoutLimitSeconds"`
				} `json:"data"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if stub.request.BatchSize != tc.batch || stub.request.Timeout != tc.timeout || got.Data.AppliedBatchSize != tc.batch || got.Data.TimeoutLimitSeconds != tc.timeout {
				t.Fatalf("forwarded=%d/%d response=%d/%d want=%d/%d", stub.request.BatchSize, stub.request.Timeout, got.Data.AppliedBatchSize, got.Data.TimeoutLimitSeconds, tc.batch, tc.timeout)
			}
		})
	}
}

func TestFetchQueryRespectsEarlierUpstreamDeadline(t *testing.T) {
	stub := &initialLimitsService{}
	controller := NewQueryController(stub)
	body := `{"dataSourceId":"11111111-1111-4111-8111-111111111111","sql":"SELECT 1","timeout":300}`
	ctx, recorder := queryErrorContext(t, http.MethodPost, "/api/v1/fetch", body)
	earlier := time.Now().Add(5 * time.Second)
	parent, cancel := context.WithDeadline(ctx.Request.Context(), earlier)
	defer cancel()
	ctx.Request = ctx.Request.WithContext(parent)
	controller.FetchQuery(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d", recorder.Code)
	}
	if stub.deadline.IsZero() || stub.deadline.After(earlier) {
		t.Fatalf("query did not honor upstream deadline: %v", stub.deadline)
	}
	var got struct {
		Data struct {
			TimeoutLimitSeconds int `json:"timeoutLimitSeconds"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Data.TimeoutLimitSeconds != 300 {
		t.Fatalf("expected normalized request upper bound, got %d", got.Data.TimeoutLimitSeconds)
	}
}
