//go:build datafabric_sqlite && cgo

package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	protocol "github.com/mark3labs/mcp-go/mcp"
)

// The helper executes the real entry point; os.Exit prevents Go's test runner
// from writing its own PASS line to the child's protocol stdout on shutdown.
func TestSQLiteStdioSubprocessHelper(t *testing.T) {
	if os.Getenv("DF_SQLITE_STDIO_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	main()
	os.Exit(0)
}

func TestSQLiteStdioProtocolDiagnostics(t *testing.T) {
	dir := t.TempDir()
	logFile, err := os.Create(filepath.Join(dir, "stderr.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSQLiteStdioSubprocessHelper$", "-test.count=1")
	cmd.Dir = dir // no config file: exercise the production fallback notice
	script, err := filepath.Abs("../../scripts/sql_translate.py")
	if err != nil {
		t.Fatal(err)
	}
	// SQL translation is not used here; startup still checks that its Python
	// dependency imports even when disabled. Isolate that check without a
	// network install or reliance on a workstation's Python environment.
	pythonModules := filepath.Join(dir, "python")
	if err = os.Mkdir(pythonModules, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(pythonModules, "sqlglot.py"), []byte("# unused startup import stub\n"), 0600); err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "DF_SQLITE_STDIO_HELPER=1",
		"DATABASE_DRIVER=sqlite", "DATABASE_SQLITE_PATH=" + filepath.Join(dir, "metadata.sqlite"),
		"SERVER_MODE=debug", "MCP_ENABLED=true", "MCP_TRANSPORT=stdio",
		"SQL_TRANSLATION_ENABLED=false", "LOGGING_LEVEL=info",
		"SQL_TRANSLATION_SCRIPT_PATH=" + script, "SQL_TRANSLATION_PYTHON_MODULE_PATH=" + pythonModules,
		"DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=fixture", "DATA_FABRIC_CREDENTIAL_KEYS={\"fixture\":\"" + key + "\"}",
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, childStdout, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	cmd.Stdout, cmd.Stderr = childStdout, logFile
	if err = cmd.Start(); err != nil {
		childStdout.Close()
		t.Fatal(err)
	}
	if err = childStdout.Close(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	waited := false
	defer func() {
		cancel()
		_ = stdin.Close()
		if !waited {
			<-wait
		}
	}()

	lines := make(chan string, 64)
	scanError := make(chan error, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				scanError <- ctx.Err()
				return
			}
		}
		scanError <- scanner.Err()
	}()
	send := func(message map[string]any) {
		t.Helper()
		b, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stdin.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	decode := func(line string) map[string]any {
		t.Helper()
		var message map[string]any
		if err := json.Unmarshal([]byte(line), &message); err != nil || message["jsonrpc"] != "2.0" {
			t.Fatalf("non-protocol stdout line %q: %v", line, err)
		}
		return message
	}
	response := func(id float64) map[string]any {
		t.Helper()
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					_ = logFile.Sync()
					logs, _ := os.ReadFile(logFile.Name())
					t.Fatalf("stdio closed before response %v; stderr: %s", id, logs)
				}
				message := decode(line)
				if message["id"] == id {
					return message
				}
			case <-ctx.Done():
				t.Fatalf("timeout waiting for response %v: %v", id, ctx.Err())
			}
		}
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": protocol.LATEST_PROTOCOL_VERSION, "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "stdio-acceptance", "version": "1"},
	}})
	if m := response(1); m["error"] != nil || m["result"] == nil {
		t.Fatalf("initialize: %v", m)
	}
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	// A missing ID triggers the actual GORM record-not-found diagnostic.
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "get_data_source_info", "arguments": map[string]any{"datasource_id": "synthetic-missing"},
	}})
	m := response(2)
	result, _ := m["result"].(map[string]any)
	if m["error"] != nil || result["isError"] != true {
		t.Fatalf("missing source: %v", m)
	}
	if err = logFile.Sync(); err != nil {
		t.Fatal(err)
	}
	logs, err := os.ReadFile(logFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logs), "Config file not found") || !strings.Contains(strings.ToLower(string(logs)), "record not found") {
		t.Fatalf("expected config and metadata diagnostics on stderr: %s", logs)
	}
	// A successful metadata call after the diagnostic must still decode.
	send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{
		"name": "list_data_sources", "arguments": map[string]any{},
	}})
	m = response(3)
	result, _ = m["result"].(map[string]any)
	if m["error"] != nil || result == nil || result["isError"] == true {
		t.Fatalf("subsequent metadata call: %v", m)
	}
	if err = stdin.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-wait:
		waited = true
		if err != nil {
			t.Fatalf("stdio process: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("stdio process did not exit: %v", ctx.Err())
	}
	for line := range lines {
		_ = decode(line)
	}
	if err = <-scanError; err != nil {
		t.Fatal(fmt.Errorf("read stdio: %w", err))
	}
}
