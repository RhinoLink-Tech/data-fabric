package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// stdioSmokeTimeout bounds each wait for a server response. The in-process
// transport answers in microseconds; two seconds only ever trips on a hang.
const stdioSmokeTimeout = 2 * time.Second

// The stdio smoke test drives the real JSON-RPC stdio transport in-process
// (piped reader/writer instead of os.Stdin/os.Stdout): initialize handshake,
// tools/list contract, one actual client query end-to-end, and the
// unknown-tool error shape — the client-visible behaviors #181 asks to pin.
//
// mcp-go keeps one static stdio session per process, so this test must not
// run in parallel with any other stdio server; the transport is only used
// here. The context cancel in t.Cleanup unblocks Listen even when the test
// fails before closing the input pipe, so no goroutines leak.
func TestStdioHandshakeToolsAndQueryRoundTrip(t *testing.T) {
	query := &stubQueryService{response: &model.FetchQueryResponse{
		QueryID: "query-stdio-1",
		Entries: []*[]interface{}{{"Ada Lovelace", 250.0}},
	}}
	repo := listingRepoStub{sources: []*model.DataSource{sourceWithSecrets()}}
	srv := NewMCPServer(query, repo, &stubTableDescriber{})

	stdio := mcpserver.NewStdioServer(srv.server)
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	listenDone := make(chan error, 1)
	go func() { listenDone <- stdio.Listen(ctx, stdinReader, stdoutWriter) }()

	// The server writes responses as they complete — tool calls arrive from a
	// worker pool, so lines may interleave; collect them and match by id.
	lines := make(chan string, 32)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdoutReader)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	send := func(payload map[string]any) {
		t.Helper()
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		if _, err := stdinWriter.Write(append(encoded, '\n')); err != nil {
			t.Fatalf("write request: %v", err)
		}
	}
	// waitForResponse matches one response by request id. Every line seen on
	// the way — including unrelated ones — must still be a valid JSON-RPC
	// message: the stdio channel belongs to the protocol, and any application
	// output on it corrupts client framing (#181 stdout tripwire).
	waitForResponse := func(id float64) map[string]any {
		t.Helper()
		deadline := time.After(stdioSmokeTimeout)
		for {
			select {
			case line, open := <-lines:
				if !open {
					t.Fatal("stdout closed before the response arrived")
				}
				var message map[string]any
				if err := json.Unmarshal([]byte(line), &message); err != nil {
					t.Fatalf("stdout line is not JSON (%q): %v", line, err)
				}
				if _, ok := message["jsonrpc"]; !ok {
					t.Fatalf("stdout line lacks the jsonrpc field: %s", line)
				}
				if lineID, ok := message["id"].(float64); ok && lineID == id {
					return message
				}
			case <-deadline:
				t.Fatalf("timed out waiting for response to request %v", id)
			}
		}
	}

	// 1. Initialize handshake: the client learns which server it reached.
	send(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "contract-test", "version": "0"},
		},
	})
	initialize := waitForResponse(1)
	if initialize["error"] != nil {
		t.Fatalf("initialize failed: %v", initialize["error"])
	}
	result, _ := initialize["result"].(map[string]any)
	serverInfo, _ := result["serverInfo"].(map[string]any)
	if name, _ := serverInfo["name"].(string); name != "Data Fabric MCP Server" {
		t.Fatalf("initialize serverInfo.name = %q, want \"Data Fabric MCP Server\"", name)
	}

	// 2. The initialized notification completes the handshake; no response.
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})

	// 3. tools/list matches the documented nine-tool contract.
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	toolList := waitForResponse(2)
	if toolList["error"] != nil {
		t.Fatalf("tools/list failed: %v", toolList["error"])
	}
	listResult, _ := toolList["result"].(map[string]any)
	tools, _ := listResult["tools"].([]any)
	names := make([]string, 0, len(tools))
	for _, entry := range tools {
		tool, _ := entry.(map[string]any)
		name, _ := tool["name"].(string)
		names = append(names, name)
	}
	slices.Sort(names)
	want := make([]string, 0, len(documentedToolContract))
	for name := range documentedToolContract {
		want = append(want, name)
	}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("tools/list = %v, want the documented contract %v", names, want)
	}

	// 4. One actual client query end-to-end through the transport: the
	// execute_sql_query result carries the service's row and never the
	// stored credentials.
	send(map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "execute_sql_query",
			"arguments": map[string]any{
				"datasource_id": "ds-1",
				"sql":           "SELECT customer, amount FROM orders",
			},
		},
	})
	call := waitForResponse(3)
	if call["error"] != nil {
		t.Fatalf("tools/call failed: %v", call["error"])
	}
	callResult, _ := call["result"].(map[string]any)
	if isError, _ := callResult["isError"].(bool); isError {
		t.Fatalf("execute_sql_query returned a tool error over stdio: %v", callResult)
	}
	content, _ := callResult["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content items = %d, want 1", len(content))
	}
	textContent, _ := content[0].(map[string]any)
	text, _ := textContent["text"].(string)
	if !strings.Contains(text, "Ada Lovelace") || !strings.Contains(text, `"queryId":"query-stdio-1"`) {
		t.Fatalf("query payload = %q, want the service row and query id", text)
	}
	if strings.Contains(text, secretMarker) {
		t.Fatalf("query payload leaks stored credentials: %s", text)
	}

	// 5. An unknown tool is a transport-level JSON-RPC error the client can
	// branch on, not a silent success.
	send(map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params":  map[string]any{"name": "no_such_tool", "arguments": map[string]any{}},
	})
	unknown := waitForResponse(4)
	rpcError, _ := unknown["error"].(map[string]any)
	if rpcError == nil {
		t.Fatalf("unknown tool produced %v, want a JSON-RPC error", unknown)
	}
	if message, _ := rpcError["message"].(string); !strings.Contains(message, "not found") {
		t.Fatalf("unknown tool error message = %q, want it to name the missing tool", message)
	}

	// Close the transport from the client side, as a real client does on exit.
	if err := stdinWriter.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	select {
	case err := <-listenDone:
		// EOF is the expected clean end of the input stream.
		if err != nil && err != io.EOF {
			t.Fatalf("Listen returned %v, want io.EOF", err)
		}
	case <-time.After(stdioSmokeTimeout):
		t.Fatal("Listen did not return after stdin closed")
	}
}
