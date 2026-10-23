package utils

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestAppErrorError(t *testing.T) {
	tests := []struct {
		name string
		err  AppError
		want string
	}{
		{
			name: "without details",
			err:  AppError{Code: ErrCodeNotFound, Message: "Data source not found"},
			want: "NOT_FOUND: Data source not found",
		},
		{
			name: "with details",
			err:  AppError{Code: ErrCodeDatabaseError, Message: "Database error", Details: "connection refused"},
			want: "DATABASE_ERROR: Database error - connection refused",
		},
		{
			name: "cause is not included",
			err:  AppError{Code: ErrCodeQueryFailed, Message: "Query execution failed", Cause: errors.New("driver failure")},
			want: "QUERY_FAILED: Query execution failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAppErrorUnwrap(t *testing.T) {
	cause := errors.New("connection refused")
	wrappedCause := fmt.Errorf("connect: %w", cause)
	tests := []struct {
		name  string
		cause error
		want  bool
	}{
		{name: "direct cause", cause: cause, want: true},
		{name: "wrapped cause", cause: wrappedCause, want: true},
		{name: "absent cause", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &AppError{Code: ErrCodeDatabaseError, Message: "Database error", Cause: tt.cause}
			if got := err.Unwrap(); got != tt.cause {
				t.Errorf("Unwrap() = %v, want original cause %v", got, tt.cause)
			}
			if got := errors.Is(err, cause); got != tt.want {
				t.Errorf("errors.Is(err, cause) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestErrorBuilder(t *testing.T) {
	cause := errors.New("driver failure")
	tests := []struct {
		name        string
		code        string
		message     string
		details     string
		cause       error
		wantMessage string
	}{
		{name: "invalid request", code: ErrCodeInvalidRequest, wantMessage: "The request is invalid"},
		{name: "database error", code: ErrCodeDatabaseError, wantMessage: "Database error"},
		{name: "invalid uuid", code: ErrCodeInvalidUUID, wantMessage: "Invalid UUID format"},
		{name: "unknown code", code: "CUSTOM_ERROR", wantMessage: "Unknown error"},
		{name: "explicit message", code: ErrCodeNotFound, message: "Requested source is missing", wantMessage: "Requested source is missing"},
		{name: "explicit message for unknown code", code: "CUSTOM_ERROR", message: "Custom failure", wantMessage: "Custom failure"},
		{name: "details and cause", code: ErrCodeDatabaseError, details: "query failed", cause: cause, wantMessage: "Database error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewErrorBuilder(tt.code).
				WithMessage(tt.message).
				WithDetails(tt.details).
				WithCause(tt.cause).
				Build()
			if err.Code != tt.code {
				t.Errorf("Code = %q, want %q", err.Code, tt.code)
			}
			if err.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", err.Message, tt.wantMessage)
			}
			if err.Details != tt.details {
				t.Errorf("Details = %q, want %q", err.Details, tt.details)
			}
			if err.Cause != tt.cause {
				t.Errorf("Cause = %v, want original cause %v", err.Cause, tt.cause)
			}
		})
	}
}

func TestErrorConstructors(t *testing.T) {
	cause := errors.New("connection refused")
	tests := []struct {
		name        string
		build       func() *AppError
		wantCode    string
		wantMessage string
		wantDetails string
		wantCause   error
	}{
		{
			name:        "database",
			build:       func() *AppError { return NewDatabaseError(cause, "cannot connect to source") },
			wantCode:    ErrCodeDatabaseError,
			wantMessage: "Database error",
			wantDetails: "cannot connect to source",
			wantCause:   cause,
		},
		{
			name:        "not found",
			build:       func() *AppError { return NewNotFoundError("data source") },
			wantCode:    ErrCodeNotFound,
			wantMessage: "data source not found",
		},
		{
			name:        "conflict",
			build:       func() *AppError { return NewConflictError("data source", "name already exists") },
			wantCode:    ErrCodeConflict,
			wantMessage: "data source conflict",
			wantDetails: "name already exists",
		},
		{
			name:        "validation",
			build:       func() *AppError { return NewValidationError("Invalid configuration", "host is required") },
			wantCode:    ErrCodeValidationFailed,
			wantMessage: "Invalid configuration",
			wantDetails: "host is required",
		},
		{
			name:        "authentication",
			build:       func() *AppError { return NewAuthenticationError("Please sign in") },
			wantCode:    ErrCodeUnauthorized,
			wantMessage: "Please sign in",
		},
		{
			name:        "authorization",
			build:       func() *AppError { return NewAuthorizationError("Permission denied") },
			wantCode:    ErrCodeForbidden,
			wantMessage: "Permission denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.build()
			if err.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", err.Code, tt.wantCode)
			}
			if err.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", err.Message, tt.wantMessage)
			}
			if err.Details != tt.wantDetails {
				t.Errorf("Details = %q, want %q", err.Details, tt.wantDetails)
			}
			if err.Cause != tt.wantCause {
				t.Errorf("Cause = %v, want original cause %v", err.Cause, tt.wantCause)
			}
		})
	}
}

func TestErrorHelpers(t *testing.T) {
	notFound := NewNotFoundError("data source")
	wrapped := fmt.Errorf("load data source: %w", notFound)
	unknown := NewErrorBuilder("CUSTOM_ERROR").Build()
	outer := NewErrorBuilder(ErrCodeConflict).WithCause(notFound).Build()
	custom := NewErrorBuilder(ErrCodeNotFound).WithStatus(http.StatusGone).Build()

	tests := []struct {
		name       string
		err        error
		code       string
		wantType   bool
		wantStatus int
	}{
		{"direct", notFound, ErrCodeNotFound, true, http.StatusNotFound},
		{"direct different code", notFound, ErrCodeConflict, false, http.StatusNotFound},
		{"wrapped", wrapped, ErrCodeNotFound, true, http.StatusNotFound},
		{"wrapped twice", fmt.Errorf("request: %w", wrapped), ErrCodeNotFound, true, http.StatusNotFound},
		{"wrapped different code", wrapped, ErrCodeConflict, false, http.StatusNotFound},
		{"unrelated", errors.New("unrelated failure"), ErrCodeNotFound, false, http.StatusInternalServerError},
		{"nil", nil, ErrCodeNotFound, false, http.StatusInternalServerError},
		{"unknown code", unknown, "CUSTOM_ERROR", true, http.StatusInternalServerError},
		{"wrapped unknown code", fmt.Errorf("request: %w", unknown), "CUSTOM_ERROR", true, http.StatusInternalServerError},
		{"outer app error", outer, ErrCodeConflict, true, http.StatusConflict},
		{"inner app error is not selected", outer, ErrCodeNotFound, false, http.StatusConflict},
		{"wrapped outer app error", fmt.Errorf("request: %w", outer), ErrCodeConflict, true, http.StatusConflict},
		{"custom status", custom, ErrCodeNotFound, true, http.StatusGone},
		{"wrapped custom status", fmt.Errorf("request: %w", custom), ErrCodeNotFound, true, http.StatusGone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsErrorType(tt.err, tt.code); got != tt.wantType {
				t.Errorf("IsErrorType(%v, %q) = %v, want %v", tt.err, tt.code, got, tt.wantType)
			}
			if got := GetErrorStatus(tt.err); got != tt.wantStatus {
				t.Errorf("GetErrorStatus(%v) = %d, want %d", tt.err, got, tt.wantStatus)
			}
		})
	}
}
