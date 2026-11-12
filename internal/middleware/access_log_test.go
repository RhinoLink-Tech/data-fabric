package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHTTPLogsUseRouteTemplates(t *testing.T) {
	var access, recovery bytes.Buffer
	router := gin.New()
	router.Use(AccessLog(&access), Recovery(&recovery))
	router.GET("/api/v1/fetch/:query_id/:slug/:token", func(c *gin.Context) {
		if c.Query("panic") != "" {
			panic(c.Param("token"))
		}
		c.Status(http.StatusNoContent)
	})
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/api/v1/fetch/id/slug/fixture-secret?cursor=fixture-secret", http.StatusNoContent},
		{"/api/v1/fetch/id/slug/fixture-secret?panic=1", http.StatusInternalServerError},
		{"/missing/fixture-secret", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != tc.status {
			t.Fatalf("status = %d, want %d", response.Code, tc.status)
		}
	}
	if strings.Contains(access.String()+recovery.String(), "fixture-secret") {
		t.Fatal("HTTP logs contained a request credential")
	}
	if !strings.Contains(access.String(), "/unmatched") || !strings.Contains(recovery.String(), "/api/v1/fetch/:query_id/:slug/:token") {
		t.Fatal("HTTP logs did not retain the expected route diagnostics")
	}
}
