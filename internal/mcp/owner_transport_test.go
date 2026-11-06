package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/replicarouting"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

var ownerInitializeBody = fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":%q,"capabilities":{},"clientInfo":{"name":"owner-test","version":"1"}}}`, mcpsdk.LATEST_PROTOCOL_VERSION)

const ownerToolBody = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"execute_sql_query","arguments":{"datasource_id":"source-1","sql":"SELECT 1"}}}`

type ownerTestBindingKey struct{}

type ownerTestBinding struct {
	id        string
	expiresAt time.Time
}

type ownerTestQueryService struct {
	stubQueryService
	received []ownerTestBinding
}

func (s *ownerTestQueryService) FetchQuery(ctx context.Context, caller *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	binding, _ := ctx.Value(ownerTestBindingKey{}).(ownerTestBinding)
	s.received = append(s.received, binding)
	return s.stubQueryService.FetchQuery(ctx, caller, req)
}

func newOwnerTestRing(t *testing.T) *replicarouting.KeyRing {
	t.Helper()
	key := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("t", 32)))
	ring, err := replicarouting.ParseKeyRing("test-site", fmt.Sprintf(`{"key":%q}`, key))
	if err != nil {
		t.Fatal(err)
	}
	return ring
}

func newOwnerTestTransport(t *testing.T, ring *replicarouting.KeyRing, ttl time.Duration, capacity int, end func(string), enter func(context.Context) (context.Context, func(), error)) *OwnerTransport {
	t.Helper()
	signer, err := ring.NewOwner("key", replicarouting.MCP, "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	if enter == nil {
		enter = func(ctx context.Context) (context.Context, func(), error) { return ctx, func() {}, nil }
	}
	owner, err := NewOwnerTransport(OwnerTransportOptions{
		Owner: signer, TTL: ttl, MaxSessions: capacity, BeginInitialize: enter,
		BindSession: func(ctx context.Context, id string, expiry time.Time) context.Context {
			return context.WithValue(ctx, ownerTestBindingKey{}, ownerTestBinding{id, expiry})
		},
		EndSession: end,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	return owner
}

func newOwnerTestJWT(t *testing.T) (*security.JWTManager, string, string) {
	t.Helper()
	manager := security.NewJWTManager(strings.Repeat("s", 32), time.Hour)
	tokens := make([]string, 2)
	for i, tenant := range []string{"tenant-a", "tenant-b"} {
		token, err := manager.GenerateToken("alice", tenant, "Alice", nil)
		if err != nil {
			t.Fatal(err)
		}
		tokens[i] = token
	}
	return manager, tokens[0], tokens[1]
}

func ownerHTTPRequest(method, token, body string, sessions ...string) *http.Request {
	r := httptest.NewRequest(method, "/mcp", strings.NewReader(body))
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for _, session := range sessions {
		r.Header.Add(server.HeaderKeySessionID, session)
	}
	return r
}

func ownerHTTPCall(handler http.Handler, method, token, body string, sessions ...string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, ownerHTTPRequest(method, token, body, sessions...))
	return w
}

func initializeTestOwner(t *testing.T, handler http.Handler, token string) string {
	t.Helper()
	response := ownerHTTPCall(handler, http.MethodPost, token, ownerInitializeBody)
	id := response.Header().Get(server.HeaderKeySessionID)
	if response.Code != http.StatusOK || id == "" {
		t.Fatalf("initialize status %d, session header present=%t, body %s", response.Code, id != "", response.Body)
	}
	return id
}

func TestOwnerTransportValidatesPrincipalAndBootBeforeDispatch(t *testing.T) {
	ring := newOwnerTestRing(t)
	jwt, alice, otherTenant := newOwnerTestJWT(t)
	query := &ownerTestQueryService{}
	mcpServer := NewMCPServer(query, mcpHTTPRepository{}, nil)
	owner := newOwnerTestTransport(t, ring, time.Hour, 4, func(string) {}, nil)
	handler := mcpServer.StreamableHTTPHandlerWithOwner(jwt, nil, RemoteLimits{}, owner)

	first := initializeTestOwner(t, handler, alice)
	second := initializeTestOwner(t, handler, alice)
	if first == second {
		t.Fatal("two initializations received the same transport session")
	}
	if slot, err := ring.Verify(first, replicarouting.MCP, "", time.Now()); err != nil || slot != "owner-a" {
		t.Fatalf("issued session marker does not route to its owner: %q %v", slot, err)
	}
	for _, tc := range []struct {
		name, method, token, body string
		headers                   []string
		want                      int
	}{
		{"missing bearer", http.MethodPost, "", ownerToolBody, []string{first}, http.StatusUnauthorized},
		{"cross-tenant POST", http.MethodPost, otherTenant, ownerToolBody, []string{first}, http.StatusNotFound},
		{"cross-tenant GET", http.MethodGet, otherTenant, "", []string{first}, http.StatusNotFound},
		{"cross-tenant DELETE", http.MethodDelete, otherTenant, "", []string{first}, http.StatusNotFound},
		{"duplicate headers", http.MethodPost, alice, ownerToolBody, []string{first, first}, http.StatusNotFound},
		{"forged header", http.MethodPost, alice, ownerToolBody, []string{first + "a"}, http.StatusNotFound},
		{"headerless tool", http.MethodPost, alice, ownerToolBody, nil, http.StatusBadRequest},
		{"headerless GET", http.MethodGet, alice, "", nil, http.StatusBadRequest},
		{"headerless DELETE", http.MethodDelete, alice, "", nil, http.StatusBadRequest},
		{"initialization with a session", http.MethodPost, alice, ownerInitializeBody, []string{first}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := ownerHTTPCall(handler, tc.method, tc.token, tc.body, tc.headers...)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %s", w.Code, tc.want, w.Body)
			}
		})
	}
	replacement := newOwnerTestTransport(t, ring, time.Hour, 4, func(string) {}, nil)
	replacementHandler := mcpServer.StreamableHTTPHandlerWithOwner(jwt, nil, RemoteLimits{}, replacement)
	if got := ownerHTTPCall(replacementHandler, http.MethodPost, alice, ownerToolBody, first).Code; got != http.StatusNotFound {
		t.Fatalf("replacement accepted old boot: status %d", got)
	}
	if len(query.received) != 0 {
		t.Fatal("rejected requests reached the query service")
	}
	for i, id := range []string{first, second} {
		w := ownerHTTPCall(handler, http.MethodPost, alice, ownerToolBody, id)
		if w.Code != http.StatusOK || len(query.received) != i+1 {
			t.Fatalf("authorized request status %d, query count %d, body %s", w.Code, len(query.received), w.Body)
		}
		binding := query.received[i]
		if binding.id != id || !time.Now().Before(binding.expiresAt) || binding.expiresAt.Nanosecond() != 0 {
			t.Fatalf("tool got the wrong transport binding or expiry")
		}
	}
}

func waitForOwnerSlots(t *testing.T, owner *OwnerTransport, want int) {
	t.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		owner.mu.Lock()
		used := owner.used
		owner.mu.Unlock()
		if used == want {
			return
		}
		select {
		case <-timeout.C:
			t.Fatalf("transport slots = %d, want %d", used, want)
		case <-ticker.C:
		}
	}
}

func TestOwnerTransportCapacityAndFailedInitialization(t *testing.T) {
	jwt, token, _ := newOwnerTestJWT(t)
	ring := newOwnerTestRing(t)
	owner := newOwnerTestTransport(t, ring, time.Hour, 1, func(string) {}, nil)
	mcpServer := NewMCPServer(&stubQueryService{}, mcpHTTPRepository{}, nil)
	handler := mcpServer.StreamableHTTPHandlerWithOwner(jwt, nil, RemoteLimits{}, owner)
	badInit := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":[]}}`
	ownerHTTPCall(handler, http.MethodPost, token, badInit)
	waitForOwnerSlots(t, owner, 0)
	id := initializeTestOwner(t, handler, token)
	if got := ownerHTTPCall(handler, http.MethodPost, token, ownerInitializeBody).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("full registry initialization got %d, want 503", got)
	}
	if got := ownerHTTPCall(handler, http.MethodDelete, token, "", id).Code; got != http.StatusOK {
		t.Fatalf("valid delete got %d, want 200", got)
	}
	if got := ownerHTTPCall(handler, http.MethodDelete, token, "", id).Code; got != http.StatusNotFound {
		t.Fatalf("repeated delete got %d, want 404", got)
	}
	waitForOwnerSlots(t, owner, 0)
	initializeTestOwner(t, handler, token)
}

