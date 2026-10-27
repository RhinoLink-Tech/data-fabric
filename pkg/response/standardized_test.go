package response

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/utils"
)

func TestStandardResponseConstructors(t *testing.T) {
	payload := map[string]interface{}{"id": "item-1", "count": float64(2)}
	tests := []struct {
		name     string
		create   func() *StandardResponse
		want     StandardResponse
		wantJSON string
	}{
		{
			name: "success with data",
			create: func() *StandardResponse {
				return SuccessResponse(payload, "request-1")
			},
			want:     StandardResponse{Success: true, Data: payload, CorrelationID: "request-1"},
			wantJSON: `{"success":true,"data":{"id":"item-1","count":2},"correlationId":"request-1"}`,
		},
		{
			name: "success without data or correlation ID",
			create: func() *StandardResponse {
				return SuccessResponse(nil, "")
			},
			want:     StandardResponse{Success: true},
			wantJSON: `{"success":true,"correlationId":""}`,
		},
		{
			name: "success with message",
			create: func() *StandardResponse {
				return SuccessMessageResponse("Item saved", "request-2")
			},
			want:     StandardResponse{Success: true, Message: "Item saved", CorrelationID: "request-2"},
			wantJSON: `{"success":true,"message":"Item saved","correlationId":"request-2"}`,
		},
		{
			name: "success with empty message",
			create: func() *StandardResponse {
				return SuccessMessageResponse("", "request-3")
			},
			want:     StandardResponse{Success: true, CorrelationID: "request-3"},
			wantJSON: `{"success":true,"correlationId":"request-3"}`,
		},
		{
			name: "error with details",
			create: func() *StandardResponse {
				return ErrorResponse("INVALID_ITEM", "Invalid item", "name is required", "request-4")
			},
			want: StandardResponse{
				Error:         &ErrorInfo{Code: "INVALID_ITEM", Message: "Invalid item", Details: "name is required"},
				CorrelationID: "request-4",
			},
			wantJSON: `{"success":false,"error":{"code":"INVALID_ITEM","message":"Invalid item","details":"name is required"},"correlationId":"request-4"}`,
		},
		{
			name: "error without details",
			create: func() *StandardResponse {
				return ErrorResponse("NOT_FOUND", "Item missing", "", "request-5")
			},
			want: StandardResponse{
				Error:         &ErrorInfo{Code: "NOT_FOUND", Message: "Item missing"},
				CorrelationID: "request-5",
			},
			wantJSON: `{"success":false,"error":{"code":"NOT_FOUND","message":"Item missing"},"correlationId":"request-5"}`,
		},
		{
			name: "application error with details and private cause",
			create: func() *StandardResponse {
				return ErrorResponseFromAppError(&utils.AppError{
					Code:    utils.ErrCodeQueryFailed,
					Message: "Query failed",
					Details: "Please retry",
					Cause:   errors.New("private driver failure"),
				}, "request-6")
			},
			want: StandardResponse{
				Error:         &ErrorInfo{Code: utils.ErrCodeQueryFailed, Message: "Query failed", Details: "Please retry"},
				CorrelationID: "request-6",
			},
			wantJSON: `{"success":false,"error":{"code":"QUERY_FAILED","message":"Query failed","details":"Please retry"},"correlationId":"request-6"}`,
		},
		{
			name: "application error without details",
			create: func() *StandardResponse {
				return ErrorResponseFromAppError(&utils.AppError{
					Code: utils.ErrCodeNotFound, Message: "Item missing",
				}, "request-7")
			},
			want: StandardResponse{
				Error:         &ErrorInfo{Code: utils.ErrCodeNotFound, Message: "Item missing"},
				CorrelationID: "request-7",
			},
			wantJSON: `{"success":false,"error":{"code":"NOT_FOUND","message":"Item missing"},"correlationId":"request-7"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.create()
			if got == nil {
				t.Fatal("constructor returned nil")
			}
			if got.Timestamp.IsZero() {
				t.Error("Timestamp is zero")
			}
			want := tc.want
			want.Timestamp = got.Timestamp
			if !reflect.DeepEqual(*got, want) {
				t.Errorf("response = %#v, want %#v", *got, want)
			}

			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			var fields map[string]interface{}
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatalf("decode response fields: %v", err)
			}
			timestampText, ok := fields["timestamp"].(string)
			if !ok {
				t.Fatalf("JSON timestamp = %#v, want a string", fields["timestamp"])
			}
			timestamp, err := time.Parse(time.RFC3339Nano, timestampText)
			if err != nil {
				t.Fatalf("parse JSON timestamp: %v", err)
			}
			if !timestamp.Equal(got.Timestamp) {
				t.Errorf("JSON timestamp = %v, want %v", timestamp, got.Timestamp)
			}
			delete(fields, "timestamp")

			var wantFields map[string]interface{}
			if err := json.Unmarshal([]byte(tc.wantJSON), &wantFields); err != nil {
				t.Fatalf("decode expected JSON: %v", err)
			}
			if !reflect.DeepEqual(fields, wantFields) {
				t.Errorf("JSON fields = %#v, want %#v", fields, wantFields)
			}
		})
	}
}
