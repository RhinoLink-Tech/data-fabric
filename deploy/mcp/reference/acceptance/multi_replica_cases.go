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
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
	"github.com/RhinoLink-Tech/data-fabric/internal/replicarouting"
	"github.com/golang-jwt/jwt/v5"
)

const routedFetchMetric = "/api/v1/fetch/:query_id/:slug/:token"

func sendHTTP(ctx context.Context, client *http.Client, method, endpoint, token, session, correlation string, body []byte) (int, http.Header, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("construct acceptance request")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if session != "" {
		request.Header.Set("Mcp-Session-Id", session)
	}
	if correlation != "" {
		request.Header.Set("X-Correlation-ID", correlation)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("send acceptance request: %T", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("read acceptance response")
	}
	return response.StatusCode, response.Header.Clone(), data, nil
}

func continuationPath(queryID, slug, token string) string {
	return "/api/v1/fetch/" + url.PathEscape(queryID) + "/" + url.PathEscape(slug) + "/" + url.PathEscape(token) + "?batch_size=1"
}

func assertUnavailableREST(status int, body []byte) error {
	var envelope struct {
		Success bool
		Error   struct {
			Code    string
			Message string
		}
		Data json.RawMessage
	}
	if status != http.StatusNotFound || json.Unmarshal(body, &envelope) != nil ||
		envelope.Success || envelope.Error.Code != "QUERY_SESSION_UNAVAILABLE" ||
		envelope.Error.Message != "query session is unavailable" {
		return fmt.Errorf("invalid routing marker did not receive the generic REST unavailable response")
	}
	if (len(envelope.Data) != 0 && string(envelope.Data) != "null") ||
		strings.Contains(string(body), "data-fabric-api") ||
		strings.Contains(string(body), "10.0.") ||
		strings.Contains(string(body), "192.168.") {
		return fmt.Errorf("generic REST routing failure exposed owner data")
	}
	return nil
}

func ownerContinuationCount(ctx context.Context, env *reference.Environment, slot string) (float64, error) {
	metrics, err := ownerMetrics(ctx, env, slot)
	if err != nil {
		return 0, err
	}
	return metricValue(metrics, "data_fabric_http_requests_total", routedFetchMetric), nil
}

type dropResponseTransport struct {
	base    http.RoundTripper
	dropped atomic.Bool
}

func (t *dropResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil || response == nil {
		return response, err
	}
	if request.Method == http.MethodGet &&
		strings.HasPrefix(request.URL.Path, "/api/v1/fetch/") &&
		response.StatusCode == http.StatusOK && t.dropped.CompareAndSwap(false, true) {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return nil, errors.New("synthetic acceptance response drop")
	}
	return response, nil
}

func checkRESTDroppedResponse(ctx context.Context, env *reference.Environment, client *http.Client) error {
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
	if err := env.SetRoutedNewOwner(ctx, replicarouting.REST, "rest-b"); err != nil {
		return err
	}
	aBefore, err := ownerContinuationCount(ctx, env, "rest-a")
	if err != nil {
		return err
	}
	bBefore, err := ownerContinuationCount(ctx, env, "rest-b")
	if err != nil {
		return err
	}

	dropper := &dropResponseTransport{base: client.Transport}
	dropClient := *client
	dropClient.Transport = dropper
	dropWire := w
	dropWire.http = &dropClient
	if _, err := dropWire.next(ctx, reader, first); err == nil || !dropper.dropped.Load() {
		return fmt.Errorf("the simulated response loss did not occur after a successful owner fetch")
	}
	aAfter, err := ownerContinuationCount(ctx, env, "rest-a")
	if err != nil {
		return err
	}
	bAfter, err := ownerContinuationCount(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	if aAfter != aBefore+1 || bAfter != bBefore {
		return fmt.Errorf("dropped continuation was retried or reached a different owner")
	}

	replay, err := w.next(ctx, reader, first)
	if err != nil {
		return err
	}
	if err := replay.expectError(rest, http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", sessionError); err != nil {
		return err
	}
	fresh, err := readQuery(ctx, w, env.Sources["mysql"], orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, fresh, "rest-b"); err != nil {
		return err
	}
	return finishThirdRow(ctx, w, fresh, fresh.QueryID)
}

func checkRESTInvalidMarkers(ctx context.Context, env *reference.Environment, client *http.Client) error {
	if err := env.SetRoutedNewOwner(ctx, replicarouting.REST, "rest-a"); err != nil {
		return err
	}
	w := wire{kind: rest, env: env, http: client}
	page, err := readQuery(ctx, w, env.Sources["mysql"], orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, page, "rest-a"); err != nil {
		return err
	}
	fields := strings.Split(page.Slug, ".")
	if len(fields) != 9 {
		return fmt.Errorf("reference query marker has an unexpected format")
	}
	tampered := page.Slug[:len(page.Slug)-1]
	if page.Slug[len(page.Slug)-1] != 'A' {
		tampered += "A"
	} else {
		tampered += "B"
	}
	unknownKeyFields := slices.Clone(fields)
	unknownKeyFields[1] = "unregistered-key"
	unknownKey := strings.Join(unknownKeyFields, ".")
	deployment := fields[2]
	unregistered, err := env.TestRoutingMarker(deployment, replicarouting.REST, "ghost", page.QueryID, time.Now().Add(time.Minute))
	if err != nil {
		return err
	}
	wrongDeployment, err := env.TestRoutingMarker("other-deployment", replicarouting.REST, "rest-a", page.QueryID, time.Now().Add(time.Minute))
	if err != nil {
		return err
	}
	beforeA, err := ownerContinuationCount(ctx, env, "rest-a")
	if err != nil {
		return err
	}
	beforeB, err := ownerContinuationCount(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	invalid := []struct {
		queryID, marker string
	}{
		{"different-query", page.Slug},
		{page.QueryID, tampered},
		{page.QueryID, unknownKey},
		{page.QueryID, unregistered},
		{page.QueryID, wrongDeployment},
	}
	for _, attempt := range invalid {
		status, _, body, err := sendHTTP(ctx, client, http.MethodGet,
			env.APIURL+continuationPath(attempt.queryID, attempt.marker, page.Token),
			env.Identities[reader].RESTToken, "", "", nil)
		if err != nil {
			return err
		}
		if err := assertUnavailableREST(status, body); err != nil {
			return err
		}
	}
	status, _, body, err := sendHTTP(ctx, client, http.MethodGet,
		env.APIURL+"/internal/replica/drain", env.Identities[reader].RESTToken, "", "", nil)
	if err != nil {
		return err
	}
	if status != http.StatusNotFound || strings.Contains(string(body), "data-fabric-api") {
		return fmt.Errorf("public ingress exposed a private admin endpoint")
	}
	status, _, body, err = sendHTTP(ctx, client, http.MethodGet, env.MCPURL,
		env.Identities[reader].MCPToken, page.Slug, "", nil)
	if err != nil {
		return err
	}
	if status != http.StatusNotFound || strings.Contains(string(body), "data-fabric-mcp") ||
		strings.Contains(string(body), "https://") {
		return fmt.Errorf("wrong-transport marker exposed an MCP owner")
	}
	afterA, err := ownerContinuationCount(ctx, env, "rest-a")
	if err != nil {
		return err
	}
	afterB, err := ownerContinuationCount(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	if beforeA == 0 || beforeB != afterB || beforeA != afterA {
		return fmt.Errorf("invalid markers reached a REST owner")
	}
	return finishThirdRow(ctx, w, page, page.QueryID)
}

func addReferenceIdentity(env *reference.Environment, key, subject, tenant string) error {
	env.Identities[key] = reference.Identity{Subject: subject, Tenant: tenant}
	restToken, err := env.MintREST(key, nil)
	if err != nil {
		return err
	}
	mcpToken, err := env.MintMCP(key, nil)
	if err != nil {
		return err
	}
	identity := env.Identities[key]
	identity.RESTToken, identity.MCPToken = restToken, mcpToken
	env.Identities[key] = identity
	return nil
}

func checkRESTPrincipalBinding(ctx context.Context, env *reference.Environment, client *http.Client) error {
	w := wire{kind: rest, env: env, http: client}
	first, err := readQuery(ctx, w, env.Sources["mysql"], orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, first, "rest-a"); err != nil {
		return err
	}
	if err := addReferenceIdentity(env, "same-subject-tenant-b",
		env.Identities[reader].Subject, "tenant-b"); err != nil {
		return err
	}
	if err := addReferenceIdentity(env, "expired-reader",
		env.Identities[reader].Subject, env.Identities[reader].Tenant); err != nil {
		return err
	}
	expired, err := env.MintREST("expired-reader", func(claims jwt.MapClaims) {
		claims["exp"] = time.Now().Add(-time.Minute).Unix()
	})
	if err != nil {
		return err
	}
	expiredIdentity := env.Identities["expired-reader"]
	expiredIdentity.RESTToken = expired
	env.Identities["expired-reader"] = expiredIdentity

	status, _, _, err := sendHTTP(ctx, client, http.MethodGet,
		env.APIURL+continuationPath(first.QueryID, first.Slug, first.Token), "", "", "", nil)
	if err != nil || status != http.StatusUnauthorized {
		return fmt.Errorf("continuation without a Bearer token was not rejected")
	}
	expiredReply, err := w.next(ctx, "expired-reader", first)
	if err != nil {
		return err
	}
	if !expiredReply.failed || expiredReply.status != http.StatusUnauthorized {
		return fmt.Errorf("expired Bearer token was not rejected")
	}
	for _, actor := range []string{"app-a", "same-subject-tenant-b"} {
		response, err := w.next(ctx, actor, first)
		if err != nil {
			return err
		}
		if !response.failed || (response.status != http.StatusNotFound && response.status != http.StatusForbidden) {
			return fmt.Errorf("another principal advanced a REST continuation")
		}
	}
	shared, err := readQuery(ctx, w, env.Sources["mysql"], orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := expectPage(shared, "", "1", 3, true); err != nil {
		return fmt.Errorf("same subject in tenant-b could not start its authorized query: %w", err)
	}
	if err := checkRESTPageOwner(env, shared, "rest-a"); err != nil {
		return err
	}
	if err := finishThirdRowAs(ctx, w, "same-subject-tenant-b", shared, shared.QueryID); err != nil {
		return err
	}
	next, err := readBatch(w.next(ctx, reader, first))
	if err != nil {
		return err
	}
	if err := expectPage(next, first.QueryID, "2", 3, true); err != nil {
		return err
	}
	return finishThirdRow(ctx, w, next, first.QueryID)
}

func finishThirdRowAs(ctx context.Context, w wire, actor string, page batch, queryID string) error {
	next, err := readBatch(w.next(ctx, actor, page))
	if err != nil {
		return err
	}
	if err := expectPage(next, queryID, "2", 3, true); err != nil {
		return err
	}
	last, err := readBatch(w.next(ctx, actor, next))
	if err != nil {
		return err
	}
	return expectPage(last, queryID, "3", 3, false)
}

func noExposedQueryResult(response reply) bool {
	if !response.failed {
		var page batch
		if response.decode(&page) != nil {
			return false
		}
		return len(page.Entries) == 0 && len(page.Columns) == 0
	}
	return (len(response.data) == 0 || string(response.data) == "null") &&
		(len(response.details) == 0 || string(response.details) == "null" || string(response.details) == "\"\"")
}

func setSourceActive(ctx context.Context, env *reference.Environment, client *http.Client, source reference.Source, active bool) error {
	action := "deactivate"
	if active {
		action = "activate"
	}
	target := env.APIURL + "/api/v1/datasources/" + url.PathEscape(source.ID) + "/" + action
	status, _, body, err := sendHTTP(ctx, client, http.MethodPost, target,
		env.Identities["operator"].RESTToken, "", "", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("synthetic source %s request returned HTTP %d", action, status)
	}
	var response struct {
		Success bool
	}
	if err := json.Unmarshal(body, &response); err != nil || !response.Success {
		return fmt.Errorf("synthetic source %s request was not acknowledged", action)
	}
	return nil
}

func checkSourceRevocation(ctx context.Context, env *reference.Environment, client *http.Client, kind surface) (retErr error) {
	w := (wire{kind: kind, env: env, http: client}).withMCPState()
	source := env.Sources["mysql"]
	first, err := readQuery(ctx, w, source, orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := setSourceActive(ctx, env, client, source, false); err != nil {
		return err
	}
	defer func() {
		restoreErr := setSourceActive(context.Background(), env, client, source, true)
		if restoreErr != nil {
			retErr = errors.Join(retErr, restoreErr)
		}
		w.closeMCPSessions()
	}()

	continuation, callErr := w.next(ctx, reader, first)
	if callErr != nil {
		return fmt.Errorf("revoked-source continuation returned no safe response: %w", callErr)
	}
	if !noExposedQueryResult(continuation) {
		return fmt.Errorf("revoked source returned rows or columns")
	}
	if !continuation.failed {
		return fmt.Errorf("revoked-source continuation succeeded")
	}
	replay, replayErr := w.next(ctx, reader, first)
	if replayErr != nil {
		return replayErr
	}
	if !replay.failed || !noExposedQueryResult(replay) {
		return fmt.Errorf("revoked source query retained a usable continuation")
	}
	if err := setSourceActive(ctx, env, client, source, true); err != nil {
		return err
	}
	fresh, err := readQuery(ctx, w, source, orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	return finishThirdRow(ctx, w, fresh, fresh.QueryID)
}

func checkOperationalEvidence(ctx context.Context, env *reference.Environment, client *http.Client, kind surface) error {
	w := (wire{kind: kind, env: env, http: client}).withMCPState()
	correlation := "df411-o3-correlation"
	page, err := readBatch(w.query(ctx, reader, correlation, env.Sources["mysql"], orderedIDs, nil, 1, 5))
	if err != nil {
		return err
	}
	if err := expectPage(page, "", "1", 3, true); err != nil {
		return err
	}
	if err := finishThirdRow(ctx, w, page, page.QueryID); err != nil {
		return err
	}
	if err := auditEvidence(ctx, w); err != nil {
		return fmt.Errorf("audit correlation evidence: %w", err)
	}
	if err := env.AssertLogsOmit(ctx, env.Identities[reader].RESTToken,
		env.Identities[reader].MCPToken, page.Slug, page.Token, orderedIDs); err != nil {
		return err
	}
	for _, slot := range []string{"rest-a", "rest-b", "mcp-a", "mcp-b"} {
		metrics, err := ownerMetrics(ctx, env, slot)
		if err != nil {
			return err
		}
		if !strings.Contains(metrics, "data_fabric_http_requests_total{") ||
			strings.Contains(metrics, page.Slug) ||
			strings.Contains(metrics, env.Identities[reader].MCPToken) ||
			strings.Contains(metrics, orderedIDs) {
			return fmt.Errorf("%s owner metrics omitted counters or exposed query credentials", slot)
		}
		status, _, _, err := ownerRequest(ctx, env, slot, "app-a", http.MethodGet, "/metrics")
		if err != nil || status != http.StatusForbidden {
			return fmt.Errorf("%s metrics were not admin protected", slot)
		}
	}
	for _, path := range []string{"/metrics", "/internal/replica", "/internal/replica/drain"} {
		status, _, _, err := sendHTTP(ctx, client, http.MethodGet, env.APIURL+path,
			env.Identities["operator"].RESTToken, "", "", nil)
		if err != nil {
			return err
		}
		if status != http.StatusNotFound {
			return fmt.Errorf("public ingress exposed private endpoint %s", path)
		}
	}
	return nil
}
