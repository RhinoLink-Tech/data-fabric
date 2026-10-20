package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tt := range []struct {
		name       string
		method     string
		wantStatus int
		wantNext   bool
		wantBody   string
	}{
		{name: "preflight", method: http.MethodOptions, wantStatus: http.StatusNoContent},
		{name: "normal_request", method: http.MethodGet, wantStatus: http.StatusOK, wantNext: true, wantBody: "next handler"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			router := gin.New()
			router.Use(Cors())
			router.Any("/resource", func(c *gin.Context) {
				called = true
				c.String(http.StatusOK, "next handler")
			})

			request := httptest.NewRequest(tt.method, "/resource", nil)
			request.Header.Set("Origin", "https://client.example")
			if tt.method == http.MethodOptions {
				request.Header.Set("Access-Control-Request-Method", http.MethodGet)
				request.Header.Set("Access-Control-Request-Headers", "Authorization")
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if called != tt.wantNext {
				t.Errorf("next handler called = %t, want %t", called, tt.wantNext)
			}
			if got := recorder.Body.String(); got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
			for header, want := range map[string]string{
				"Access-Control-Allow-Origin":  "*",
				"Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
				"Access-Control-Allow-Headers": "Origin, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization",
			} {
				if got := recorder.Header().Get(header); got != want {
					t.Errorf("%s = %q, want %q", header, got, want)
				}
			}
		})
	}
}
