package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
)

func TestQueryFailureStatusAndCodeAreStable(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "timeout", err: context.DeadlineExceeded, wantStatus: 504, wantCode: "QUERY_TIMEOUT"},
		{name: "invalid select", err: security.ErrNotSelectQuery, wantStatus: 400, wantCode: "INVALID_QUERY"},
		{name: "syntax error", err: security.ErrSQLSyntaxError, wantStatus: 400, wantCode: "INVALID_QUERY"},
		{name: "table access denied", err: security.ErrTableAccessDenied, wantStatus: 403, wantCode: "TABLE_ACCESS_DENIED"},
		{name: "dangerous statement", err: security.ErrDangerousKeyword, wantStatus: 400, wantCode: "INVALID_QUERY"},
		{name: "unavailable session", err: service.ErrQuerySessionUnavailable, wantStatus: 404, wantCode: "QUERY_SESSION_UNAVAILABLE"},
		{name: "inactive data source", err: fmt.Errorf("wrapped: %w", service.ErrDataSourceInactive), wantStatus: 409, wantCode: "DATASOURCE_INACTIVE"},
		{name: "engine failure", err: errors.New("internal coordinator URL contains password=secret"), wantStatus: 502, wantCode: "QUERY_ENGINE_ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, code := queryFailureStatusAndCode(tt.err)
			if status != tt.wantStatus || code != tt.wantCode {
				t.Fatalf("queryFailureStatusAndCode() = %d/%q, want %d/%q", status, code, tt.wantStatus, tt.wantCode)
			}
		})
	}
}
