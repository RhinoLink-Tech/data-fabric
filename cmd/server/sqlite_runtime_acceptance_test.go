//go:build datafabric_sqlite && cgo

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	sdk "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	protocol "github.com/mark3labs/mcp-go/mcp"
)

const sqliteRuntimePassword = "synthetic-reader-only"

func TestSQLiteRuntimeHelper(t *testing.T) {
	if os.Getenv("DF_SQLITE_RUNTIME_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	main()
}

type sqliteRuntimeProcess struct {
	cmd       *exec.Cmd
	log       *os.File
	container string
}

func startSQLiteRuntime(t *testing.T, dbPath, address, jwtSecret, keyJSON, parserPath string, mcpMode bool) *sqliteRuntimeProcess {
	t.Helper()
	script, err := filepath.Abs("../../scripts/sql_translate.py")
	if err != nil {
		t.Fatal(err)
	}
	logFile, err := os.CreateTemp(t.TempDir(), "server-*.log")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSQLiteRuntimeHelper$", "-test.count=1")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "DF_SQLITE_RUNTIME_HELPER=1",
		"DATABASE_DRIVER=sqlite", "DATABASE_SQLITE_PATH=" + dbPath, "SERVER_MODE=debug",
		"SERVER_HOST=127.0.0.1", "SECURITY_ENABLE_AUTH=true", "SECURITY_ENFORCE_READ_ONLY=true",
		"SECURITY_JWT_SECRET=" + jwtSecret, "DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=fixture",
		"DATA_FABRIC_CREDENTIAL_KEYS=" + keyJSON, "SQL_TRANSLATION_SCRIPT_PATH=" + script,
		"SQL_TRANSLATION_PYTHON_MODULE_PATH=" + parserPath, "MCP_ENABLED=" + strconv.FormatBool(mcpMode),
		"MCP_TRANSPORT=streamable-http", "MCP_AUTH_MODE=local-jwt",
	}
	if mcpMode {
		cmd.Env = append(cmd.Env, "MCP_ADDRESS="+address)
	} else {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			t.Fatal(err)
		}
		cmd.Env = append(cmd.Env, "SERVER_PORT="+port)
	}
	containerName := ""
	if image := os.Getenv("DF_SQLITE_RUNTIME_IMAGE"); image != "" {
		// CI-only: use the runner's unprivileged UID for a private bind mount.
		// Both containers share one SQLite directory on the same host.
		containerName = fmt.Sprintf("df-sqlite-ci-%d-%t", time.Now().UnixNano(), mcpMode)
		args := []string{"run", "--rm", "--name", containerName, "--network", "host",
			"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
			"--mount", "type=bind,source=" + filepath.Dir(dbPath) + ",target=" + filepath.Dir(dbPath)}
		for _, value := range cmd.Env {
			if strings.HasPrefix(value, "DF_SQLITE_RUNTIME_HELPER=") || strings.HasPrefix(value, "HOME=") || strings.HasPrefix(value, "PATH=") {
				continue
			}
			if strings.HasPrefix(value, "SQL_TRANSLATION_SCRIPT_PATH=") {
				value = "SQL_TRANSLATION_SCRIPT_PATH=/app/scripts/sql_translate.py"
			}
			if strings.HasPrefix(value, "SQL_TRANSLATION_PYTHON_MODULE_PATH=") {
				value = "SQL_TRANSLATION_PYTHON_MODULE_PATH=/opt/data-fabric/python"
			}
			args = append(args, "-e", value)
		}
		args = append(args, image)
		cmd = exec.Command("docker", args...)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &sqliteRuntimeProcess{cmd: cmd, log: logFile, container: containerName}
	t.Cleanup(func() { p.stop() })
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if conn, e := net.DialTimeout("tcp", address, 200*time.Millisecond); e == nil {
			conn.Close()
			return p
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = logFile.Sync()
	logs, _ := os.ReadFile(logFile.Name())
	safe := strings.NewReplacer(sqliteRuntimePassword, "[redacted]", jwtSecret, "[redacted]", keyJSON, "[redacted]").Replace(string(logs))
	t.Fatalf("runtime failed to start: %s", safe)
	return nil
}

func (p *sqliteRuntimeProcess) stop() {
	if p == nil || p.cmd == nil {
		return
	}
	if p.container != "" {
		_ = exec.Command("docker", "stop", "--time", "10", p.container).Run()
	} else {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
	}
	done := make(chan struct{})
	go func() { _ = p.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
	}
	_ = p.log.Close()
	p.cmd = nil
}

func freeSQLiteRuntimeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}

