package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
	"github.com/RhinoLink-Tech/data-fabric/internal/replicarouting"
)

func waitOwnerBoot(ctx context.Context, env *reference.Environment, slot, previous string, timeout time.Duration) (ownerStatus, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := readOwnerStatus(ctx, env, slot)
		if err == nil && status.BootID != previous && status.State == "ready" {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return ownerStatus{}, ctx.Err()
		case <-deadline.C:
			if err != nil {
				return ownerStatus{}, err
			}
			return ownerStatus{}, fmt.Errorf("%s did not restart with a new ready boot", slot)
		case <-ticker.C:
		}
	}
}

func checkRESTOwnerRestart(ctx context.Context, env *reference.Environment, client *http.Client) error {
	if err := env.SetRoutedNewOwner(ctx, replicarouting.REST, "rest-b"); err != nil {
		return err
	}
	w := wire{kind: rest, env: env, http: client}
	first, err := readQuery(ctx, w, env.Sources["mysql"], orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, first, "rest-b"); err != nil {
		return err
	}
	before, err := readOwnerStatus(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	if err := env.RestartReplica(ctx, "rest-b"); err != nil {
		return err
	}
	if _, err := waitOwnerBoot(ctx, env, "rest-b", before.BootID, 90*time.Second); err != nil {
		return err
	}
	beforeRequests, err := ownerContinuationCount(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	beforeMetrics, err := ownerMetrics(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	beforeQueries := metricValue(beforeMetrics, "data_fabric_query_total", "")
	unavailable, err := w.next(ctx, reader, first)
	if err != nil {
		return err
	}
	if err := unavailable.expectError(rest, http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", sessionError); err != nil {
		return err
	}
	afterRequests, err := ownerContinuationCount(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	afterMetrics, err := ownerMetrics(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	if afterRequests != beforeRequests+1 ||
		metricValue(afterMetrics, "data_fabric_query_total", "") != beforeQueries {
		return fmt.Errorf("old boot continuation reached SQL or was routed to another owner")
	}
	fresh, err := readQuery(ctx, w, env.Sources["mysql"], orderedIDs, nil, 1)
	if err != nil {
		return fmt.Errorf("replacement owner rejected fresh work: %w", err)
	}
	if err := checkRESTPageOwner(env, fresh, "rest-b"); err != nil {
		return err
	}
	return finishThirdRow(ctx, w, fresh, fresh.QueryID)
}

func checkRESTOwnerDrain(ctx context.Context, env *reference.Environment, client *http.Client) error {
	if err := env.SetRoutedNewOwner(ctx, replicarouting.REST, "rest-a"); err != nil {
		return err
	}
	w := wire{kind: rest, env: env, http: client}
	first, err := readQuery(ctx, w, env.Sources["mysql"], orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, first, "rest-a"); err != nil {
		return err
	}
	_, _, body, err := ownerRequest(ctx, env, "rest-a", "operator", http.MethodPost, "/internal/replica/drain")
	if err != nil {
		return err
	}
	var draining ownerStatus
	if err := json.Unmarshal(body, &draining); err != nil || draining.State != "draining" || draining.DrainDeadline == nil {
		return fmt.Errorf("REST owner did not report a drain deadline")
	}
	_, _, repeatedBody, err := ownerRequest(ctx, env, "rest-a", "operator", http.MethodPost, "/internal/replica/drain")
	if err != nil {
		return err
	}
	var repeated ownerStatus
	if err := json.Unmarshal(repeatedBody, &repeated); err != nil || repeated.DrainDeadline == nil ||
		!repeated.DrainDeadline.Equal(*draining.DrainDeadline) {
		return fmt.Errorf("repeated REST drain moved the original deadline")
	}
	rejected, err := w.query(ctx, reader, "", env.Sources["mysql"], orderedIDs, nil, 1, 5)
	if err != nil {
		return err
	}
	if !rejected.failed || rejected.status != http.StatusServiceUnavailable ||
		rejected.code != "QUERY_OWNER_DRAINING" || !noExposedQueryResult(rejected) {
		return fmt.Errorf("draining REST owner admitted new query work")
	}
	second, err := readBatch(w.next(ctx, reader, first))
	if err != nil {
		return fmt.Errorf("draining owner stopped a valid continuation before its deadline: %w", err)
	}
	if err := expectPage(second, first.QueryID, "2", 3, true); err != nil {
		return err
	}
	if err := finishThirdRow(ctx, w, second, first.QueryID); err != nil {
		return err
	}
	if err := env.RestartReplica(ctx, "rest-a"); err != nil {
		return err
	}
	_, err = waitOwnerBoot(ctx, env, "rest-a", draining.BootID, 90*time.Second)
	return err
}

func openMCPForIdentity(ctx context.Context, env *reference.Environment, identity string) (*mcpclient.Client, *sessionCapture, error) {
	capture := &sessionCapture{}
	clientHTTP := newHTTP(env)
	base := clientHTTP.Transport
	clientHTTP.Transport = sessionCaptureTransport{base: base, capture: capture}
	headers := map[string]string{"Authorization": "Bearer " + env.Identities[identity].MCPToken}
	client, err := mcpclient.NewStreamableHttpClient(env.MCPURL,
		transport.WithHTTPBasicClient(clientHTTP), transport.WithHTTPHeaders(headers))
	if err != nil {
		clientHTTP.CloseIdleConnections()
		return nil, nil, fmt.Errorf("construct routed MCP client")
	}
	if err := client.Start(ctx); err != nil {
		client.Close()
		clientHTTP.CloseIdleConnections()
		return nil, nil, fmt.Errorf("start routed MCP client")
	}
	if _, err := client.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "data-fabric-multi-replica-acceptance", Version: "1"},
	}}); err != nil {
		client.Close()
		clientHTTP.CloseIdleConnections()
		return nil, nil, fmt.Errorf("initialize routed MCP client")
	}
	if capture.sessionID() == "" {
		client.Close()
		clientHTTP.CloseIdleConnections()
		return nil, nil, fmt.Errorf("MCP initialize omitted its session header")
	}
	return client, capture, nil
}

func mcpInitializeBody(id int) []byte {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": mcp.LATEST_PROTOCOL_VERSION,
			"clientInfo":      map[string]string{"name": "data-fabric-acceptance", "version": "1"},
			"capabilities":    map[string]any{},
		},
	})
	return body
}

