package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestSameDialectEndpointWithSQLGlot(t *testing.T) {
	modulePath := os.Getenv("DF_SQLGLOT_TEST_DIR")
	if modulePath == "" {
		t.Skip("set DF_SQLGLOT_TEST_DIR to a local SQLGlot installation")
	}
	script, err := filepath.Abs("../../scripts/sql_translate.py")
	if err != nil {
		t.Fatal(err)
	}
	manager := sql_translator.NewRuntimeSQLTranslationManager(sql_translator.RuntimeOptions{Enabled: true, ScriptPath: script, PythonModulePath: modulePath})
	controller := NewSQLTranslationController(manager)
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", `{"sql":"SELECT 1","sourceDialect":"mysql","targetDialect":"mysql"}`, true},
		{"aliases", `{"sql":"SELECT 1","sourceDialect":" MySQL ","targetDialect":"PostgreSQL"}`, true},
		{"tsql alias", `{"sql":"SELECT 1","sourceDialect":"mysql","targetDialect":" TSQL "}`, true},
		{"malformed", `{"sql":"SELECT FROM","sourceDialect":"mysql","targetDialect":"mysql"}`, false},
		{"multiple", `{"sql":"SELECT 1; SELECT 2","sourceDialect":"mysql","targetDialect":"mysql"}`, false},
		{"unknown", `{"sql":"SELECT 1","sourceDialect":"unknown646","targetDialect":"unknown646"}`, false},
		{"blank", `{"sql":"SELECT 1","sourceDialect":" ","targetDialect":" "}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sql/translate", strings.NewReader(tc.body))
			controller.TranslateSQL(ctx)
			if (recorder.Code == http.StatusOK) != tc.valid {
				t.Fatalf("unexpected HTTP status %d", recorder.Code)
			}
		})
	}
}

func TestSameDialectTranslationEndpointHonorsInputLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := sql_translator.NewRuntimeSQLTranslationManager(sql_translator.RuntimeOptions{Enabled: true, MaxSQLBytes: 3})
	controller := NewSQLTranslationController(manager)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sql/translate", strings.NewReader(`{"sql":"SELECT 1","sourceDialect":"mysql","targetDialect":"mysql"}`))
	controller.TranslateSQL(ctx)
	if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), "SQL_INPUT_TOO_LARGE") {
		t.Fatalf("same-dialect request bypassed the limit: status %d, body %s", recorder.Code, recorder.Body.String())
	}
}