func sqliteREST(t *testing.T, method, url, token string, payload any) (int, map[string]any) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("REST request failed: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(sqliteRuntimePassword)) {
		t.Fatal("REST response leaked source credential")
	}
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("invalid REST response status %d", resp.StatusCode)
	}
	return resp.StatusCode, data
}

func sqliteMCPClient(t *testing.T, address, token string) *sdk.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := sdk.NewStreamableHttpClient("http://"+address+"/mcp", transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}))
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Initialize(ctx, protocol.InitializeRequest{Params: protocol.InitializeParams{ProtocolVersion: protocol.LATEST_PROTOCOL_VERSION, ClientInfo: protocol.Implementation{Name: "sqlite-runtime-acceptance", Version: "1"}}}); err != nil {
		client.Close()
		t.Fatalf("MCP initialize: %v", err)
	}
	return client
}

func sqliteMCPCall(t *testing.T, client *sdk.Client, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := client.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: name, Arguments: args}})
	if err != nil {
		t.Fatalf("MCP %s transport error: %v", name, err)
	}
	raw, _ := json.Marshal(result.Content)
	if bytes.Contains(raw, []byte(sqliteRuntimePassword)) {
		t.Fatal("MCP result leaked source credential")
	}
	if result.IsError {
		return nil, true
	}
	if len(result.Content) != 1 {
		t.Fatal("MCP result content missing")
	}
	text, ok := result.Content[0].(protocol.TextContent)
	if !ok {
		t.Fatal("MCP result is not text")
	}
	var output map[string]any
	if err = json.Unmarshal([]byte(text.Text), &output); err != nil {
		t.Fatalf("invalid MCP %s result", name)
	}
	return output, false
}

func sqliteRuntimeSnapshot(t *testing.T, source, destination string, restore bool) {
	t.Helper()
	image := os.Getenv("DF_SQLITE_RUNTIME_IMAGE")
	if image == "" {
		if err := config.SnapshotSQLiteMetadata(context.Background(), source, destination); err != nil {
			t.Fatalf("SQLite snapshot failed: %v", err)
		}
		return
	}
	action := "backup"
	if restore {
		action = "restore"
	}
	cmd := exec.Command("docker", "run", "--rm",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--mount", "type=bind,source="+filepath.Dir(source)+",target="+filepath.Dir(source),
		image, "sqlite-metadata", action, "--source", source, "--output", destination)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("image SQLite %s failed: %s", action, output)
	}
}

func setupSQLiteRuntimeSource(t *testing.T) int {
	t.Helper()
	// This fixture is provisioned by the dedicated CI job. Do not point it at
	// an existing local or business database.
	if os.Getenv("DF_SQLITE_RUNTIME_ACCEPTANCE") != "github-ci" || os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("isolated GitHub CI PostgreSQL fixture required")
	}
	port, err := strconv.Atoi(os.Getenv("DF_SQLITE_RUNTIME_PG_PORT"))
	if err != nil || port < 1 || port > 65535 {
		t.Fatal("fixture port missing")
	}
	dsn := fmt.Sprintf("host=127.0.0.1 port=%d user=df_fixture password=fixture-only dbname=df_fixture sslmode=disable", port)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	statements := []string{
		"CREATE TABLE public.df_runtime_orders (id INTEGER PRIMARY KEY, status TEXT NOT NULL)",
		"INSERT INTO public.df_runtime_orders VALUES (1,'new'),(2,'shipped'),(3,'new')",
		"CREATE ROLE df_runtime_reader LOGIN PASSWORD '" + sqliteRuntimePassword + "'",
		"GRANT CONNECT ON DATABASE df_fixture TO df_runtime_reader",
		"GRANT USAGE ON SCHEMA public TO df_runtime_reader",
		"GRANT SELECT ON public.df_runtime_orders TO df_runtime_reader",
	}
	for _, statement := range statements {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("isolated source setup: %v", err)
		}
	}
	return port
}

