package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/RhinoLink-Tech/data-fabric/internal/middleware"
	"github.com/RhinoLink-Tech/data-fabric/internal/replicarouting"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
)

// OwnerTransportOptions wires owner routing to the local query lifecycle.
type OwnerTransportOptions struct {
	Owner           *replicarouting.Owner
	TTL             time.Duration
	MaxSessions     int
	BeginInitialize func(context.Context) (context.Context, func(), error)
	BindSession     func(context.Context, string, time.Time) context.Context
	EndSession      func(string)
}

type transportPrincipal struct {
	tenantID string
	userID   string
}

type transportSession struct {
	id        string
	principal transportPrincipal
	expiresAt time.Time
	lifetime  context.Context
	cancel    context.CancelFunc
	timer     *time.Timer
	ready     bool
	requests  sync.WaitGroup
}

// OwnerTransport owns a bounded, process-local registry. A slot stays charged
// until active requests exit and their query resources are released.
type OwnerTransport struct {
	opts     OwnerTransportOptions
	mu       sync.Mutex
	sessions map[string]*transportSession
	used     int
	closed   bool
	open     sync.WaitGroup
}

func NewOwnerTransport(opts OwnerTransportOptions) (*OwnerTransport, error) {
	if opts.Owner == nil || opts.TTL < time.Second || opts.MaxSessions < 1 {
		return nil, errors.New("MCP owner transport requires a signer, TTL of at least one second, and positive session capacity")
	}
	if opts.BeginInitialize == nil || opts.BindSession == nil || opts.EndSession == nil {
		return nil, errors.New("MCP owner transport requires initialization, query binding, and cleanup hooks")
	}
	return &OwnerTransport{opts: opts, sessions: make(map[string]*transportSession)}, nil
}

func (t *OwnerTransport) reserve(principal transportPrincipal) (*transportSession, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.used >= t.opts.MaxSessions {
		return nil, errors.New("MCP session capacity unavailable")
	}

	// Use the signed marker's integer-second expiry in the registry and the
	// query binding so all three checks agree on the final valid instant.
	expiry := time.Now().Add(t.opts.TTL).Truncate(time.Second)
	id, err := t.opts.Owner.Mark(strings.ToLower(rand.Text()), expiry)
	if err != nil {
		return nil, err
	}
	if t.sessions[id] != nil {
		return nil, errors.New("duplicate MCP session")
	}
	lifetime, cancel := context.WithCancel(context.Background())
	s := &transportSession{id: id, principal: principal, expiresAt: expiry, lifetime: lifetime, cancel: cancel}
	s.requests.Add(1)
	t.open.Add(1)
	t.used++
	t.sessions[id] = s
	s.timer = time.AfterFunc(time.Until(expiry), func() { t.end(s) })
	return s, nil
}

func (t *OwnerTransport) acquire(id string, principal transportPrincipal, now time.Time) *transportSession {
	if t.opts.Owner.Verify(id, "", now) != nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.sessions[id]
	if s == nil || !s.ready || s.principal != principal || !time.Now().Before(s.expiresAt) {
		return nil
	}
	s.requests.Add(1)
	return s
}

func (t *OwnerTransport) isReady(s *transportSession) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessions[s.id] == s && s.ready && time.Now().Before(s.expiresAt)
}

func (t *OwnerTransport) end(s *transportSession) {
	t.mu.Lock()
	if t.sessions[s.id] != s {
		t.mu.Unlock()
		return
	}
	delete(t.sessions, s.id)
	t.mu.Unlock()
	t.cancelAndRelease(s)
}

func (t *OwnerTransport) cancelAndRelease(s *transportSession) {
	s.timer.Stop()
	s.cancel()
	go func() {
		s.requests.Wait()
		if s.ready {
			t.opts.EndSession(s.id)
		}
		t.mu.Lock()
		t.used--
		t.mu.Unlock()
		t.open.Done()
	}()
}

// Close stops accepting sessions and cancels requests, including SSE listeners,
// before waiting for each session's query cleanup.
func (t *OwnerTransport) LiveSessionCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.used
}

func (t *OwnerTransport) Close() {
	t.mu.Lock()
	t.closed = true
	sessions := t.sessions
	t.sessions = nil
	t.mu.Unlock()
	for _, s := range sessions {
		t.cancelAndRelease(s)
	}
	t.open.Wait()
}

type ownerRequestKey struct{}

type ownerRequest struct {
	transport    *OwnerTransport
	session      *transportSession
	initializing bool
}

