package controller

import (
	"context"
	"net/http"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
	"github.com/gin-gonic/gin"
)

type batchSizeRecordingService struct {
	unifiedservice.UnifiedQueryService
	calls     int
	batchSize int
}

func (s *batchSizeRecordingService) FetchNextBatch(_ context.Context, _ *security.Caller, _, _, _ string, batchSize int) (*model.FetchQueryResponse, error) {
	s.calls++
	s.batchSize = batchSize
	return &model.FetchQueryResponse{}, nil
}

func TestFetchNextBatchBatchSizeValidation(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantCode  int
		wantSize  int
		wantCalls int
	}{
		{name: "omitted keeps default", query: "", wantCode: http.StatusOK, wantSize: 10000, wantCalls: 1},
		{name: "lower boundary", query: "batch_size=1", wantCode: http.StatusOK, wantSize: 1, wantCalls: 1},
		{name: "upper boundary", query: "batch_size=10000", wantCode: http.StatusOK, wantSize: 10000, wantCalls: 1},
		{name: "valid middle value", query: "batch_size=2", wantCode: http.StatusOK, wantSize: 2, wantCalls: 1},
		{name: "explicit empty", query: "batch_size=", wantCode: http.StatusBadRequest, wantCalls: 0},
		{name: "key without value", query: "batch_size", wantCode: http.StatusBadRequest, wantCalls: 0},
		{name: "whitespace", query: "batch_size=%20", wantCode: http.StatusBadRequest, wantCalls: 0},
		{name: "malformed", query: "batch_size=abc", wantCode: http.StatusBadRequest, wantCalls: 0},
		{name: "trailing garbage", query: "batch_size=10x", wantCode: http.StatusBadRequest, wantCalls: 0},
		{name: "negative", query: "batch_size=-1", wantCode: http.StatusBadRequest, wantCalls: 0},
		{name: "zero", query: "batch_size=0", wantCode: http.StatusBadRequest, wantCalls: 0},
		{name: "over limit", query: "batch_size=10001", wantCode: http.StatusBadRequest, wantCalls: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := &batchSizeRecordingService{}
			controller := NewQueryController(stub)
			path := "/api/v1/fetch/q1/slug1/token1"
			if tc.query != "" {
				path += "?" + tc.query
			}
			ctx, recorder := queryErrorContext(t, http.MethodGet, path, "")
			ctx.Params = []gin.Param{{Key: "query_id", Value: "q1"}, {Key: "slug", Value: "slug1"}, {Key: "token", Value: "token1"}}

			controller.FetchNextBatch(ctx)

			if recorder.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d; body: %s", recorder.Code, tc.wantCode, recorder.Body.String())
			}
			if stub.calls != tc.wantCalls {
				t.Fatalf("service calls = %d, want %d (invalid batch_size must not reach the service)", stub.calls, tc.wantCalls)
			}
			if tc.wantCalls == 1 && stub.batchSize != tc.wantSize {
				t.Fatalf("forwarded batch size = %d, want %d", stub.batchSize, tc.wantSize)
			}
		})
	}
}