func mcpToolCallBody(id int, name string, arguments map[string]any) []byte {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": arguments},
	})
	return body
}

func checkMCPUnavailable(status int, body []byte, secrets ...string) error {
	response := strings.ToLower(strings.TrimSpace(string(body)))
	if status != http.StatusNotFound ||
		(!strings.Contains(response, "session_unavailable") && response != "not found") {
		return fmt.Errorf("MCP stale or foreign session returned HTTP %d with an unexpected unavailable response", status)
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(string(body), secret) {
			return fmt.Errorf("MCP unavailable response exposed session or query credentials")
		}
	}
	return nil
}

func checkMCPPrincipalBinding(ctx context.Context, env *reference.Environment, client *http.Client) error {
	if err := env.SetRoutedNewOwner(ctx, replicarouting.MCP, "mcp-b"); err != nil {
		return err
	}
	primary, primaryCapture, err := openMCPForIdentity(ctx, env, reader)
	if err != nil {
		return err
	}
	defer primary.Close()
	primarySession := primaryCapture.sessionID()
	first, err := runMCPQuery(ctx, primary, env.Sources["mysql"])
	if err != nil {
		return err
	}
	if err := checkMCPPageOwner(env, first, "mcp-b"); err != nil {
		return err
	}
	if err := expectPage(first, "", "1", 3, true); err != nil {
		return err
	}

	body := mcpToolCallBody(41, "fetch_next_batch", nextArguments(first))
	for _, identity := range []string{"app-a", "app-a-tenant-b"} {
		status, _, response, err := sendHTTP(ctx, client, http.MethodPost, env.MCPURL,
			env.Identities[identity].MCPToken, primarySession, "", body)
		if err != nil {
			return err
		}
		if err := checkMCPUnavailable(status, response, first.QueryID, first.Slug, first.Token); err != nil {
			return fmt.Errorf("%s used another principal's MCP session: %w", identity, err)
		}
	}
	status, _, response, err := sendHTTP(ctx, client, http.MethodPost, env.MCPURL,
		env.Identities[reader].MCPToken, "", "", body)
	if err != nil {
		return err
	}
	if status != http.StatusBadRequest || !strings.Contains(string(response), "session_required") ||
		strings.Contains(string(response), first.Token) {
		return fmt.Errorf("MCP request without a session header was not safely rejected")
	}

	second, secondCapture, err := openMCPForIdentity(ctx, env, reader)
	if err != nil {
		return err
	}
	defer second.Close()
	crossSession, err := callTool(ctx, second, "fetch_next_batch", nextArguments(first))
	if err != nil {
		return err
	}
	if !crossSession.failed || !noExposedQueryResult(crossSession) {
		return fmt.Errorf("a second MCP session reused the first session's query credentials")
	}
	next, err := runMCPNext(ctx, primary, first)
	if err != nil {
		return fmt.Errorf("rejected MCP principals advanced the original cursor: %w", err)
	}
	if err := checkMCPPageOwner(env, next, "mcp-b"); err != nil {
		return err
	}
	last, err := runMCPNext(ctx, primary, next)
	if err != nil {
		return err
	}
	if err := expectPage(last, first.QueryID, "3", 3, false); err != nil {
		return err
	}
	secondSession := secondCapture.sessionID()
	for _, session := range []string{primarySession, secondSession} {
		if _, err := routedMCPSessionRequest(ctx, client, env, session, http.MethodDelete); err != nil {
			return err
		}
	}
	return nil
}

