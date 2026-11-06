package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
	"github.com/RhinoLink-Tech/data-fabric/internal/replicarouting"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

type replicaEvidence struct {
	Case       string
	Transport  surface
	Result     string
	DurationMS int64
	OwnerSlots string
	Reason     string
}

func (r replicaEvidence) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"case": r.Case, "transport": r.Transport, "result": r.Result,
		"duration_ms": r.DurationMS, "owner_slots": r.OwnerSlots, "reason": r.Reason,
	})
}

// CheckMultiReplica verifies public continuation and transport-session routing
// against the two-replica Docker profile, not in-process proxy fixtures.
func CheckMultiReplica(ctx context.Context, env *reference.Environment, out io.Writer) error {
	httpClient := newHTTP(env)
	defer httpClient.CloseIdleConnections()

	cases := []struct {
		id, transport, slots string
		check                func(context.Context) error
	}{
		{"R1", string(rest), "rest-a->rest-b", func(ctx context.Context) error {
			return checkRESTOwnerSwitch(ctx, env, httpClient)
		}},
		{"M1", string(mcpHTTP), "mcp-a->mcp-b", func(ctx context.Context) error {
			return checkMCPOwnerSwitch(ctx, env, httpClient)
		}},
	}
	encoder := json.NewEncoder(out)
	var failures []error
	for _, test := range cases {
		started := time.Now()
		err := test.check(ctx)
		record := replicaEvidence{
			Case: test.id, Transport: surface(test.transport), Result: "pass",
			DurationMS: time.Since(started).Milliseconds(), OwnerSlots: test.slots,
		}
		if err != nil {
			record.Result, record.Reason = "fail", err.Error()
			failures = append(failures, fmt.Errorf("%s %s: %w", test.id, test.transport, err))
		}
		if encodeErr := encoder.Encode(record); encodeErr != nil {
			return fmt.Errorf("write multi-replica evidence: %w", encodeErr)
		}
	}
	return errors.Join(failures...)
}

func checkRESTOwnerSwitch(ctx context.Context, env *reference.Environment, client *http.Client) error {
	w := wire{kind: rest, env: env, http: client}
	source := env.Sources["mysql"]
	first, err := readQuery(ctx, w, source, orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, first, "rest-a"); err != nil {
		return err
	}
	if err := expectPage(first, "", "1", 3, true); err != nil {
		return err
	}
	if err := env.SetRoutedNewOwner(ctx, replicarouting.REST, "rest-b"); err != nil {
		return err
	}

	second, err := readBatch(w.next(ctx, reader, first))
	if err != nil {
		return err
	}
	if second.Token == first.Token {
		return fmt.Errorf("REST continuation token did not advance")
	}
	if err := checkRESTPageOwner(env, second, "rest-a"); err != nil {
		return err
	}
	if err := expectPage(second, first.QueryID, "2", 3, true); err != nil {
		return err
	}
	third, err := readBatch(w.next(ctx, reader, second))
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, third, "rest-a"); err != nil {
		return err
	}
	if err := expectPage(third, first.QueryID, "3", 3, false); err != nil {
		return err
	}

	fresh, err := readQuery(ctx, w, source, orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, fresh, "rest-b"); err != nil {
		return err
	}
	if err := expectPage(fresh, "", "1", 3, true); err != nil {
		return err
	}
	next, err := readBatch(w.next(ctx, reader, fresh))
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, next, "rest-b"); err != nil {
		return err
	}
	if err := expectPage(next, fresh.QueryID, "2", 3, true); err != nil {
		return err
	}
	last, err := readBatch(w.next(ctx, reader, next))
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, last, "rest-b"); err != nil {
		return err
	}
	return expectPage(last, fresh.QueryID, "3", 3, false)
}

func checkRESTPageOwner(env *reference.Environment, page batch, want string) error {
	slot, err := env.RoutedOwner(page.Slug, replicarouting.REST, page.QueryID)
	if err != nil || slot != want {
		return fmt.Errorf("REST query owner = %q, want %s", slot, want)
	}
	return nil
}

type sessionCapture struct {
	mu sync.Mutex
	id string
}

func (c *sessionCapture) save(id string) {
	if id == "" {
		return
	}
	c.mu.Lock()
	c.id = id
	c.mu.Unlock()
}

func (c *sessionCapture) sessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.id
}

type sessionCaptureTransport struct {
	base    http.RoundTripper
	capture *sessionCapture
}

func (t sessionCaptureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err == nil && response != nil {
		t.capture.save(response.Header.Get("Mcp-Session-Id"))
	}
	return response, err
}

func (t sessionCaptureTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func openMCPWithSession(ctx context.Context, env *reference.Environment) (*mcpclient.Client, *sessionCapture, error) {
	capture := &sessionCapture{}
	httpClient := newHTTP(env)
	base := httpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	httpClient.Transport = sessionCaptureTransport{base: base, capture: capture}
	headers := map[string]string{"Authorization": "Bearer " + env.Identities[reader].MCPToken}
	client, err := mcpclient.NewStreamableHttpClient(env.MCPURL,
		transport.WithHTTPBasicClient(httpClient), transport.WithHTTPHeaders(headers))
	if err != nil {
		return nil, nil, fmt.Errorf("construct routed MCP client")
	}
	if err := client.Start(ctx); err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("start routed MCP client")
	}
	if _, err := client.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "data-fabric-two-replica-acceptance", Version: "1"},
	}}); err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("initialize routed MCP client")
	}
	if capture.sessionID() == "" {
		client.Close()
		return nil, nil, fmt.Errorf("MCP initialize response omitted the owner session marker")
	}
	return client, capture, nil
}

