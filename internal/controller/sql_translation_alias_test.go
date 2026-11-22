package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
	"github.com/gin-gonic/gin"
)

func TestTranslationDialectAliasesREST(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controller := NewSQLTranslationController(sql_translator.NewSQLTranslationManager(false))
	for _, tc := range []struct {
		source, target string
		wantStatus     int
	}{
		{" MySQL ", "PostgreSQL", http.StatusOK},
		{"mysql", "postgres", http.StatusOK},
		{" MySQL ", " TSQL ", http.StatusOK},
		{"mysql", "not_a_dialect", http.StatusBadRequest},
		{"mariadb", "mysql", http.StatusBadRequest},
		{" ", "mysql", http.StatusBadRequest},
	} {
		t.Run(tc.source+"_"+tc.target, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"sql": "SELECT 1", "sourceDialect": tc.source, "targetDialect": tc.target})
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sql/translate", strings.NewReader(string(body)))
			controller.TranslateSQL(ctx)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("got status %d, want %d", recorder.Code, tc.wantStatus)
			}
		})
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/sql/validate", strings.NewReader(`{"sql":"SELECT 1","dialect":" PostgreSQL "}`))
	controller.ValidateSQL(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("validation alias status = %d", recorder.Code)
	}
}