type ownerListeningRecorder struct {
	*httptest.ResponseRecorder
	started chan struct{}
}

func (w ownerListeningRecorder) WriteHeader(code int) {
	w.ResponseRecorder.WriteHeader(code)
	close(w.started)
}

func TestOwnerTransportRetirementCancelsSSEAndReleasesQueries(t *testing.T) {
	for _, action := range []string{"delete", "expiry", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			jwt, token, _ := newOwnerTestJWT(t)
			ended := make(chan string, 2)
			ttl := time.Hour
			if action == "expiry" {
				ttl = 2 * time.Second
			}
			owner := newOwnerTestTransport(t, newOwnerTestRing(t), ttl, 1, func(id string) { ended <- id }, nil)
			mcpServer := NewMCPServer(&stubQueryService{}, mcpHTTPRepository{}, nil)
			handler := mcpServer.StreamableHTTPHandlerWithOwner(jwt, nil, RemoteLimits{}, owner)
			id := initializeTestOwner(t, handler, token)
			req := ownerHTTPRequest(http.MethodGet, token, "", id)
			ctx, cancel := context.WithCancel(req.Context())
			defer cancel()
			w := ownerListeningRecorder{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{})}
			done := make(chan struct{})
			go func() {
				defer close(done)
				handler.ServeHTTP(w, req.WithContext(ctx))
			}()
			select {
			case <-w.started:
			case <-time.After(3 * time.Second):
				t.Fatal("SSE listener did not start")
			}
			switch action {
			case "delete":
				if got := ownerHTTPCall(handler, http.MethodDelete, token, "", id).Code; got != http.StatusOK {
					t.Fatalf("delete got %d, want 200", got)
				}
			case "shutdown":
				go owner.Close()
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("retired transport left its SSE listener running")
			}
			if w.Code != http.StatusOK {
				t.Fatalf("SSE listener was not accepted, status %d", w.Code)
			}
			select {
			case cleaned := <-ended:
				if cleaned != id {
					t.Fatal("cleanup received the wrong session")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("retired transport did not release query resources")
			}
			waitForOwnerSlots(t, owner, 0)
			if got := ownerHTTPCall(handler, http.MethodPost, token, ownerToolBody, id).Code; got != http.StatusNotFound {
				t.Fatalf("retired session still accepted requests: status %d", got)
			}
		})
	}
}

