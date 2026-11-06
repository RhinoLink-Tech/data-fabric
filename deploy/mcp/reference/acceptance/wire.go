package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

const maxResponse = 2 << 20

type surface string

const (
	rest    surface = "rest"
	mcpHTTP surface = "mcp-http"
)

type wire struct {
	kind            surface
	env             *reference.Environment
	http            *http.Client
	mcpSessions     map[string]*mcpclient.Client
	mcpCorrelations map[string]string
	mcpCaptures     map[string]*sessionCapture
}

func (w wire) withMCPState() wire {
	if w.kind == mcpHTTP {
		w.mcpSessions = make(map[string]*mcpclient.Client)
		w.mcpCorrelations = make(map[string]string)
		w.mcpCaptures = make(map[string]*sessionCapture)
	}
	return w
}

func (w wire) releaseMCPActor(ctx context.Context, actor string) error {
	var failures []error
	if capture := w.mcpCaptures[actor]; capture != nil {
		if session := capture.sessionID(); session != "" {
			identity := w.env.Identities[actor]
			status, _, _, err := sendHTTP(ctx, w.http, http.MethodDelete, w.env.MCPURL,
				identity.MCPToken, session, "", nil)
			if err != nil {
				failures = append(failures, fmt.Errorf("delete MCP acceptance session: %T", err))
			} else if status != http.StatusOK && status != http.StatusNotFound {
				failures = append(failures, fmt.Errorf("MCP acceptance session DELETE returned HTTP %d", status))
			}
		}
	}
	if client := w.mcpSessions[actor]; client != nil {
		_ = client.Close()
	}
	delete(w.mcpSessions, actor)
	delete(w.mcpCorrelations, actor)
	delete(w.mcpCaptures, actor)
	return errors.Join(failures...)
}

func (w wire) releaseMCPSessions(ctx context.Context) error {
	var failures []error
	for actor := range w.mcpSessions {
		if err := w.releaseMCPActor(ctx, actor); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (w wire) closeMCPSessions() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = w.releaseMCPSessions(ctx)
}

type column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

type batch struct {
	QueryID    string   `json:"queryId"`
	Slug       string   `json:"slug"`
	Token      string   `json:"token"`
	NextURI    string   `json:"nextUri"`
	Columns    []column `json:"columns"`
	Entries    [][]any  `json:"entries"`
	TotalCount int      `json:"totalCount"`
}

type reply struct {
	data    json.RawMessage
	status  int
	code    string
	message string
	details json.RawMessage
	failed  bool
}

func (r reply) decode(dst any) error {
	if r.failed {
		return fmt.Errorf("operation failed (%s)", r.failureKind())
	}
	d := json.NewDecoder(bytes.NewReader(r.data))
	d.UseNumber()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("operation returned invalid data shape")
	}
	return nil
}

func (r reply) failureKind() string {
	if !r.failed {
		return "successful response"
	}
	// The raw code or text is untrusted and can contain request details.
	if r.status != 0 {
		switch r.code {
		case "TABLE_ACCESS_DENIED", "DATASOURCE_ACCESS_DENIED", "DATASOURCE_NOT_FOUND",
			"QUERY_SESSION_UNAVAILABLE", "QUERY_ENGINE_ERROR", "QUERY_TIMEOUT",
			"QUERY_CANCELLED", "INVALID_QUERY", "COUNT_UNSUPPORTED",
			"QUERY_ADMISSION_LIMIT", "QUERY_RATE_LIMIT_EXCEEDED", "DATASOURCE_INACTIVE",
			"SQL_INJECTION_DETECTED", "SQL_SYNTAX_ERROR":
			return fmt.Sprintf("HTTP %d %s", r.status, r.code)
		}
		return fmt.Sprintf("HTTP %d", r.status)
	}
	switch r.message {
	case sessionError, sourceDenied, tableDenied, readonlyError, invalidQuery, capacityError,
		unsafeQuery, sourceFailure, queryTimeout, queryCancelled, sourceHidden, tableHidden:
		return r.message
	}
	return "MCP tool error"
}

