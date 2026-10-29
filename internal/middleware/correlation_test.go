package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestCorrelationID(t *testing.T) {
	for _, tt := range []struct {
		name     string
		headerID string
	}{
		{name: "preserves caller ID", headerID: "Caller-Trace:batch/42+AbC"},
		{name: "generates missing ID"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			var ginID, requestID string
			router := gin.New()
			router.Use(CorrelationID())
			router.GET("/resource", func(c *gin.Context) {
				called = true
				ginID = c.GetString(CorrelationIDKey)
				requestID, _ = c.Request.Context().Value(CorrelationIDKey).(string)
				c.String(http.StatusOK, "next handler")
			})

			request := httptest.NewRequest(http.MethodGet, "/resource", nil)
			if tt.headerID != "" {
				request.Header.Set("X-Correlation-ID", tt.headerID)
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if !called {
				t.Fatal("next handler was not called")
			}
			if recorder.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", recorder.Code, http.StatusOK)
			}
			if got := recorder.Body.String(); got != "next handler" {
				t.Errorf("body = %q, want %q", got, "next handler")
			}

			responseID := recorder.Header().Get("X-Correlation-ID")
			if responseID == "" {
				t.Fatal("response correlation ID is empty")
			}
			if tt.headerID != "" {
				if responseID != tt.headerID {
					t.Errorf("response correlation ID = %q, want caller ID %q", responseID, tt.headerID)
				}
			} else {
				parsed, err := uuid.Parse(responseID)
				if err != nil {
					t.Errorf("generated correlation ID %q is not a valid UUID: %v", responseID, err)
				} else if parsed == uuid.Nil {
					t.Error("generated correlation ID is the nil UUID")
				}
			}
			if ginID != responseID {
				t.Errorf("Gin context correlation ID = %q, want %q", ginID, responseID)
			}
			if requestID != responseID {
				t.Errorf("request context correlation ID = %q, want %q", requestID, responseID)
			}
		})
	}
}
