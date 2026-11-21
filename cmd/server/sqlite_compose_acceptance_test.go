//go:build datafabric_sqlite && cgo

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	sdk "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	protocol "github.com/mark3labs/mcp-go/mcp"
)

func composePort(t *testing.T) string {
	t.Helper()
	_, port, err := net.SplitHostPort(freeSQLiteRuntimeAddress(t))
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func composeTokenSecret(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "JWT_SECRET=") {
			return strings.TrimPrefix(line, "JWT_SECRET=")
		}
	}
	t.Fatal("generated JWT secret missing")
	return ""
}

func sqliteComposeMCP(t *testing.T, endpoint, token, certPath string) *sdk.Client {
	t.Helper()
	pem, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("invalid test TLS certificate")
	}
	httpClient := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	client, err := sdk.NewStreamableHttpClient(endpoint, transport.WithHTTPBasicClient(httpClient), transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Initialize(ctx, protocol.InitializeRequest{Params: protocol.InitializeParams{ProtocolVersion: protocol.LATEST_PROTOCOL_VERSION, ClientInfo: protocol.Implementation{Name: "sqlite-compose-acceptance", Version: "1"}}}); err != nil {
		client.Close()
		t.Fatalf("TLS MCP initialize: %v", err)
	}
	return client
}

