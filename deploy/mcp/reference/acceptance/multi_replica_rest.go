package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
)

const restContinuationRoute = "/api/v1/fetch/:query_id/:slug/:token"

func checkRESTConcurrentReplay(ctx context.Context, env *reference.Environment, client *http.Client) error {
	w := wire{kind: rest, env: env, http: client}
	source := env.Sources["mysql"]
	first, err := readQuery(ctx, w, source, orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, first, "rest-b"); err != nil {
		return err
	}
	if err := expectPage(first, "", "1", 3, true); err != nil {
		return err
	}
	type attempt struct {
		response reply
		err      error
	}
	start := make(chan struct{})
	done := make(chan attempt, 2)
	for range 2 {
		go func() {
			<-start
			response, callErr := w.next(ctx, reader, first)
			done <- attempt{response: response, err: callErr}
		}()
	}
	close(start)
	a, b := <-done, <-done
	if err := errors.Join(a.err, b.err); err != nil {
		return err
	}
	if a.response.failed == b.response.failed {
		return fmt.Errorf("same-token concurrent REST fetch did not produce exactly one winner")
	}
	winner, loser := a, b
	if winner.response.failed {
		winner, loser = loser, winner
	}
	if err := loser.response.expectError(rest, http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", sessionError); err != nil {
		return err
	}
	replay, err := w.next(ctx, reader, first)
	if err != nil {
		return err
	}
	if err := replay.expectError(rest, http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", sessionError); err != nil {
		return err
	}
	second, err := readBatch(winner.response, nil)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, second, "rest-b"); err != nil {
		return err
	}
	if err := expectPage(second, first.QueryID, "2", 3, true); err != nil {
		return err
	}
	third, err := readBatch(w.next(ctx, reader, second))
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, third, "rest-b"); err != nil {
		return err
	}
	return expectPage(third, first.QueryID, "3", 3, false)
}

type dropContinuationResponse struct {
	base    http.RoundTripper
	dropped bool
}

func (t *dropContinuationResponse) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil || response == nil || response.StatusCode != http.StatusOK ||
		request.Method != http.MethodGet || !strings.HasPrefix(request.URL.Path, "/api/v1/fetch/") {
		return response, err
	}
	if t.dropped {
		return response, err
	}
	t.dropped = true
	_ = response.Body.Close()
	return nil, errors.New("injected response loss after owner advanced")
}