func TestOwnerTransportRejectsInitializationWhenDraining(t *testing.T) {
	jwt, token, _ := newOwnerTestJWT(t)
	owner := newOwnerTestTransport(t, newOwnerTestRing(t), time.Hour, 1, func(string) {}, func(context.Context) (context.Context, func(), error) {
		return nil, nil, context.Canceled
	})
	mcpServer := NewMCPServer(&stubQueryService{}, mcpHTTPRepository{}, nil)
	handler := mcpServer.StreamableHTTPHandlerWithOwner(jwt, nil, RemoteLimits{}, owner)
	w := ownerHTTPCall(handler, http.MethodPost, token, ownerInitializeBody)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get(server.HeaderKeySessionID) != "" {
		t.Fatalf("draining initialization got status %d or a session header", w.Code)
	}
	waitForOwnerSlots(t, owner, 0)
}

func newOwnerTestTransportForSlot(t *testing.T, ring *replicarouting.KeyRing, slot string, ttl time.Duration, capacity int, end func(string), enter func(context.Context) (context.Context, func(), error)) *OwnerTransport {
	t.Helper()
	signer, err := ring.NewOwner("key", replicarouting.MCP, slot)
	if err != nil {
		t.Fatal(err)
	}
	if enter == nil {
		enter = func(ctx context.Context) (context.Context, func(), error) { return ctx, func() {}, nil }
	}
	owner, err := NewOwnerTransport(OwnerTransportOptions{
		Owner: signer, TTL: ttl, MaxSessions: capacity, BeginInitialize: enter,
		BindSession: func(ctx context.Context, id string, expiry time.Time) context.Context {
			return context.WithValue(ctx, ownerTestBindingKey{}, ownerTestBinding{id, expiry})
		},
		EndSession: end,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	return owner
}

func TestMCPIngressKeepsSessionsOnTheirOwnerAfterReload(t *testing.T) {
	ring := newOwnerTestRing(t)
	jwt, token, _ := newOwnerTestJWT(t)
	queryA := &ownerTestQueryService{}
	queryB := &ownerTestQueryService{}
	ownerA := newOwnerTestTransportForSlot(t, ring, "owner-a", time.Hour, 4, func(string) {}, nil)
	ownerB := newOwnerTestTransportForSlot(t, ring, "owner-b", time.Hour, 4, func(string) {}, nil)
	serverA := httptest.NewServer(NewMCPServer(queryA, mcpHTTPRepository{}, nil).StreamableHTTPHandlerWithOwner(jwt, nil, RemoteLimits{}, ownerA))
	serverB := httptest.NewServer(NewMCPServer(queryB, mcpHTTPRepository{}, nil).StreamableHTTPHandlerWithOwner(jwt, nil, RemoteLimits{}, ownerB))
	t.Cleanup(serverA.Close)
	t.Cleanup(serverB.Close)

	cfg := replicarouting.Upstreams{MCP: map[string]replicarouting.Upstream{
		"owner-a": {URL: serverA.URL, AcceptNew: true},
		"owner-b": {URL: serverB.URL},
	}}
	ingress, err := replicarouting.NewIngress(ring, cfg)
	if err != nil {
		t.Fatal(err)
	}
	public := httptest.NewServer(ingress)
	t.Cleanup(public.Close)
	conn, loopbackErr := net.DialTimeout("tcp", public.Listener.Addr().String(), time.Second)
	if loopbackErr != nil {
		t.Skipf("local TCP loopback is unavailable: %v", loopbackErr)
	}
	_ = conn.Close()
	client := public.Client()
	client.Timeout = 5 * time.Second
	send := func(method, body, session string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, public.URL+"/mcp", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		if session != "" {
			req.Header.Set(server.HeaderKeySessionID, session)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s request through ingress: %v", method, err)
		}
		return resp
	}
	initialize := func() string {
		t.Helper()
		resp := send(http.MethodPost, ownerInitializeBody, "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("initialize status %d", resp.StatusCode)
		}
		id := resp.Header.Get(server.HeaderKeySessionID)
		if id == "" {
			t.Fatal("initialize response omitted the MCP session header")
		}
		return id
	}
	call := func(method, body, session string, want int) {
		t.Helper()
		resp := send(method, body, session)
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s status = %d, want %d", method, resp.StatusCode, want)
		}
		if method != http.MethodGet {
			_, _ = io.Copy(io.Discard, resp.Body)
		}
	}

	sessionA := initialize()
	if slot, err := ring.Verify(sessionA, replicarouting.MCP, "", time.Now()); err != nil || slot != "owner-a" {
		t.Fatalf("first session owner = %q, %v; want owner-a", slot, err)
	}
	cfg.MCP["owner-a"] = replicarouting.Upstream{URL: serverA.URL}
	cfg.MCP["owner-b"] = replicarouting.Upstream{URL: serverB.URL, AcceptNew: true}
	if err := ingress.Reload(cfg); err != nil {
		t.Fatal(err)
	}

	call(http.MethodPost, ownerToolBody, sessionA, http.StatusOK)
	if len(queryA.received) != 1 || len(queryB.received) != 0 {
		t.Fatalf("existing session tool calls reached owner A/B = %d/%d, want 1/0", len(queryA.received), len(queryB.received))
	}
	call(http.MethodGet, "", sessionA, http.StatusOK)
	call(http.MethodDelete, "", sessionA, http.StatusOK)
	call(http.MethodPost, ownerToolBody, sessionA, http.StatusNotFound)
	waitForOwnerSlots(t, ownerA, 0)

	sessionB := initialize()
	if slot, err := ring.Verify(sessionB, replicarouting.MCP, "", time.Now()); err != nil || slot != "owner-b" {
		t.Fatalf("new session owner = %q, %v; want owner-b", slot, err)
	}
	call(http.MethodPost, ownerToolBody, sessionB, http.StatusOK)
	if len(queryB.received) != 1 {
		t.Fatalf("new session tool calls reached owner B %d times, want once", len(queryB.received))
	}
}