func TestSQLiteComposeFirstUse(t *testing.T) {
	if os.Getenv("DF_SQLITE_RUNTIME_ACCEPTANCE") != "github-ci" || os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("isolated GitHub CI fixture required")
	}
	image := os.Getenv("DF_SQLITE_RUNTIME_IMAGE")
	port := os.Getenv("DF_SQLITE_RUNTIME_PG_PORT")
	if image == "" || port == "" {
		t.Fatal("isolated image and PostgreSQL fixture required")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	template, err := os.ReadFile(filepath.Join(root, "deploy/sqlite/.env.example"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, ".env.example"), template, 0o600); err != nil {
		t.Fatal(err)
	}
	project := fmt.Sprintf("df-sqlite-ci-%d", time.Now().UnixNano())
	init := exec.Command("python3", filepath.Join(root, "scripts/sqlite/init.py"), "--directory", dir, "--project-name", project)
	if output, err := init.CombinedOutput(); err != nil {
		t.Fatalf("profile init: %s", output)
	}
	apiPort, mcpPort := composePort(t), composePort(t)
	envFile := filepath.Join(dir, ".env")
	composeFile := filepath.Join(root, "deploy/sqlite/compose.yml")
	env := append(os.Environ(), "DATA_FABRIC_SQLITE_IMAGE="+image, "DATA_FABRIC_SQLITE_TLS_DIR="+filepath.Join(dir, "tls"), "API_PORT="+apiPort, "MCP_HTTPS_PORT="+mcpPort)
	run := func(path string, args ...string) {
		t.Helper()
		base := []string{"compose", "--project-name", project, "--env-file", envFile, "--file", composeFile}
		cmd := exec.Command("docker", append(base, args...)...)
		cmd.Env = append(env, "DATA_FABRIC_SQLITE_PATH="+path)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compose %s failed: %s", args[0], output)
		}
	}
	metadataPath := "/var/lib/data-fabric/metadata.sqlite"
	t.Cleanup(func() {
		base := []string{"compose", "--project-name", project, "--env-file", envFile, "--file", composeFile, "down", "--volumes", "--remove-orphans"}
		cmd := exec.Command("docker", base...)
		cmd.Env = env
		_ = cmd.Run()
	})
	run(metadataPath, "up", "--detach", "--wait", "--wait-timeout", "120")
	manager := security.NewJWTManager(composeTokenSecret(t, envFile), time.Hour)
	alice, err := manager.GenerateToken("alice", "team-a", "alice", []string{"user"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := manager.GenerateToken("bob", "team-b", "bob", []string{"user"})
	if err != nil {
		t.Fatal(err)
	}
	api := "http://127.0.0.1:" + apiPort
	code, created := sqliteREST(t, "POST", api+"/api/v1/datasources", alice, map[string]any{
		"name": "compose-orders", "type": "postgresql", "config": map[string]any{"host": "host.docker.internal", "port": 5432, "database": "df_fixture", "username": "df_runtime_reader", "password": sqliteRuntimePassword, "ssl": false},
	})
	if code != http.StatusCreated {
		t.Fatalf("compose source creation status %d", code)
	}
	data, ok := created["data"].(map[string]any)
	if !ok {
		t.Fatal("source response missing")
	}
	sourceID, ok := data["id"].(string)
	if !ok || sourceID == "" {
		t.Fatal("source ID missing")
	}
	payload := map[string]any{"dataSourceId": sourceID, "sql": "SELECT id, status FROM public.df_runtime_orders ORDER BY id", "batch_size": 2}
	code, _ = sqliteREST(t, "POST", api+"/api/v1/fetch", "", payload)
	if code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated REST status %d", code)
	}
	code, first := sqliteREST(t, "POST", api+"/api/v1/fetch", alice, payload)
	if code != 200 {
		t.Fatalf("compose REST query status %d", code)
	}
	page, ok := first["data"].(map[string]any)
	if !ok {
		t.Fatal("REST page missing")
	}
	rows, ok := page["entries"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatal("REST page incomplete")
	}
	code, _ = sqliteREST(t, "POST", api+"/api/v1/fetch", bob, payload)
	if code != http.StatusForbidden && code != http.StatusNotFound {
		t.Fatalf("foreign tenant REST status %d", code)
	}
	mcpURL := "https://localhost:" + mcpPort + "/mcp"
	mcpClient := sqliteComposeMCP(t, mcpURL, alice, filepath.Join(dir, "tls/tls.crt"))
	result, failed := sqliteMCPCall(t, mcpClient, "execute_sql_query", map[string]any{"datasource_id": sourceID, "sql": "SELECT count(*) AS total FROM public.df_runtime_orders", "execution_mode": "pagination"})
	if failed {
		t.Fatal("TLS MCP query failed")
	}
	mcpRows, ok := result["entries"].([]any)
	if !ok || len(mcpRows) != 1 {
		t.Fatal("TLS MCP result missing")
	}
	row, ok := mcpRows[0].([]any)
	if !ok || len(row) != 1 || row[0] != float64(3) {
		t.Fatal("TLS MCP count incorrect")
	}
	mcpClient.Close()
	foreignMCP := sqliteComposeMCP(t, mcpURL, bob, filepath.Join(dir, "tls/tls.crt"))
	_, foreignDenied := sqliteMCPCall(t, foreignMCP, "execute_sql_query", map[string]any{"datasource_id": sourceID, "sql": "SELECT id FROM public.df_runtime_orders"})
	if !foreignDenied {
		t.Fatal("foreign tenant MCP query accepted")
	}
	foreignMCP.Close()
	snapshotPath := filepath.Join(t.TempDir(), "backup.sqlite")
	snapshot := func(wantSuccess bool, args ...string) {
		t.Helper()
		base := []string{filepath.Join(root, "scripts/sqlite/volume_snapshot.py"), "--project-name", project, "--env-file", envFile}
		cmd := exec.Command("python3", append(base, args...)...)
		cmd.Env = env
		output, err := cmd.CombinedOutput()
		if (err == nil) != wantSuccess {
			t.Fatalf("snapshot %v: error=%v output=%s", args, err, output)
		}
	}
	snapshot(true, "backup", snapshotPath)
	snapshot(false, "backup", snapshotPath)
	snapshot(false, "restore", snapshotPath, "--output-name", "restored.sqlite")
	run(metadataPath, "down")
	snapshot(true, "restore", snapshotPath, "--output-name", "restored.sqlite")
	snapshot(false, "restore", snapshotPath, "--output-name", "restored.sqlite")
	run("/var/lib/data-fabric/restored.sqlite", "up", "--detach", "--wait", "--wait-timeout", "120")
	code, after := sqliteREST(t, "GET", api+"/api/v1/datasources/"+sourceID, alice, nil)
	if code != 200 || after["data"] == nil {
		t.Fatal("source not restored in Compose profile")
	}
	mcpClient = sqliteComposeMCP(t, mcpURL, alice, filepath.Join(dir, "tls/tls.crt"))
	_, failed = sqliteMCPCall(t, mcpClient, "execute_sql_query", map[string]any{"datasource_id": sourceID, "sql": "SELECT count(*) FROM public.df_runtime_orders", "execution_mode": "pagination"})
	if failed {
		t.Fatal("MCP query failed after restore")
	}
	mcpClient.Close()
}