func checkMCPSessionTermination(ctx context.Context, env *reference.Environment, client *http.Client) error {
	if err := env.SetRoutedNewOwner(ctx, replicarouting.MCP, "mcp-b"); err != nil {
		return err
	}
	ownerClient, capture, err := openMCPForIdentity(ctx, env, reader)
	if err != nil {
		return err
	}
	session := capture.sessionID()
	first, err := runMCPQuery(ctx, ownerClient, env.Sources["mysql"])
	if err != nil {
		ownerClient.Close()
		return err
	}
	status, err := routedMCPSessionRequest(ctx, client, env, session, http.MethodDelete)
	if err != nil {
		ownerClient.Close()
		return err
	}
	ownerClient.Close()
	if status != http.StatusOK {
		return fmt.Errorf("MCP session DELETE returned HTTP %d", status)
	}
	if err := waitForMCPSessionGone(ctx, client, env, session); err != nil {
		return err
	}
	body := mcpToolCallBody(51, "fetch_next_batch", nextArguments(first))
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		status, _, response, err := sendHTTP(ctx, client, method, env.MCPURL,
			env.Identities[reader].MCPToken, session, "", body)
		if err != nil {
			return err
		}
		if err := checkMCPUnavailable(status, response, first.QueryID, first.Slug, first.Token); err != nil {
			return fmt.Errorf("terminated session %s: %w", method, err)
		}
	}
	newClient, newCapture, err := openMCPForIdentity(ctx, env, reader)
	if err != nil {
		return fmt.Errorf("new MCP session could not initialize after a 404: %w", err)
	}
	newSession := newCapture.sessionID()
	defer newClient.Close()
	oldQuery, err := callTool(ctx, newClient, "fetch_next_batch", nextArguments(first))
	if err != nil {
		return err
	}
	if !oldQuery.failed || !noExposedQueryResult(oldQuery) {
		return fmt.Errorf("new MCP transport session reused a terminated query")
	}
	status, err = routedMCPSessionRequest(ctx, client, env, newSession, http.MethodDelete)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("could not release reinitialized MCP session")
	}
	return nil
}

