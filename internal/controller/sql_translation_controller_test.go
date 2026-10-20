package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
	"github.com/gin-gonic/gin"
)

func TestSQLTranslationEndpointsReturnServiceUnavailableWhenDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := sql_translator.NewRuntimeSQLTranslationManager(sql_translator.RuntimeOptions{Enabled: false})
	controller := NewSQLTranslationController(manager)

	tests := []struct {
		name    string
		method  string
		path    string
		handler gin.HandlerFunc
		body    string
	}{
		{"translate", http.MethodPost, "/api/v1/sql/translate", controller.TranslateSQL, "{\"sql\":\"SELECT 1\",\"sourceDialect\":\"mysql\",\"targetDialect\":\"postgres\"}"},
		{"dialects", http.MethodGet, "/api/v1/sql/dialects", controller.GetSupportedDialects, ""},
		{"validate", http.MethodPost, "/api/v1/sql/validate", controller.ValidateSQL, "{\"sql\":\"SELECT 1\",\"dialect\":\"postgres\"}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			test.handler(ctx)
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d; body: %s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), "SQL_TRANSLATION_DISABLED") {
				t.Fatalf("response %q does not identify disabled translation", recorder.Body.String())
			}
		})
	}
}