// markOwnerInitialized runs only after the SDK handles initialization. Failed
// requests release their reserved slot without publishing a session.
func markOwnerInitialized(ctx context.Context, _ any, _ *mcpsdk.InitializeRequest, _ *mcpsdk.InitializeResult) {
	request, ok := ctx.Value(ownerRequestKey{}).(ownerRequest)
	if !ok || !request.initializing {
		return
	}
	t, s := request.transport, request.session
	t.mu.Lock()
	if t.sessions[s.id] == s && time.Now().Before(s.expiresAt) {
		s.ready = true
	}
	t.mu.Unlock()
}

func (t *OwnerTransport) ResolveSessionIdManager(r *http.Request) server.SessionIdManager {
	request, _ := r.Context().Value(ownerRequestKey{}).(ownerRequest)
	return request
}

func (r ownerRequest) Generate() string {
	if !r.initializing || r.session == nil {
		return ""
	}
	return r.session.id
}

func (r ownerRequest) Validate(id string) (bool, error) {
	if r.session == nil || r.session.id != id || !r.transport.isReady(r.session) {
		return true, nil
	}
	return false, nil
}

func (r ownerRequest) Terminate(id string) (bool, error) {
	if r.session == nil || r.session.id != id {
		return true, nil
	}
	r.transport.end(r.session)
	return false, nil
}

// StreamableHTTPHandlerWithOwner enables owner-bound session headers after
// the usual remote request limits and Bearer authentication.
func (m *MCPServer) StreamableHTTPHandlerWithOwner(tokenValidator MCPTokenValidator, rateLimiter *middleware.RateLimiter, limits RemoteLimits, owner *OwnerTransport) http.Handler {
	if owner == nil {
		return m.StreamableHTTPHandlerWithLimits(tokenValidator, rateLimiter, limits)
	}
	transport := server.NewStreamableHTTPServer(m.server,
		server.WithSessionIdManagerResolver(owner), server.WithLogger(ownerSDKLogger{}))
	mux := http.NewServeMux()
	mux.Handle("/mcp", authenticateMCPRequests(tokenValidator, owner.wrap(transport), m.auditRecorder))
	return withRemoteLimits(rateLimiter, limits, mux)
}

// The SDK formats session IDs and protocol payloads into transport logs.
// Preserve the failure signal and keep signed session headers out of logs.
type ownerSDKLogger struct{}

func (ownerSDKLogger) Infof(string, ...any)  {}
func (ownerSDKLogger) Errorf(string, ...any) { slog.Error("routed MCP transport error") }

func (t *OwnerTransport) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, _ := r.Context().Value(callerContextKey{}).(*security.Caller)
		if caller == nil || caller.UserID == "" {
			writeMCPUnauthorized(w)
			return
		}
		principal := transportPrincipal{tenantID: caller.TenantID, userID: caller.UserID}
		headers := r.Header.Values(server.HeaderKeySessionID)
		if len(headers) > 1 || (len(headers) == 1 && headers[0] == "") {
			writeMCPUnavailable(w)
			return
		}

		if r.Method == http.MethodPost {
			initializing, err := readOwnerInitializeMethod(r)
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					writeRemoteError(w, http.StatusRequestEntityTooLarge, "request_too_large", "MCP request exceeds the body limit")
				} else {
					writeRemoteError(w, http.StatusBadRequest, "invalid_request", "MCP request must contain a JSON-RPC message")
				}
				return
			}
			if initializing {
				if len(headers) != 0 {
					writeRemoteError(w, http.StatusBadRequest, "invalid_request", "MCP initialization cannot include a session header")
					return
				}
				t.serveInitialize(w, r, next, principal)
				return
			}
		} else if r.Method != http.MethodGet && r.Method != http.MethodDelete {
			w.Header().Set("Allow", "GET, POST, DELETE")
			writeRemoteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "MCP method is not supported")
			return
		}
		if len(headers) == 0 {
			writeRemoteError(w, http.StatusBadRequest, "session_required", "MCP session header required")
			return
		}

		s := t.acquire(headers[0], principal, time.Now())
		if s == nil {
			writeMCPUnavailable(w)
			return
		}
		defer s.requests.Done()
		t.serveSession(w, r, next, s, false)
	})
}

