package sql_translator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagerRejectsBlankTranslationInputs(t *testing.T) {
	manager := NewSQLTranslationManager(false)
	for _, args := range [][3]string{
		{" \n\t", "mysql", "postgres"},
		{"SELECT 1", " ", "postgres"},
		{"SELECT 1", "mysql", "\t"},
		{"SELECT 1", " ", " "},
	} {
		if got, err := manager.TranslateQuery(args[0], args[1], args[2]); err == nil || got != "" {
			t.Fatalf("TranslateQuery(%q) = %q, %v; want rejection", args, got, err)
		}
	}
}

func TestManagerSameDialectHonorsTimeoutAndOutputLimit(t *testing.T) {
	tests := []struct {
		name, script string
		timeout      time.Duration
		maxOutput    int64
		want         error
	}{
		{"timeout", "exec sleep 10", 50 * time.Millisecond, 128, context.DeadlineExceeded},
		{"output", "head -c 4096 /dev/zero", time.Second, 32, ErrSQLTranslationOutputLimit},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			manager := &SQLTranslationManager{translator: newShellTranslator(t, tc.script, tc.timeout, 1, 1024, tc.maxOutput), enabled: true}
			if _, err := manager.TranslateQuery("SELECT 1", "mysql", "mysql"); !errors.Is(err, tc.want) {
				t.Fatalf("same-dialect error = %v, want %v", err, tc.want)
			}
		})
	}
}

// Set DF_SQLGLOT_TEST_DIR to an existing installation to exercise the real
// parser without downloading dependencies as part of the Go unit suite.
func TestManagerSameDialectWithSQLGlot(t *testing.T) {
	modulePath := os.Getenv("DF_SQLGLOT_TEST_DIR")
	if modulePath == "" {
		t.Skip("set DF_SQLGLOT_TEST_DIR to a local SQLGlot installation")
	}
	script, err := filepath.Abs("../../../scripts/sql_translate.py")
	if err != nil {
		t.Fatal(err)
	}
	manager := NewRuntimeSQLTranslationManager(RuntimeOptions{Enabled: true, ScriptPath: script, PythonModulePath: modulePath})
	if err := manager.ValidateRuntime(); err != nil {
		t.Fatal(err)
	}
	got, err := manager.TranslateQuery("SELECT 1;", " MySQL ", "mysql")
	if err != nil || !strings.Contains(got, "SELECT") {
		t.Fatalf("valid same-dialect SQL = %q, %v", got, err)
	}
	for _, tc := range [][3]string{
		{"SELECT FROM", "mysql", "mysql"},
		{"SELECT 1; SELECT 2", "mysql", "mysql"},
		{"SELECT 1", "unknown646", "unknown646"},
		{"SELECT 1", " ", " "},
	} {
		if result, err := manager.TranslateQuery(tc[0], tc[1], tc[2]); err == nil || result != "" {
			t.Fatalf("invalid same-dialect SQL was accepted: %q, %v", result, err)
		}
	}
}

func TestManagerDoesNotBypassSameDialectValidation(t *testing.T) {
	translator := newShellTranslator(t, `printf '{"error":"invalid SQL statement"}'`, time.Second, 1, 1024, 128)
	manager := &SQLTranslationManager{translator: translator, enabled: true}
	got, err := manager.TranslateQuery("SELECT FROM", "mysql", "mysql")
	if got != "" || !IsTranslationError(err) {
		t.Fatalf("same-dialect translation = %q, %v; want parser rejection", got, err)
	}
}

func TestManagerSameDialectHonorsInputLimit(t *testing.T) {
	translator := newShellTranslator(t, `printf '{"translated_sql":"SELECT 1"}'`, time.Second, 1, 3, 128)
	manager := &SQLTranslationManager{translator: translator, enabled: true}
	if _, err := manager.TranslateQuery("SELECT 1", "mysql", "mysql"); err == nil || !strings.Contains(err.Error(), "input exceeds") {
		t.Fatalf("same-dialect translation error = %v, want input limit", err)
	}
}

func TestManagerSameDialectHonorsCancellation(t *testing.T) {
	translator := newShellTranslator(t, "exec sleep 10", time.Second, 1, 1024, 128)
	manager := &SQLTranslationManager{translator: translator, enabled: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.TranslateQueryContext(ctx, "SELECT 1", "mysql", "mysql"); !errors.Is(err, context.Canceled) {
		t.Fatalf("same-dialect translation error = %v, want cancellation", err)
	}
}
