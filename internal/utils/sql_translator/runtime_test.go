package sql_translator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newShellTranslator(t *testing.T, script string, timeout time.Duration, maxConcurrent int, maxSQLBytes, maxOutputBytes int64) *ExternalSQLTranslator {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-based subprocess behavior is tested on Linux")
	}
	path := filepath.Join(t.TempDir(), "translator.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return NewExternalSQLTranslatorWithOptions(RuntimeOptions{
		Enabled: true, PythonPath: "/bin/sh", ScriptPath: path,
		Timeout: timeout, MaxConcurrent: maxConcurrent,
		MaxSQLBytes: maxSQLBytes, MaxOutputBytes: maxOutputBytes,
	})
}

func TestTranslateContextHonorsSQLInputLimit(t *testing.T) {
	translator := newShellTranslator(t, "printf '{\"translated_sql\":\"SELECT 1\"}'", time.Second, 1, 3, 128)
	if _, err := translator.TranslateContext(context.Background(), "SELECT 1", "mysql", "postgres"); err == nil || !strings.Contains(err.Error(), "input exceeds") {
		t.Fatalf("TranslateContext error = %v, want input size error", err)
	}
}

func TestAnalyzeContextHonorsSQLInputLimit(t *testing.T) {
	translator := newShellTranslator(t, "printf '{}'", time.Second, 1, 3, 128)
	if _, err := translator.AnalyzeContext(context.Background(), "SELECT 1", "postgres"); err == nil || !strings.Contains(err.Error(), "input exceeds") {
		t.Fatalf("AnalyzeContext error = %v, want input size error", err)
	}
}

func TestValidateContextUsesASTAnalysis(t *testing.T) {
	response := `{"statement_count":1,"is_query":true,"has_mutation":false,"has_select_into":false,"has_locking":false,"functions":[]}`
	translator := newShellTranslator(t, "printf '%s' '"+response+"'", time.Second, 1, 1024, 512)
	valid, err := translator.ValidateContext(context.Background(), "SELECT 1", "postgres")
	if err != nil || !valid {
		t.Fatalf("ValidateContext() = %v, %v; want true, nil", valid, err)
	}
}

func TestDisabledTranslationManagerKeepsASTAnalysisAvailable(t *testing.T) {
	response := `{"statement_count":1,"is_query":true,"functions":[]}`
	translator := newShellTranslator(t, "printf '%s' '"+response+"'", time.Second, 1, 1024, 512)
	manager := &SQLTranslationManager{translator: translator, enabled: false}
	if !manager.SupportsASTAnalysis() {
		t.Fatal("SupportsASTAnalysis() = false, want true when the SQLGlot runtime is configured")
	}
	analysis, err := manager.AnalyzeSQLContext(context.Background(), "SELECT 1", "postgres")
	if err != nil || analysis.StatementCount != 1 || !analysis.IsQuery {
		t.Fatalf("AnalyzeSQLContext() = %+v, %v; want one query statement", analysis, err)
	}
}

func TestTranslateContextKillsProcessOnTimeout(t *testing.T) {
	translator := newShellTranslator(t, "exec sleep 10", 50*time.Millisecond, 1, 1024, 128)
	started := time.Now()
	_, err := translator.TranslateContext(context.Background(), "SELECT 1", "mysql", "postgres")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("TranslateContext error = %v, want deadline exceeded", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("timed out subprocess was not terminated promptly")
	}
}

func TestTranslateContextCapsSubprocessOutput(t *testing.T) {
	translator := newShellTranslator(t, "head -c 4096 /dev/zero", time.Second, 1, 1024, 32)
	_, err := translator.TranslateContext(context.Background(), "SELECT 1", "mysql", "postgres")
	if !errors.Is(err, ErrSQLTranslationOutputLimit) {
		t.Fatalf("TranslateContext error = %v, want output limit error", err)
	}
}

func TestTranslationSemaphoreHonorsRequestCancellation(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	script := "printf started > " + marker + "\nexec sleep 10"
	translator := newShellTranslator(t, script, 5*time.Second, 1, 1024, 128)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	firstDone := make(chan error, 1)
	go func() {
		_, err := translator.TranslateContext(firstCtx, "SELECT 1", "mysql", "postgres")
		firstDone <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first translator process did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}

	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancelSecond()
	if _, err := translator.TranslateContext(secondCtx, "SELECT 1", "mysql", "postgres"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second TranslateContext error = %v, want deadline while waiting for concurrency slot", err)
	}
	cancelFirst()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first translator process did not stop after cancellation")
	}
}

func TestDisabledManagerRejectsTranslationAndHidesDialects(t *testing.T) {
	manager := NewRuntimeSQLTranslationManager(RuntimeOptions{Enabled: false})
	if _, err := manager.TranslateQueryContext(context.Background(), "SELECT 1", "mysql", "postgres"); !errors.Is(err, ErrSQLTranslationDisabled) {
		t.Fatalf("TranslateQueryContext error = %v, want disabled error", err)
	}
	if dialects := manager.GetSupportedDialects(); len(dialects) != 0 {
		t.Fatalf("GetSupportedDialects() = %v, want none when disabled", dialects)
	}
}