func rawMCPInitialize(ctx context.Context, env *reference.Environment, client *http.Client, body []byte) (int, http.Header, []byte, error) {
	return sendHTTP(ctx, client, http.MethodPost, env.MCPURL,
		env.Identities[reader].MCPToken, "", "", body)
}

func checkMCPTransportCapacity(ctx context.Context, env *reference.Environment, client *http.Client) error {
	if err := env.SetRoutedNewOwner(ctx, replicarouting.MCP, "mcp-b"); err != nil {
		return err
	}
	if err := env.SetRoutedMCPSessionCapacity(ctx, "mcp-b", 4); err != nil {
		return err
	}
	var sessions []string
	var clients []*mcpclient.Client
	cleanup := func() {
		for index, session := range sessions {
			_, _ = routedMCPSessionRequest(context.Background(), client, env, session, http.MethodDelete)
			if index < len(clients) {
				clients[index].Close()
			}
		}
	}
	restore := func() {
		cleanup()
		_ = env.SetRoutedMCPSessionCapacity(context.Background(), "mcp-b", reference.ReferenceMaxRoutedMCPSessions)
	}
	for range 4 {
		opened, capture, err := openMCPForIdentity(ctx, env, reader)
		if err != nil {
			restore()
			return err
		}
		clients = append(clients, opened)
		sessions = append(sessions, capture.sessionID())
	}
	before, err := readOwnerStatus(ctx, env, "mcp-b")
	if err != nil {
		restore()
		return err
	}
	status, headers, body, err := rawMCPInitialize(ctx, env, client, mcpInitializeBody(61))
	if err != nil {
		restore()
		return err
	}
	if status != http.StatusServiceUnavailable || !strings.Contains(string(body), "session_capacity") ||
		headers.Get("Mcp-Session-Id") != "" {
		restore()
		return fmt.Errorf("MCP session capacity did not reject initialization before allocation")
	}
	after, err := readOwnerStatus(ctx, env, "mcp-b")
	if err != nil || after.PendingSessions != before.PendingSessions {
		restore()
		return fmt.Errorf("rejected MCP initialization changed the live session count")
	}

	_, _ = routedMCPSessionRequest(ctx, client, env, sessions[0], http.MethodDelete)
	clients[0].Close()
	sessions, clients = sessions[1:], clients[1:]
	if err := waitOwnerPendingAtMost(ctx, env, "mcp-b", 3, 2*time.Second); err != nil {
		restore()
		return err
	}
	failedStatus, _, failedInit, err := rawMCPInitialize(ctx, env, client,
		[]byte("{\"jsonrpc\":\"2.0\",\"id\":62,\"method\":\"initialize\",\"params\":{\"protocolVersion\":[]}}"))
	if err != nil {
		restore()
		return err
	}
	if strings.Contains(string(failedInit), "session_capacity") ||
		(!strings.Contains(string(failedInit), `"error"`) && failedStatus != http.StatusBadRequest) {
		restore()
		return fmt.Errorf("invalid initialize did not reach the MCP SDK failure path (HTTP %d: %s)", failedStatus, strings.TrimSpace(string(failedInit)))
	}
	if err := waitOwnerPendingAtMost(ctx, env, "mcp-b", 3, 2*time.Second); err != nil {
		restore()
		return fmt.Errorf("failed initialization leaked a reserved transport slot: %w", err)
	}
	replacement, capture, err := openMCPForIdentity(ctx, env, reader)
	if err != nil {
		restore()
		return fmt.Errorf("failed initialization did not release its reservation: %w", err)
	}
	clients = append(clients, replacement)
	sessions = append(sessions, capture.sessionID())
	cleanup()
	if err := waitOwnerPendingAtMost(ctx, env, "mcp-b", 0, 3*time.Second); err != nil {
		_ = env.SetRoutedMCPSessionCapacity(context.Background(), "mcp-b", reference.ReferenceMaxRoutedMCPSessions)
		return err
	}
	return env.SetRoutedMCPSessionCapacity(ctx, "mcp-b", reference.ReferenceMaxRoutedMCPSessions)
}