func TestSQLiteRuntimeRESTMCPRestartAndRestore(t *testing.T) {
	port := setupSQLiteRuntimeSource(t)
	parserPath := os.Getenv("DF_SQLITE_RUNTIME_SQLGLOT")
	if parserPath == "" {
		t.Fatal("isolated SQLGlot dependency path required")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "metadata.sqlite")
	snapshot := filepath.Join(dir, "backup.sqlite")
	restored := filepath.Join(dir, "restored.sqlite")
	jwtSecret := "sqlite-runtime-synthetic-secret-0123456789abcdef"
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(map[string]string{"fixture": base64.StdEncoding.EncodeToString(key)})
	keyJSON := string(encoded)
	manager := security.NewJWTManager(jwtSecret, time.Hour)
	alice, err := manager.GenerateToken("alice", "team-a", "alice", []string{"user"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := manager.GenerateToken("bob", "team-b", "bob", []string{"user"})
	if err != nil {
		t.Fatal(err)
	}
	restAddress, mcpAddress := freeSQLiteRuntimeAddress(t), freeSQLiteRuntimeAddress(t)
	rest := startSQLiteRuntime(t, dbPath, restAddress, jwtSecret, keyJSON, parserPath, false)
	code, created := sqliteREST(t, "POST", "http://"+restAddress+"/api/v1/datasources", alice, map[string]any{
		"name": "synthetic-orders", "type": "postgresql", "config": map[string]any{"host": "127.0.0.1", "port": port, "database": "df_fixture", "username": "df_runtime_reader", "password": sqliteRuntimePassword, "ssl": false},
	})
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create source status=%d response=%v", code, created)
	}
	data, ok := created["data"].(map[string]any)
	if !ok {
		t.Fatal("source response missing")
	}
	sourceID, ok := data["id"].(string)
	if !ok || sourceID == "" {
		t.Fatal("source ID missing")
	}
	mcpProcess := startSQLiteRuntime(t, dbPath, mcpAddress, jwtSecret, keyJSON, parserPath, true)
	aliceMCP := sqliteMCPClient(t, mcpAddress, alice)
	list, failed := sqliteMCPCall(t, aliceMCP, "list_data_sources", nil)
	if failed {
		t.Fatal("MCP source discovery failed")
	}
	listed, _ := json.Marshal(list)
	if !bytes.Contains(listed, []byte(sourceID)) {
		t.Fatal("MCP did not see REST-created source")
	}
	query := map[string]any{"dataSourceId": sourceID, "sql": "SELECT id, status FROM public.df_runtime_orders ORDER BY id", "batch_size": 2}
	code, first := sqliteREST(t, "POST", "http://"+restAddress+"/api/v1/fetch", alice, query)
	if code != 200 {
		t.Fatalf("REST query status=%d response=%v", code, first)
	}
	page, ok := first["data"].(map[string]any)
	if !ok {
		t.Fatal("first REST page missing")
	}
	rows, ok := page["entries"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatal("first REST page incomplete")
	}
	next, ok := page["nextUri"].(string)
	if !ok || !strings.HasPrefix(next, "/api/v1/fetch/") {
		t.Fatal("REST continuation missing")
	}
	code, second := sqliteREST(t, "GET", "http://"+restAddress+next, alice, nil)
	if code != 200 {
		t.Fatalf("REST continuation status=%d", code)
	}
	secondPage := second["data"].(map[string]any)
	last, ok := secondPage["entries"].([]any)
	if !ok || len(last) != 1 {
		t.Fatal("REST final page incomplete")
	}
	foreignCode, _ := sqliteREST(t, "POST", "http://"+restAddress+"/api/v1/fetch", bob, query)
	if foreignCode != http.StatusForbidden && foreignCode != http.StatusNotFound {
		t.Fatalf("foreign tenant query status %d", foreignCode)
	}
	writeCode, _ := sqliteREST(t, "POST", "http://"+restAddress+"/api/v1/fetch", alice, map[string]any{"dataSourceId": sourceID, "sql": "DELETE FROM public.df_runtime_orders"})
	if writeCode < 400 {
		t.Fatalf("write query accepted: %d", writeCode)
	}
	mcpResult, failed := sqliteMCPCall(t, aliceMCP, "execute_sql_query", map[string]any{"datasource_id": sourceID, "sql": "SELECT id, status FROM public.df_runtime_orders ORDER BY id", "batch_size": 2, "execution_mode": "pagination"})
	if failed {
		t.Fatal("MCP query failed")
	}
	mcpRows, ok := mcpResult["entries"].([]any)
	if !ok || len(mcpRows) != 2 {
		t.Fatal("MCP first page incomplete")
	}
	bobMCP := sqliteMCPClient(t, mcpAddress, bob)
	_, failed = sqliteMCPCall(t, bobMCP, "execute_sql_query", map[string]any{"datasource_id": sourceID, "sql": "SELECT id FROM public.df_runtime_orders"})
	if !failed {
		t.Fatal("foreign tenant MCP query accepted")
	}
	bobMCP.Close()
	aliceMCP.Close()
	sqliteRuntimeSnapshot(t, dbPath, snapshot, false)
	mcpProcess.stop()
	rest.stop()
	sqliteRuntimeSnapshot(t, snapshot, restored, true)
	rest = startSQLiteRuntime(t, restored, restAddress, jwtSecret, keyJSON, parserPath, false)
	mcpProcess = startSQLiteRuntime(t, restored, mcpAddress, jwtSecret, keyJSON, parserPath, true)
	code, got := sqliteREST(t, "GET", "http://"+restAddress+"/api/v1/datasources/"+sourceID, alice, nil)
	if code != 200 || got["data"] == nil {
		t.Fatalf("restored source unavailable: %d", code)
	}
	aliceMCP = sqliteMCPClient(t, mcpAddress, alice)
	after, failed := sqliteMCPCall(t, aliceMCP, "execute_sql_query", map[string]any{"datasource_id": sourceID, "sql": "SELECT count(*) AS total FROM public.df_runtime_orders", "execution_mode": "pagination"})
	if failed {
		t.Fatal("restored MCP query failed")
	}
	afterRows, ok := after["entries"].([]any)
	if !ok || len(afterRows) != 1 {
		t.Fatal("restored query rows missing")
	}
	row, ok := afterRows[0].([]any)
	if !ok || len(row) != 1 || row[0] != float64(3) {
		t.Fatalf("unexpected restored count: %v", afterRows)
	}
	aliceMCP.Close()
	mcpProcess.stop()
	rest.stop()
	metadata, err := config.InitDatabase(&config.Config{Database: config.DatabaseConfig{Driver: "sqlite", SQLitePath: restored}})
	if err != nil {
		t.Fatal(err)
	}
	rawDB, err := metadata.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	var stored string
	if err = metadata.Raw("SELECT config FROM data_sources WHERE id = ?", sourceID).Scan(&stored).Error; err != nil || strings.Contains(stored, sqliteRuntimePassword) || !strings.Contains(stored, "dfcred:v1:") {
		t.Fatal("restored credential encryption invalid")
	}
	var events int64
	if err = metadata.Table("audit_events").Where("tenant_id = ?", "team-a").Count(&events).Error; err != nil || events == 0 {
		t.Fatal("restored audit records missing")
	}
}