func checkRESTLostResponse(ctx context.Context, env *reference.Environment, client *http.Client) error {
	w := wire{kind: rest, env: env, http: client}
	source := env.Sources["mysql"]
	first, err := readQuery(ctx, w, source, orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, first, "rest-b"); err != nil {
		return err
	}
	before, err := ownerMetrics(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	drop := &dropContinuationResponse{base: client.Transport}
	dropClient := *client
	dropClient.Transport = drop
	dropWire := w
	dropWire.http = &dropClient
	if _, err := dropWire.next(ctx, reader, first); err == nil || !drop.dropped {
		return fmt.Errorf("continuation response loss was not injected after the owner replied")
	}
	afterDrop, err := ownerMetrics(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	advanced := metricValue(afterDrop, "data_fabric_http_requests_total", restContinuationRoute) -
		metricValue(before, "data_fabric_http_requests_total", restContinuationRoute)
	if advanced != 1 {
		return fmt.Errorf("lost response caused %v owner continuation requests, want exactly one", advanced)
	}
	replay, err := w.next(ctx, reader, first)
	if err != nil {
		return err
	}
	if err := replay.expectError(rest, http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", sessionError); err != nil {
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
	return finishThirdRow(ctx, w, fresh, fresh.QueryID)
}

func rawRESTFetchPath(queryID, slug, token string) string {
	return "/api/v1/fetch/" + url.PathEscape(queryID) + "/" + url.PathEscape(slug) + "/" + url.PathEscape(token) + "?batch_size=1"
}

func checkRESTMarkerRejection(ctx context.Context, env *reference.Environment, client *http.Client) error {
	w := wire{kind: rest, env: env, http: client}
	source := env.Sources["mysql"]
	first, err := readQuery(ctx, w, source, orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	second, err := readQuery(ctx, w, source, orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	for _, page := range []batch{first, second} {
		if err := checkRESTPageOwner(env, page, "rest-b"); err != nil {
			return err
		}
	}
	fields := strings.Split(first.Slug, ".")
	if len(fields) != 9 {
		return fmt.Errorf("signed REST owner marker has an unexpected shape")
	}
	tampered := func(field int, value string) string {
		changed := append([]string(nil), fields...)
		changed[field] = value
		return strings.Join(changed, ".")
	}
	badSignature := first.Slug
	if badSignature[len(badSignature)-1] == 'A' {
		badSignature = badSignature[:len(badSignature)-1] + "B"
	} else {
		badSignature = badSignature[:len(badSignature)-1] + "A"
	}
	unregistered, err := env.MintAcceptanceMarker(
		env.RoutingDeploymentID(), "reference", "rest", "rest-unregistered",
		"unregistered-query", time.Now().Add(time.Minute),
	)
	if err != nil {
		return err
	}
	probes := []struct{ name, queryID, slug string }{
		{"swapped query ID", second.QueryID, first.Slug},
		{"swapped slug", first.QueryID, second.Slug},
		{"altered signature", first.QueryID, badSignature},
		{"unknown key ID", first.QueryID, tampered(1, "missing-key")},
		{"wrong deployment scope", first.QueryID, tampered(2, "other-deployment")},
		{"wrong transport scope", first.QueryID, tampered(3, "mcp")},
		{"unregistered owner slot", "unregistered-query", unregistered},
	}
	before, err := ownerMetrics(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	for _, probe := range probes {
		token := first.Token
		if probe.name == "unregistered owner slot" {
			token = "opaque-token"
		}
		result, err := w.restCall(ctx, reader, "", http.MethodGet,
			rawRESTFetchPath(probe.queryID, probe.slug, token), nil)
		if err != nil {
			return err
		}
		if err := result.expectError(rest, http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", "query session is unavailable"); err != nil {
			return fmt.Errorf("%s: %w", probe.name, err)
		}
		if strings.Contains(string(result.data)+result.message+string(result.details), "http://") {
			return fmt.Errorf("%s disclosed a private upstream address", probe.name)
		}
	}
	after, err := ownerMetrics(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	if delta := metricValue(after, "data_fabric_http_requests_total", restContinuationRoute) -
		metricValue(before, "data_fabric_http_requests_total", restContinuationRoute); delta != 0 {
		return fmt.Errorf("invalid owner markers reached REST owner %d times", int(delta))
	}
	if err := finishThirdRow(ctx, w, first, first.QueryID); err != nil {
		return err
	}
	return finishThirdRow(ctx, w, second, second.QueryID)
}

func restReplyWithToken(ctx context.Context, w wire, token, method, path string, body any) (reply, error) {
	response, err := w.request(ctx, method, w.env.APIURL+path, token, "", body)
	if err != nil {
		return reply{}, err
	}
	defer response.Body.Close()
	var envelope struct {
		Success bool
		Data    json.RawMessage
		Error   struct {
			Code    string
			Message string
			Details json.RawMessage
		}
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponse)).Decode(&envelope); err != nil {
		return reply{}, fmt.Errorf("invalid REST response envelope (HTTP %d)", response.StatusCode)
	}
	return reply{
		data: envelope.Data, status: response.StatusCode, code: envelope.Error.Code,
		message: envelope.Error.Message, details: envelope.Error.Details,
		failed: !envelope.Success || response.StatusCode != http.StatusOK,
	}, nil
}

func restNextWithToken(ctx context.Context, w wire, token string, previous batch) (reply, error) {
	path, err := url.Parse(previous.NextURI)
	if err != nil || path.IsAbs() || path.Host != "" || path.Fragment != "" ||
		!strings.HasPrefix(path.Path, "/api/v1/fetch/") {
		return reply{}, fmt.Errorf("invalid relative continuation URI")
	}
	query := path.Query()
	query.Set("batch_size", "1")
	path.RawQuery = query.Encode()
	return restReplyWithToken(ctx, w, token, http.MethodGet, path.String(), nil)
}

func checkRESTPrincipalBindingExtended(ctx context.Context, env *reference.Environment, client *http.Client) error {
	if err := env.EnsureTenantBSource(ctx, "mysql"); err != nil {
		return err
	}
	w := wire{kind: rest, env: env, http: client}
	source := env.Sources["mysql"]
	firstResult, err := w.query(ctx, "app-a", "", source, orderedIDs, nil, 1, 5)
	if err != nil {
		return err
	}
	first, err := readBatch(firstResult, nil)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, first, "rest-b"); err != nil {
		return err
	}
	otherUser, err := w.next(ctx, "app-b", first)
	if err != nil {
		return err
	}
	if err := otherUser.expectError(rest, http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", sessionError); err != nil {
		return fmt.Errorf("same-tenant user advanced another user's cursor: %w", err)
	}
	tenantBToken := env.Identities["app-a-tenant-b"].RESTToken
	otherTenant, err := restNextWithToken(ctx, w, tenantBToken, first)
	if err != nil {
		return err
	}
	if err := otherTenant.expectError(rest, http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", sessionError); err != nil {
		return fmt.Errorf("same subject in another tenant advanced the cursor: %w", err)
	}
	missing, err := restNextWithToken(ctx, w, "", first)
	if err != nil {
		return err
	}
	if !missing.failed || missing.status != http.StatusUnauthorized {
		return fmt.Errorf("missing REST bearer returned HTTP %d, want 401", missing.status)
	}
	expiredToken, err := env.MintREST("app-a", func(claims jwt.MapClaims) {
		claims["exp"] = time.Now().Add(-time.Minute).Unix()
	})
	if err != nil {
		return err
	}
	expired, err := restNextWithToken(ctx, w, expiredToken, first)
	if err != nil {
		return err
	}
	if !expired.failed || expired.status != http.StatusUnauthorized {
		return fmt.Errorf("expired REST bearer returned HTTP %d, want 401", expired.status)
	}
	next, err := readBatch(w.next(ctx, "app-a", first))
	if err != nil {
		return fmt.Errorf("creator could not continue after rejected principals: %w", err)
	}
	if err := checkRESTPageOwner(env, next, "rest-b"); err != nil {
		return err
	}
	if err := expectPage(next, first.QueryID, "2", 3, true); err != nil {
		return err
	}
	creatorLast, err := readBatch(w.next(ctx, "app-a", next))
	if err != nil {
		return fmt.Errorf("creator could not finish after rejected principals: %w", err)
	}
	if err := expectPage(creatorLast, first.QueryID, "3", 3, false); err != nil {
		return err
	}

	tenantBSource, ok := env.TenantBSources["mysql"]
	if !ok {
		return fmt.Errorf("tenant-b deterministic source was not registered")
	}
	tenantBReply, err := restReplyWithToken(ctx, w, tenantBToken, http.MethodPost, "/api/v1/fetch", map[string]any{
		"dataSourceId": tenantBSource.ID, "sql": orderedIDs, "type": 2, "batch_size": 1, "timeout": 5,
	})
	if err != nil {
		return err
	}
	tenantBFirst, err := readBatch(tenantBReply, nil)
	if err != nil {
		return fmt.Errorf("same subject could not start tenant-b query: %w", err)
	}
	if err := checkRESTPageOwner(env, tenantBFirst, "rest-b"); err != nil {
		return err
	}
	if err := expectPage(tenantBFirst, "", "1", 3, true); err != nil {
		return err
	}
	tenantBNext, err := readBatch(restNextWithToken(ctx, w, tenantBToken, tenantBFirst))
	if err != nil {
		return err
	}
	if err := expectPage(tenantBNext, tenantBFirst.QueryID, "2", 3, true); err != nil {
		return err
	}
	tenantBLast, err := readBatch(restNextWithToken(ctx, w, tenantBToken, tenantBNext))
	if err != nil {
		return err
	}
	return expectPage(tenantBLast, tenantBFirst.QueryID, "3", 3, false)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func waitOwnerPendingAtMost(ctx context.Context, env *reference.Environment, slot string, maximum int, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := readOwnerStatus(ctx, env, slot)
		if err == nil && status.PendingSessions <= maximum {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if err != nil {
				return err
			}
			return fmt.Errorf("%s owner retained %d sessions after cleanup bound", slot, status.PendingSessions)
		case <-ticker.C:
		}
	}
}

func checkRESTAbsoluteExpiry(ctx context.Context, env *reference.Environment, client *http.Client) error {
	w := wire{kind: rest, env: env, http: client}
	source := env.Sources["mysql"]
	before, err := readOwnerStatus(ctx, env, "rest-b")
	if err != nil {
		return err
	}
	first, err := readQuery(ctx, w, source, orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, first, "rest-b"); err != nil {
		return err
	}
	if err := sleepContext(ctx, time.Duration(SessionTTLSeconds-1)*time.Second); err != nil {
		return err
	}
	second, err := readBatch(w.next(ctx, reader, first))
	if err != nil {
		return fmt.Errorf("continuation failed before absolute expiry: %w", err)
	}
	if err := checkRESTPageOwner(env, second, "rest-b"); err != nil {
		return err
	}
	if err := expectPage(second, first.QueryID, "2", 3, true); err != nil {
		return err
	}
	if err := sleepContext(ctx, 2*time.Second); err != nil {
		return err
	}
	expired, err := w.next(ctx, reader, second)
	if err != nil {
		return err
	}
	if err := expired.expectError(rest, http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", "query session is unavailable"); err != nil {
		return fmt.Errorf("query lifetime was extended by batch fetch: %w", err)
	}
	if err := waitOwnerPendingAtMost(ctx, env, "rest-b", before.PendingSessions, 3*time.Second); err != nil {
		return fmt.Errorf("expired cursor was not retired within cleanup bound: %w", err)
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
	return finishThirdRow(ctx, w, fresh, fresh.QueryID)
}
