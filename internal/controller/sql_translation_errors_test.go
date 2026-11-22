package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
	"github.com/gin-gonic/gin"
)

func translationResponse(t *testing.T, manager *sql_translator.SQLTranslationManager, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sql/translate", strings.NewReader(body))
	NewSQLTranslationController(manager).TranslateSQL(ctx)
	return recorder
}

func TestTranslationRuntimeFailuresRemainServiceErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell subprocess fixtures run on Linux")
	}
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, shell string
		timeout     time.Duration
		status      int
		code        string
	}{
		{"runtime", `printf '{"error":"private-driver-marker","error_kind":"runtime_failure"}'`, time.Second, http.StatusInternalServerError, "TRANSLATION_ERROR"},
		{"timeout", `exec sleep 3`, 50 * time.Millisecond, http.StatusGatewayTimeout, "SQL_TRANSLATION_TIMEOUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "parser.sh")
			if err := os.WriteFile(path, []byte(tc.shell+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			manager := sql_translator.NewRuntimeSQLTranslationManager(sql_translator.RuntimeOptions{Enabled: true, PythonPath: "/bin/sh", ScriptPath: path, Timeout: tc.timeout})
			response := translationResponse(t, manager, `{"sql":"SELECT 1","sourceDialect":"mysql","targetDialect":"postgres"}`)
			if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.code) || strings.Contains(response.Body.String(), "private-driver-marker") {
				t.Fatalf("unexpected runtime response: status %d, body %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestTranslationInvalidInputRESTWithSQLGlot(t *testing.T) {
	modulePath := os.Getenv("DF_SQLGLOT_TEST_DIR")
	if modulePath == "" {
		t.Skip("set DF_SQLGLOT_TEST_DIR to a local SQLGlot installation")
	}
	gin.SetMode(gin.TestMode)
	script, err := filepath.Abs("../../scripts/sql_translate.py")
	if err != nil {
		t.Fatal(err)
	}
	manager := sql_translator.NewRuntimeSQLTranslationManager(sql_translator.RuntimeOptions{Enabled: true, ScriptPath: script, PythonModulePath: modulePath})
	for _, body := range []string{
		`{"sql":"SELECT 'sql-secret-marker' FROM","sourceDialect":"mysql","targetDialect":"mysql"}`,
		`{"sql":"SELECT 1; SELECT 2","sourceDialect":"mysql","targetDialect":"postgres"}`,
	} {
		response := translationResponse(t, manager, body)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "INVALID_SQL_TRANSLATION") || strings.Contains(response.Body.String(), "sql-secret-marker") {
			t.Fatalf("unexpected invalid-input response: status %d, body %s", response.Code, response.Body.String())
		}
	}
}