func (r reply) expectError(kind surface, status int, code, message string) error {
	messageMatches := r.message == message
	if kind == rest && status == http.StatusNotFound && code == "QUERY_SESSION_UNAVAILABLE" &&
		r.message == "query session is unavailable" {
		// The ingress rejects an expired signed route before contacting its owner.
		messageMatches = true
	}
	if !r.failed || (kind == rest && (r.status != status || r.code != code)) || !messageMatches {
		return fmt.Errorf("expected %s; observed %s", code, r.failureKind())
	}
	if (len(r.data) != 0 && string(r.data) != "null") ||
		(len(r.details) != 0 && string(r.details) != "null" && string(r.details) != "\"\"") {
		return fmt.Errorf("failure exposes data or internal details")
	}
	return nil
}

func newHTTP(env *reference.Environment) *http.Client {
	return &http.Client{
		Transport:     &http.Transport{TLSClientConfig: env.TLSConfig.Clone()},
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (w wire) request(ctx context.Context, method, endpoint, token, correlation string, body any) (*http.Response, error) {
	var content io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode wire request")
		}
		content = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, content)
	if err != nil {
		return nil, fmt.Errorf("construct wire request")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if correlation != "" {
		req.Header.Set("X-Correlation-ID", correlation)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := w.http.Do(req)
	if err != nil {
		// net/url errors include the complete URL, including continuation tokens.
		return nil, fmt.Errorf("send wire request: %T", err)
	}
	return res, nil
}

func (w wire) restCall(ctx context.Context, actor, correlation, method, path string, body any) (reply, error) {
	res, err := w.request(ctx, method, w.env.APIURL+path, w.env.Identities[actor].RESTToken, correlation, body)
	if err != nil {
		return reply{}, err
	}
	defer res.Body.Close()
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   struct {
			Code    string          `json:"code"`
			Message string          `json:"message"`
			Details json.RawMessage `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxResponse)).Decode(&envelope); err != nil {
		return reply{}, fmt.Errorf("invalid REST response envelope (HTTP %d)", res.StatusCode)
	}
	return reply{
		data: envelope.Data, status: res.StatusCode,
		code: envelope.Error.Code, message: envelope.Error.Message, details: envelope.Error.Details,
		failed: !envelope.Success || res.StatusCode != http.StatusOK,
	}, nil
}

func (w wire) openMCP(ctx context.Context, actor, correlation string) (*mcpclient.Client, error) {
	headers := map[string]string{"Authorization": "Bearer " + w.env.Identities[actor].MCPToken}
	if correlation != "" {
		headers["X-Correlation-ID"] = correlation
	}
	clientHTTP := *w.http
	base := clientHTTP.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	capture := &sessionCapture{}
	clientHTTP.Transport = sessionCaptureTransport{base: base, capture: capture}
	client, err := mcpclient.NewStreamableHttpClient(w.env.MCPURL,
		transport.WithHTTPBasicClient(&clientHTTP), transport.WithHTTPHeaders(headers))
	if err != nil {
		return nil, fmt.Errorf("construct MCP client")
	}
	if err := client.Start(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("start MCP client")
	}
	if _, err := client.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "data-fabric-public-acceptance", Version: "1"},
	}}); err != nil {
		client.Close()
		return nil, fmt.Errorf("initialize MCP client")
	}
	if w.mcpCaptures != nil {
		w.mcpCaptures[actor] = capture
	}
	return client, nil
}

func (w wire) tool(ctx context.Context, actor, correlation, name string, args map[string]any) (reply, error) {
	if w.mcpSessions != nil {
		client := w.mcpSessions[actor]
		if client == nil || (correlation != "" && w.mcpCorrelations[actor] != correlation) {
			if client != nil {
				if err := w.releaseMCPActor(ctx, actor); err != nil {
					return reply{}, err
				}
			}
			newClient, err := w.openMCP(ctx, actor, correlation)
			if err != nil {
				return reply{}, err
			}
			client = newClient
			w.mcpSessions[actor] = client
			w.mcpCorrelations[actor] = correlation
		}
		return callTool(ctx, client, name, args)
	}
	client, err := w.openMCP(ctx, actor, correlation)
	if err != nil {
		return reply{}, err
	}
	defer client.Close()
	return callTool(ctx, client, name, args)
}

func callTool(ctx context.Context, client *mcpclient.Client, name string, args map[string]any) (reply, error) {
	result, err := client.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name, Arguments: args}})
	if err != nil {
		return reply{}, fmt.Errorf("call MCP tool %s: %T", name, err)
	}
	if result == nil || len(result.Content) != 1 {
		return reply{}, fmt.Errorf("unexpected MCP tool content")
	}
	content, ok := result.Content[0].(mcp.TextContent)
	if !ok || len(content.Text) > maxResponse {
		return reply{}, fmt.Errorf("unexpected MCP text content")
	}
	if result.IsError {
		return reply{failed: true, message: content.Text}, nil
	}
	return reply{data: json.RawMessage(content.Text)}, nil
}

func (w wire) query(ctx context.Context, actor, correlation string, source reference.Source, sql string, parameters []any, size, timeout int) (reply, error) {
	if w.kind == rest {
		return w.restCall(ctx, actor, correlation, http.MethodPost, "/api/v1/fetch", map[string]any{
			"dataSourceId": source.ID, "sql": sql, "parameters": parameters,
			"type": 2, "batch_size": size, "timeout": timeout,
		})
	}
	return w.tool(ctx, actor, correlation, "execute_sql_query", map[string]any{
		"datasource_id": source.ID, "sql": sql, "parameters": parameters,
		"batch_size": size, "timeout": timeout,
	})
}

func (w wire) next(ctx context.Context, actor string, previous batch) (reply, error) {
	if previous.NextURI == "" || previous.Token == "" || previous.Slug == "" || previous.QueryID == "" {
		return reply{}, fmt.Errorf("missing continuation credentials")
	}
	if w.kind == rest {
		path, err := url.Parse(previous.NextURI)
		if err != nil || path.IsAbs() || path.Host != "" || path.Fragment != "" ||
			!strings.HasPrefix(path.Path, "/api/v1/fetch/") {
			return reply{}, fmt.Errorf("invalid relative continuation URI")
		}
		query := path.Query()
		query.Set("batch_size", "1")
		path.RawQuery = query.Encode()
		return w.restCall(ctx, actor, "", http.MethodGet, path.String(), nil)
	}
	return w.tool(ctx, actor, "", "fetch_next_batch", nextArguments(previous))
}

func nextArguments(previous batch) map[string]any {
	return map[string]any{
		"query_id": previous.QueryID, "slug": previous.Slug, "token": previous.Token, "batch_size": 1,
	}
}

func (w wire) listSources(ctx context.Context, actor string) (reply, error) {
	if w.kind == rest {
		return w.restCall(ctx, actor, "", http.MethodGet, "/api/v1/datasources", nil)
	}
	return w.tool(ctx, actor, "", "list_data_sources", nil)
}

func (w wire) getSource(ctx context.Context, actor string, source reference.Source) (reply, error) {
	if w.kind == rest {
		return w.restCall(ctx, actor, "", http.MethodGet, "/api/v1/datasources/"+url.PathEscape(source.ID), nil)
	}
	return w.tool(ctx, actor, "", "get_data_source_info", map[string]any{"datasource_id": source.ID})
}

func (w wire) rejectAuth(ctx context.Context, token string) error {
	var res *http.Response
	var err error
	if w.kind == rest {
		res, err = w.request(ctx, http.MethodGet, w.env.APIURL+"/api/v1/datasources", token, "", nil)
	} else {
		res, err = w.request(ctx, http.MethodPost, w.env.MCPURL, token, "", map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize",
			"params": map[string]any{
				"protocolVersion": mcp.LATEST_PROTOCOL_VERSION,
				"clientInfo":      map[string]string{"name": "acceptance-auth-negative", "version": "1"},
				"capabilities":    map[string]any{},
			},
		})
	}
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("expected authentication rejection, observed HTTP %d", res.StatusCode)
	}
	if w.kind == mcpHTTP && !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer") {
		return fmt.Errorf("MCP authentication rejection has no bearer challenge")
	}
	return nil
}