func checkMCPOwnerSwitch(ctx context.Context, env *reference.Environment, client *http.Client) error {
	mcpClient, capture, err := openMCPWithSession(ctx, env)
	if err != nil {
		return err
	}
	defer mcpClient.Close()
	session := capture.sessionID()
	slot, err := env.RoutedOwner(session, replicarouting.MCP, "")
	if err != nil || slot != "mcp-a" {
		return fmt.Errorf("MCP session owner = %q, want mcp-a", slot)
	}

	source := env.Sources["mysql"]
	first, err := runMCPQuery(ctx, mcpClient, source)
	if err != nil {
		return err
	}
	if err := checkMCPPageOwner(env, first, "mcp-a"); err != nil {
		return err
	}
	if err := expectPage(first, "", "1", 3, true); err != nil {
		return err
	}
	if err := env.SetRoutedNewOwner(ctx, replicarouting.MCP, "mcp-b"); err != nil {
		return err
	}

	second, err := runMCPNext(ctx, mcpClient, first)
	if err != nil {
		return err
	}
	if second.Token == first.Token {
		return fmt.Errorf("MCP continuation token did not advance")
	}
	if err := checkMCPPageOwner(env, second, "mcp-a"); err != nil {
		return err
	}
	if err := expectPage(second, first.QueryID, "2", 3, true); err != nil {
		return err
	}
	third, err := runMCPNext(ctx, mcpClient, second)
	if err != nil {
		return err
	}
	if err := checkMCPPageOwner(env, third, "mcp-a"); err != nil {
		return err
	}
	if err := expectPage(third, first.QueryID, "3", 3, false); err != nil {
		return err
	}

	status, err := routedMCPSessionRequest(ctx, client, env, session, http.MethodGet)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("MCP session GET returned HTTP %d, want 200", status)
	}
	status, err = routedMCPSessionRequest(ctx, client, env, session, http.MethodDelete)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("MCP session DELETE returned HTTP %d, want 200", status)
	}
	mcpClient.Close()
	if err := waitForMCPSessionGone(ctx, client, env, session); err != nil {
		return err
	}

	newClient, newCapture, err := openMCPWithSession(ctx, env)
	if err != nil {
		return err
	}
	defer newClient.Close()
	newSession := newCapture.sessionID()
	slot, err = env.RoutedOwner(newSession, replicarouting.MCP, "")
	if err != nil || slot != "mcp-b" {
		return fmt.Errorf("new MCP session owner = %q, want mcp-b", slot)
	}
	fresh, err := runMCPQuery(ctx, newClient, source)
	if err != nil {
		return err
	}
	if err := checkMCPPageOwner(env, fresh, "mcp-b"); err != nil {
		return err
	}
	if err := expectPage(fresh, "", "1", 3, true); err != nil {
		return err
	}
	last, err := runMCPNext(ctx, newClient, fresh)
	if err != nil {
		return err
	}
	if err := checkMCPPageOwner(env, last, "mcp-b"); err != nil {
		return err
	}
	if err := expectPage(last, fresh.QueryID, "2", 3, true); err != nil {
		return err
	}
	terminal, err := runMCPNext(ctx, newClient, last)
	if err != nil {
		return err
	}
	if err := checkMCPPageOwner(env, terminal, "mcp-b"); err != nil {
		return err
	}
	if err := expectPage(terminal, fresh.QueryID, "3", 3, false); err != nil {
		return err
	}
	status, err = routedMCPSessionRequest(ctx, client, env, newSession, http.MethodDelete)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("new MCP session DELETE returned HTTP %d, want 200", status)
	}
	return nil
}

func runMCPQuery(ctx context.Context, client *mcpclient.Client, source reference.Source) (batch, error) {
	return readBatch(callTool(ctx, client, "execute_sql_query", map[string]any{
		"datasource_id": source.ID, "sql": orderedIDs, "batch_size": 1, "timeout": 5,
	}))
}

func runMCPNext(ctx context.Context, client *mcpclient.Client, previous batch) (batch, error) {
	return readBatch(callTool(ctx, client, "fetch_next_batch", nextArguments(previous)))
}

func checkMCPPageOwner(env *reference.Environment, page batch, want string) error {
	slot, err := env.RoutedOwner(page.Slug, replicarouting.MCP, page.QueryID)
	if err != nil || slot != want {
		return fmt.Errorf("MCP query owner = %q, want %s", slot, want)
	}
	return nil
}

func routedMCPSessionRequest(ctx context.Context, client *http.Client, env *reference.Environment, session, method string) (int, error) {
	requestCtx := ctx
	cancel := func() {}
	if method == http.MethodGet {
		requestCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
	}
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, env.MCPURL, nil)
	if err != nil {
		return 0, fmt.Errorf("construct MCP session request")
	}
	request.Header.Set("Authorization", "Bearer "+env.Identities[reader].MCPToken)
	request.Header.Set("Mcp-Session-Id", session)
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("send MCP session request: %T", err)
	}
	defer response.Body.Close()
	if method != http.MethodGet {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponse))
	}
	return response.StatusCode, nil
}

func waitForMCPSessionGone(ctx context.Context, client *http.Client, env *reference.Environment, session string) error {
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := routedMCPSessionRequest(ctx, client, env, session, http.MethodGet)
		if err == nil && status == http.StatusNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("deleted MCP session GET returned HTTP %d, want 404", status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("deleted MCP session remained available")
		case <-ticker.C:
		}
	}
}