func (t *OwnerTransport) serveInitialize(w http.ResponseWriter, r *http.Request, next http.Handler, principal transportPrincipal) {
	ctx, finish, err := t.opts.BeginInitialize(r.Context())
	if err != nil {
		writeRemoteError(w, http.StatusServiceUnavailable, "owner_draining", "This replica cannot accept new MCP sessions")
		return
	}
	defer finish()
	s, err := t.reserve(principal)
	if err != nil {
		writeRemoteError(w, http.StatusServiceUnavailable, "session_capacity", "MCP session capacity unavailable")
		return
	}
	defer s.requests.Done()
	defer func() {
		if ctx.Err() != nil || !t.isReady(s) || w.Header().Get(server.HeaderKeySessionID) != s.id {
			t.end(s)
		}
	}()
	t.serveSession(w, r.WithContext(ctx), next, s, true)
}

func (t *OwnerTransport) serveSession(w http.ResponseWriter, r *http.Request, next http.Handler, s *transportSession, initializing bool) {
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(s.lifetime, cancel)
	defer stop()
	defer cancel()
	if s.lifetime.Err() != nil || ctx.Err() != nil || !time.Now().Before(s.expiresAt) {
		writeMCPUnavailable(w)
		return
	}
	ctx = context.WithValue(ctx, ownerRequestKey{}, ownerRequest{transport: t, session: s, initializing: initializing})
	if !initializing {
		ctx = t.opts.BindSession(ctx, s.id, s.expiresAt)
	}
	next.ServeHTTP(w, r.WithContext(ctx))
}

func readOwnerInitializeMethod(r *http.Request) (bool, error) {
	// The remote body limit is already installed. Inspect only the envelope
	// and replay its bytes so the SDK still owns JSON-RPC decoding.
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		return false, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var envelope struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false, err
	}
	return envelope.Method == string(mcpsdk.MethodInitialize), nil
}

func writeMCPUnavailable(w http.ResponseWriter) {
	writeRemoteError(w, http.StatusNotFound, "session_unavailable", "MCP session unavailable. Initialize again.")
}

// StreamableHTTPHandlerWithOwnerAdmin adds the private owner status and drain
// routes for routed remote MCP. The public ingress only forwards /mcp.
func (m *MCPServer) StreamableHTTPHandlerWithOwnerAdmin(tokenValidator MCPTokenValidator, rateLimiter *middleware.RateLimiter, limits RemoteLimits, owner *OwnerTransport, lifecycle *unifiedservice.OwnerLifecycle, policyRevision string) http.Handler {
	if owner == nil || lifecycle == nil {
		return m.StreamableHTTPHandlerWithOwner(tokenValidator, rateLimiter, limits, owner)
	}
	transport := server.NewStreamableHTTPServer(m.server,
		server.WithSessionIdManagerResolver(owner), server.WithLogger(ownerSDKLogger{}))
	mux := http.NewServeMux()
	mux.Handle("/mcp", authenticateMCPRequests(tokenValidator, owner.wrap(transport), m.auditRecorder))
	admin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.RawPath != "" || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		caller, _ := r.Context().Value(callerContextKey{}).(*security.Caller)
		if caller == nil || !caller.IsAdmin() {
			writeRemoteError(w, http.StatusForbidden, "forbidden", "administrator role required")
			return
		}
		var status unifiedservice.OwnerStatus
		switch {
		case r.URL.Path == "/internal/replica" && r.Method == http.MethodGet:
			status = lifecycle.Status()
		case r.URL.Path == "/internal/replica/drain" && r.Method == http.MethodPost:
			status = lifecycle.Drain()
		case r.URL.Path == "/internal/replica" || r.URL.Path == "/internal/replica/drain":
			allow := http.MethodGet
			if r.URL.Path == "/internal/replica/drain" {
				allow = http.MethodPost
			}
			w.Header().Set("Allow", allow)
			writeRemoteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "replica admin method is not supported")
			return
		default:
			http.NotFound(w, r)
			return
		}
		status.PolicyRevision = policyRevision
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(status)
	})
	mux.Handle("/internal/replica", authenticateMCPRequests(tokenValidator, admin, m.auditRecorder))
	mux.Handle("/internal/replica/drain", authenticateMCPRequests(tokenValidator, admin, m.auditRecorder))
	metrics := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, _ := r.Context().Value(callerContextKey{}).(*security.Caller)
		if caller == nil || !caller.IsAdmin() {
			writeRemoteError(w, http.StatusForbidden, "forbidden", "administrator role required")
			return
		}
		middleware.PrometheusHandler().ServeHTTP(w, r)
	})
	mux.Handle("/metrics", authenticateMCPRequests(tokenValidator, metrics, m.auditRecorder))
	return withRemoteLimits(rateLimiter, limits, mux)
}
