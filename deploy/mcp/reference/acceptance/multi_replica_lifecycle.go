package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
	"github.com/RhinoLink-Tech/data-fabric/internal/replicarouting"
)

func checkRoutingKeyRotation(ctx context.Context, env *reference.Environment, client *http.Client) error {
	rest := wire{kind: rest, env: env, http: client}
	if err := env.SetRoutedNewOwner(ctx, replicarouting.REST, "rest-a"); err != nil {
		return err
	}
	oldREST, err := readQuery(ctx, rest, env.Sources["mysql"], orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, oldREST, "rest-a"); err != nil {
		return err
	}
	markerFields := strings.Split(oldREST.Slug, ".")
	if len(markerFields) != 9 || markerFields[1] != "reference" {
		return fmt.Errorf("old REST owner did not issue a marker with the retiring key")
	}
	restMetricsBeforeUnavailable, err := ownerMetrics(ctx, env, "rest-a")
	if err != nil {
		return err
	}
	noCountProbe, err := readTrinoNoCount(rest.queryNoCountWithMode(ctx, reader, env.Sources["mysql"], orderedIDs, nil, 1, "pagination"))
	if err != nil {
		return fmt.Errorf("start no-count owner-loss probe: %w", err)
	}
	if noCountProbe.ExecutionMode != "pagination" || noCountProbe.NextURI == "" {
		return fmt.Errorf("no-count owner-loss probe has no pagination continuation")
	}
	if err := checkRESTPageOwner(env, noCountProbe.batch, "rest-a"); err != nil {
		return err
	}
	if _, _, body, err := ownerRequest(ctx, env, "rest-a", "operator", http.MethodPost, "/internal/replica/drain"); err != nil {
		return err
	} else {
		var status ownerStatus
		if err := json.Unmarshal(body, &status); err != nil || status.State != "draining" {
			return fmt.Errorf("old REST owner did not begin draining")
		}
	}
	if err := env.SetRoutedNewOwner(ctx, replicarouting.REST, "rest-b"); err != nil {
		return err
	}
	newREST, err := readQuery(ctx, rest, env.Sources["mysql"], orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, newREST, "rest-b"); err != nil {
		return err
	}
	if err := finishThirdRow(ctx, rest, newREST, newREST.QueryID); err != nil {
		return err
	}
	oldRESTSecond, err := readBatch(rest.next(ctx, reader, oldREST))
	if err != nil {
		return fmt.Errorf("old REST session did not survive key overlap and drain: %w", err)
	}
	oldRESTLast, err := readBatch(rest.next(ctx, reader, oldRESTSecond))
	if err != nil {
		return err
	}
	if err := expectPage(oldRESTLast, oldREST.QueryID, "3", 3, false); err != nil {
		return err
	}
	if _, err := waitOwnerRetired(ctx, env, "rest-a", 4*time.Second); err != nil {
		return err
	}
	noCountUnavailable, err := rest.next(ctx, reader, noCountProbe.batch)
	if err != nil {
		return err
	}
	if err := noCountUnavailable.expectError(surface("rest"), http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", sessionError); err != nil {
		return fmt.Errorf("retired REST owner did not reject the no-count continuation: %w", err)
	}
	restMetricsAfterUnavailable, err := ownerMetrics(ctx, env, "rest-a")
	if err != nil {
		return err
	}
	if err := assertOwnerLogicalOutcomeDelta(restMetricsBeforeUnavailable, restMetricsAfterUnavailable, "owner_unavailable", 1); err != nil {
		return fmt.Errorf("retired REST continuation was not recorded once as owner unavailable: %w", err)
	}

	if err := env.SetRoutedNewOwner(ctx, replicarouting.MCP, "mcp-a"); err != nil {
		return err
	}
	oldMCPClient, oldCapture, err := openMCPForIdentity(ctx, env, reader)
	if err != nil {
		return err
	}
	oldSession := oldCapture.sessionID()
	oldSessionFields := strings.Split(oldSession, ".")
	if len(oldSessionFields) != 9 || oldSessionFields[1] != "reference" {
		oldMCPClient.Close()
		return fmt.Errorf("old MCP owner did not issue a session with the retiring key")
	}
	oldMCPFirst, err := runMCPQuery(ctx, oldMCPClient, env.Sources["mysql"])
	if err != nil {
		oldMCPClient.Close()
		return err
	}
	if err := checkMCPPageOwner(env, oldMCPFirst, "mcp-a"); err != nil {
		oldMCPClient.Close()
		return err
	}
	noCountMCP, err := readTrinoNoCount(callTool(ctx, oldMCPClient, "execute_sql_query", map[string]any{
		"datasource_id": env.Sources["mysql"].ID, "sql": orderedIDs,
		"batch_size": 1, "timeout": 5, "count_mode": "none", "execution_mode": "pagination",
	}))
	if err != nil {
		oldMCPClient.Close()
		return fmt.Errorf("start MCP no-count owner-loss probe: %w", err)
	}
	if noCountMCP.ExecutionMode != "pagination" || noCountMCP.NextURI == "" {
		oldMCPClient.Close()
		return fmt.Errorf("MCP no-count owner-loss probe has no pagination continuation")
	}
	if err := checkMCPPageOwner(env, noCountMCP.batch, "mcp-a"); err != nil {
		oldMCPClient.Close()
		return err
	}
	if _, _, body, err := ownerRequest(ctx, env, "mcp-a", "operator", http.MethodPost, "/internal/replica/drain"); err != nil {
		oldMCPClient.Close()
		return err
	} else {
		var status ownerStatus
		if err := json.Unmarshal(body, &status); err != nil || status.State != "draining" {
			oldMCPClient.Close()
			return fmt.Errorf("old MCP owner did not begin draining")
		}
	}
	if err := env.SetRoutedNewOwner(ctx, replicarouting.MCP, "mcp-b"); err != nil {
		oldMCPClient.Close()
		return err
	}
	newMCPClient, newCapture, err := openMCPForIdentity(ctx, env, reader)
	if err != nil {
		oldMCPClient.Close()
		return err
	}
	newSession := newCapture.sessionID()
	newMCPFirst, err := runMCPQuery(ctx, newMCPClient, env.Sources["mysql"])
	if err != nil {
		oldMCPClient.Close()
		newMCPClient.Close()
		return err
	}
	if err := checkMCPPageOwner(env, newMCPFirst, "mcp-b"); err != nil {
		oldMCPClient.Close()
		newMCPClient.Close()
		return err
	}
	newMCPStep, err := runMCPNext(ctx, newMCPClient, newMCPFirst)
	if err != nil {
		oldMCPClient.Close()
		newMCPClient.Close()
		return err
	}
	newMCPLast, err := runMCPNext(ctx, newMCPClient, newMCPStep)
	if err != nil {
		oldMCPClient.Close()
		newMCPClient.Close()
		return err
	}
	if err := expectPage(newMCPLast, newMCPFirst.QueryID, "3", 3, false); err != nil {
		oldMCPClient.Close()
		newMCPClient.Close()
		return err
	}
	oldMCPSecond, err := runMCPNext(ctx, oldMCPClient, oldMCPFirst)
	if err != nil {
		oldMCPClient.Close()
		newMCPClient.Close()
		return fmt.Errorf("old MCP session did not survive key overlap and drain: %w", err)
	}
	if err := checkMCPPageOwner(env, oldMCPSecond, "mcp-a"); err != nil {
		oldMCPClient.Close()
		newMCPClient.Close()
		return err
	}
	oldMCPLast, err := runMCPNext(ctx, oldMCPClient, oldMCPSecond)
	if err != nil {
		oldMCPClient.Close()
		newMCPClient.Close()
		return err
	}
	if err := expectPage(oldMCPLast, oldMCPFirst.QueryID, "3", 3, false); err != nil {
		oldMCPClient.Close()
		newMCPClient.Close()
		return err
	}
	releaseStatus, err := routedMCPSessionRequest(ctx, client, env, oldSession, http.MethodDelete)
	if err != nil || releaseStatus != http.StatusOK {
		oldMCPClient.Close()
		newMCPClient.Close()
		return fmt.Errorf("could not release old MCP session during key rotation")
	}
	oldMCPClient.Close()
	if _, err := waitOwnerRetired(ctx, env, "mcp-a", 4*time.Second); err != nil {
		newMCPClient.Close()
		return err
	}
	// Confirm that failure is attributable to retirement, not marker expiry.
	if err := checkMCPPageOwner(env, noCountMCP.batch, "mcp-a"); err != nil {
		newMCPClient.Close()
		return fmt.Errorf("MCP no-count probe expired before owner retirement: %w", err)
	}
	unavailableMCP, err := callTool(ctx, newMCPClient, "fetch_next_batch", nextArguments(noCountMCP.batch))
	if err != nil {
		newMCPClient.Close()
		return err
	}
	if err := unavailableMCP.expectError(mcpHTTP, 0, "", sessionError); err != nil {
		newMCPClient.Close()
		return fmt.Errorf("retired MCP owner did not reject the no-count continuation: %w", err)
	}
	releaseStatus, err = routedMCPSessionRequest(ctx, client, env, newSession, http.MethodDelete)
	if err != nil || releaseStatus != http.StatusOK {
		newMCPClient.Close()
		return fmt.Errorf("could not release new MCP session during key rotation")
	}
	newMCPClient.Close()

	oldKeyMarker := oldREST.Slug
	wrongDeploymentREST, err := env.MintAcceptanceMarker(
		"unrelated-deployment", "reference-next", replicarouting.REST, "rest-b",
		"unrelated-rest-query", time.Now().Add(time.Minute),
	)
	if err != nil {
		return err
	}
	wrongDeploymentMCP, err := env.MintAcceptanceMarker(
		"unrelated-deployment", "reference-next", replicarouting.MCP, "mcp-b",
		"unrelated-mcp-session", time.Now().Add(time.Minute),
	)
	if err != nil {
		return err
	}
	restARequests, err := ownerContinuationCount(ctx, env, "rest-a")
	if err != nil {
		return err
	}
	restBRequests, err := ownerContinuationCount(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	mcpARequests, err := ownerMetrics(ctx, env, "mcp-a")
	if err != nil {
		return err
	}
	mcpBRequests, err := ownerMetrics(ctx, env, "mcp-b")
	if err != nil {
		return err
	}
	if err := env.RetireRoutingKey(ctx, "reference"); err != nil {
		return err
	}
	for _, marker := range []struct {
		queryID string
		slug    string
	}{
		{oldREST.QueryID, oldKeyMarker},
		{"unrelated-rest-query", wrongDeploymentREST},
	} {
		status, _, body, err := sendHTTP(ctx, client, http.MethodGet,
			env.APIURL+rawRESTFetchPath(marker.queryID, marker.slug, "opaque-token"),
			env.Identities[reader].RESTToken, "", "", nil)
		if err != nil {
			return err
		}
		if err := assertUnavailableREST(status, body); err != nil {
			return err
		}
	}
	for _, session := range []string{oldSession, wrongDeploymentMCP} {
		status, _, body, err := sendHTTP(ctx, client, http.MethodDelete, env.MCPURL,
			env.Identities[reader].MCPToken, session, "", nil)
		if err != nil {
			return err
		}
		if err := checkMCPUnavailable(status, body, oldSession, newSession); err != nil {
			return err
		}
	}
	restAAfter, err := ownerContinuationCount(ctx, env, "rest-a")
	if err != nil {
		return err
	}
	restBAfter, err := ownerContinuationCount(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	mcpAAfter, err := ownerMetrics(ctx, env, "mcp-a")
	if err != nil {
		return err
	}
	mcpBAfter, err := ownerMetrics(ctx, env, "mcp-b")
	if err != nil {
		return err
	}
	if restAAfter != restARequests || restBAfter != restBRequests ||
		metricValue(mcpAAfter, "data_fabric_http_requests_total", "/mcp") != metricValue(mcpARequests, "data_fabric_http_requests_total", "/mcp") ||
		metricValue(mcpBAfter, "data_fabric_http_requests_total", "/mcp") != metricValue(mcpBRequests, "data_fabric_http_requests_total", "/mcp") {
		return fmt.Errorf("retired-key or wrong-deployment markers reached an owner")
	}
	freshREST, err := readQuery(ctx, rest, env.Sources["mysql"], orderedIDs, nil, 1)
	if err != nil {
		return fmt.Errorf("new REST key did not accept work after retirement: %w", err)
	}
	if err := checkRESTPageOwner(env, freshREST, "rest-b"); err != nil {
		return err
	}
	if err := finishThirdRow(ctx, rest, freshREST, freshREST.QueryID); err != nil {
		return err
	}
	freshMCP, freshCapture, err := openMCPForIdentity(ctx, env, reader)
	if err != nil {
		return fmt.Errorf("new MCP key did not initialize after retirement: %w", err)
	}
	defer freshMCP.Close()
	freshSession := freshCapture.sessionID()
	freshPage, err := runMCPQuery(ctx, freshMCP, env.Sources["mysql"])
	if err != nil {
		return err
	}
	if err := checkMCPPageOwner(env, freshPage, "mcp-b"); err != nil {
		return err
	}
	freshNext, err := runMCPNext(ctx, freshMCP, freshPage)
	if err != nil {
		return err
	}
	freshLast, err := runMCPNext(ctx, freshMCP, freshNext)
	if err != nil {
		return err
	}
	if err := expectPage(freshLast, freshPage.QueryID, "3", 3, false); err != nil {
		return err
	}
	status, err := routedMCPSessionRequest(ctx, client, env, freshSession, http.MethodDelete)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("could not release MCP session under the new key")
	}
	return nil
}
