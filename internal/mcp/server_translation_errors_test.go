package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
)

func TestTranslationInvalidInputMCPWithSQLGlot(t *testing.T) {
	modulePath := os.Getenv("DF_SQLGLOT_TEST_DIR")
	if modulePath == "" {
		t.Skip("set DF_SQLGLOT_TEST_DIR to a local SQLGlot installation")
	}
	script, err := filepath.Abs("../../scripts/sql_translate.py")
	if err != nil {
		t.Fatal(err)
	}
	manager := sql_translator.NewRuntimeSQLTranslationManager(sql_translator.RuntimeOptions{Enabled: true, ScriptPath: script, PythonModulePath: modulePath})
	srv := NewMCPServerWithTableAuthorizer(nil, nil, &stubTableDescriber{}, nil, manager)
	request := callToolRequest("translate_sql", map[string]any{"sql": "SELECT 'private-sql-marker' FROM", "source_dialect": "mysql", "target_dialect": "postgres"})
	result, err := srv.handleTranslateSQL(context.Background(), request)
	if result != nil || !errors.Is(err, sql_translator.ErrSQLTranslationInvalidInput) || strings.Contains(err.Error(), "private-sql-marker") {
		t.Fatalf("unexpected MCP error: %v", err)
	}
}
