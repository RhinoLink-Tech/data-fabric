package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	sdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	unifiedservice "github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
)

// Drive the SDK's JSON-RPC decoding rather than constructing a request map:
// a map populated by json.Unmarshal has already lost precision.
func TestExecuteSQLQueryPreservesRawJSONParameterNumbers(t *testing.T) {
	query := &stubQueryService{}
	srv := executeServer(query)
	message := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute_sql_query","arguments":{"datasource_id":"ds-1","sql":"SELECT ?","batch_size":2,"parameters":[9007199254740993,-9223372036854775808,9223372036854775807,1.25,1e3,true,null,"text"]}}}`
	result := srv.server.HandleMessage(context.Background(), json.RawMessage(message))
	if result == nil || query.captured == nil {
		t.Fatalf("JSON-RPC call did not reach the query service: %T", result)
	}
	want := []interface{}{json.Number("9007199254740993"), json.Number("-9223372036854775808"), json.Number("9223372036854775807"), json.Number("1.25"), json.Number("1e3"), true, nil, "text"}
	if !reflect.DeepEqual(query.captured.Parameters, want) {
		t.Fatalf("parameters = %#v, want %#v", query.captured.Parameters, want)
	}
	if query.captured.BatchSize != 2 {
		t.Fatalf("batch size = %d, want 2", query.captured.BatchSize)
	}
}

func TestHTTPExecuteSQLQueryPreservesLargeInteger(t *testing.T) {
	query := &stubQueryService{}
	srv := executeServer(query)
	jwt := security.NewJWTManager("0123456789abcdef0123456789abcdef", time.Hour)
	token, err := jwt.GenerateToken("alice", "team-a", "alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Transport: mcpHandlerTransport{srv.StreamableHTTPHandler(jwt)}}
	c, err := client.NewStreamableHttpClient(mcpTestURL, transport.WithHTTPBasicClient(httpClient),
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = c.Initialize(ctx, sdk.InitializeRequest{Params: sdk.InitializeParams{
		ProtocolVersion: sdk.LATEST_PROTOCOL_VERSION,
		ClientInfo:      sdk.Implementation{Name: "exact-integer-test", Version: "1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.CallTool(ctx, sdk.CallToolRequest{Params: sdk.CallToolParams{
		Name: "execute_sql_query",
		Arguments: map[string]any{"datasource_id": "ds-1", "sql": "SELECT ?",
			"parameters": []any{json.Number("9007199254740993")}},
	}})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("MCP call failed: %v", err)
	}
	if query.captured == nil || !reflect.DeepEqual(query.captured.Parameters, []interface{}{json.Number("9007199254740993")}) {
		t.Fatalf("HTTP transport lost integer precision: %#v", query.captured)
	}
}

func TestSafeMCPTransportLoggerOmitsSessionAndErrorDetails(t *testing.T) {
	var output bytes.Buffer
	sink := slog.NewTextHandler(&output, nil)
	logger := slog.New(safeMCPTransportHandler{sink: sink}).With("session", "private-session")
	logger.Info("session private-session")
	logger.Error("payload private-query", "error", "private-error")
	got := output.String()
	if !strings.Contains(got, "MCP transport error") {
		t.Fatalf("error signal missing from %q", got)
	}
	for _, secret := range []string{"private-session", "private-query", "private-error"} {
		if strings.Contains(got, secret) {
			t.Fatalf("log leaked %s", secret)
		}
	}
}

func TestRawExecuteParametersRejectsNonArrayWithoutEchoingValue(t *testing.T) {
	request := sdk.CallToolRequest{Params: sdk.CallToolParams{
		Name:         "execute_sql_query",
		RawArguments: json.RawMessage(`{"parameters":"private-value"}`),
	}}
	_, err := parseExecuteParameters(request)
	if err == nil || !strings.Contains(err.Error(), "parameters must be an array") {
		t.Fatalf("invalid parameters error = %v", err)
	}
	if strings.Contains(err.Error(), "private-value") {
		t.Fatal("error exposed parameter value")
	}

	request.Params.RawArguments = json.RawMessage(`{"parameters":null}`)
	params, err := parseExecuteParameters(request)
	if err != nil || params != nil {
		t.Fatalf("null parameters = %#v, %v", params, err)
	}
}

type exactParameterCapture struct {
	unifiedservice.UnifiedQueryService
	values chan exactParameterResult
}

type exactParameterResult struct {
	source string
	value  interface{}
}

func (s *exactParameterCapture) FetchQuery(_ context.Context, _ *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	var value interface{}
	if len(req.Parameters) == 1 {
		value = req.Parameters[0]
	}
	s.values <- exactParameterResult{source: req.DataSourceID, value: value}
	return &model.FetchQueryResponse{QueryID: "concurrent"}, nil
}

func TestConcurrentJSONRPCRequestsKeepTheirOwnExactParameters(t *testing.T) {
	const count = 32
	capture := &exactParameterCapture{values: make(chan exactParameterResult, count)}
	srv := NewMCPServer(capture, listingRepoStub{}, &stubTableDescriber{})
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			message := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute_sql_query","arguments":{"datasource_id":"ds-%d","sql":"SELECT ?","parameters":[%d]}}}`, i, int64(9007199254740992)+int64(i))
			srv.server.HandleMessage(context.Background(), json.RawMessage(message))
		}(i)
	}
	wg.Wait()
	close(capture.values)
	observed := 0
	for item := range capture.values {
		var index int
		if _, err := fmt.Sscanf(item.source, "ds-%d", &index); err != nil {
			t.Fatal(err)
		}
		want := json.Number(fmt.Sprint(int64(9007199254740992) + int64(index)))
		if item.value != want {
			t.Fatalf("%s parameter = %#v, want %q", item.source, item.value, want)
		}
		observed++
	}
	if observed != count {
		t.Fatalf("captured %d requests, want %d", observed, count)
	}
}
