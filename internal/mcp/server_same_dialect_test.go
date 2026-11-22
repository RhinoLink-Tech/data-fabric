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

func TestSameDialectTranslationToolHonorsInputLimit(t *testing.T) {
	manager := sql_translator.NewRuntimeSQLTranslationManager(sql_translator.RuntimeOptions{Enabled: true, MaxSQLBytes: 3})
	srv := NewMCPServerWithTableAuthorizer(nil, nil, &stubTableDescriber{}, nil, manager)
	request := callToolRequest("translate_sql", map[string]any{"sql": "SELECT 1", "source_dialect": "mysql", "target_dialect": "mysql"})
	if result, err := srv.handleTranslateSQL(context.Background(), request); err == nil || !strings.Contains(err.Error(), "input exceeds") || result != nil {
		t.Fatalf("same-dialect request bypassed the limit: result %v, error %v", result, err)
	}
}

func TestSameDialectToolWithSQLGlot(t *testing.T) {
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
	for _, tc := range []struct {
		sql, dialect string
		valid        bool
	}{
		{"SELECT 1", "mysql", true}, {"SELECT FROM", "mysql", false},
		{"SELECT 1; SELECT 2", "mysql", false}, {"SELECT 1", "unknown646", false},
		{"SELECT 1", " ", false},
	} {
		request := callToolRequest("translate_sql", map[string]any{"sql": tc.sql, "source_dialect": tc.dialect, "target_dialect": tc.dialect})
		result, err := srv.handleTranslateSQL(context.Background(), request)
		if (err == nil && result != nil) != tc.valid {
			t.Fatalf("unexpected translation outcome for %q: %v", tc.sql, err)
		}
	}
}

func TestSameDialectTranslationToolRejectsDisabledManager(t *testing.T) {
	manager := sql_translator.NewRuntimeSQLTranslationManager(sql_translator.RuntimeOptions{Enabled: false})
	srv := NewMCPServerWithTableAuthorizer(nil, nil, &stubTableDescriber{}, nil, manager)
	request := callToolRequest("translate_sql", map[string]any{"sql": "SELECT 1", "source_dialect": "mysql", "target_dialect": "mysql"})
	if result, err := srv.handleTranslateSQL(context.Background(), request); !errors.Is(err, sql_translator.ErrSQLTranslationDisabled) || result != nil {
		t.Fatalf("disabled translation accepted: result %v, error %v", result, err)
	}
}
