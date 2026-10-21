package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/RhinoLink-Tech/data-fabric/internal/middleware"
)

// DefaultRemoteMaxConcurrentRequests bounds in-flight remote MCP requests
// when mcp.max_concurrent_requests is unset.
const DefaultRemoteMaxConcurrentRequests = 64

// RemoteLimits bounds the remote MCP entrance: request bodies, per-client
// request rate (through the shared limiter), and in-flight requests. They
// exist so one client cannot exhaust gateway memory or connections before
// authentication runs (#185, #186). Zero values fall back to safe defaults.
type RemoteLimits struct {
	// MaxBodyBytes caps one request body; 0 uses the server-wide default.
	MaxBodyBytes int64
	// MaxConcurrent caps in-flight requests across all clients; 0 uses
	// DefaultRemoteMaxConcurrentRequests.
	MaxConcurrent int
}

// EffectiveBodyBytes returns the body cap after defaulting; exposed so
// startup can log the real budget.
func (l RemoteLimits) EffectiveBodyBytes() int64 {
	if l.MaxBodyBytes <= 0 {
		return middleware.DefaultMaxRequestBodyBytes
	}
	return l.MaxBodyBytes
}

// EffectiveConcurrent returns the concurrency cap after defaulting.
func (l RemoteLimits) EffectiveConcurrent() int {
	if l.MaxConcurrent <= 0 {
		return DefaultRemoteMaxConcurrentRequests
	}
	return l.MaxConcurrent
}

// StreamableHTTPHandlerWithLimits is StreamableHTTPHandler wrapped with
// entrance budgets: body limit → per-client rate limit → concurrency cap →
// authentication → transport. The budgets run before the token check on
// purpose: OIDC/JWKS validation is network work, so unauthenticated floods
// must be bounded first.
func (m *MCPServer) StreamableHTTPHandlerWithLimits(tokenValidator MCPTokenValidator, rateLimiter *middleware.RateLimiter, limits RemoteLimits) http.Handler {
	return withRemoteLimits(rateLimiter, limits, m.StreamableHTTPHandler(tokenValidator))
}

// withRemoteLimits applies the entrance budgets around an already-built
// handler; it is split out so the wrapping itself can be tested without an
// MCP server.
func withRemoteLimits(rateLimiter *middleware.RateLimiter, limits RemoteLimits, next http.Handler) http.Handler {
	handler := limitConcurrency(limits, next)
	if rateLimiter != nil {
		handler = rateLimiter.RateLimitHTTP(handler)
	}
	return limitRemoteRequestBody(limits, handler)
}

// limitRemoteRequestBody rejects requests whose declared body exceeds
// maxBytes before the body is read, and caps undeclared/chunked bodies via
// http.MaxBytesReader so the transport sees the limit too. maxBytes <= 0
// falls back to the server-wide default.
func limitRemoteRequestBody(limits RemoteLimits, next http.Handler) http.Handler {
	maxBytes := limits.EffectiveBodyBytes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxBytes {
			writeRemoteError(w, http.StatusRequestEntityTooLarge, "request_too_large",
				fmt.Sprintf("request body exceeds %d bytes; reduce the payload and retry", maxBytes))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}

// limitConcurrency bounds the number of in-flight remote MCP requests across
// all clients; overflow is rejected with 503 instead of queuing (#186).
// maxConcurrent <= 0 falls back to DefaultRemoteMaxConcurrentRequests.
func limitConcurrency(limits RemoteLimits, next http.Handler) http.Handler {
	slots := make(chan struct{}, limits.EffectiveConcurrent())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			writeRemoteError(w, http.StatusServiceUnavailable, "service_busy",
				"too many concurrent MCP requests; retry shortly")
		}
	})
}

// writeRemoteError reports entrance rejections in the same JSON shape as the
// transport's unauthorized response so clients see one error format.
func writeRemoteError(w http.ResponseWriter, status int, errorName, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   errorName,
		"message": message,
	})
}
