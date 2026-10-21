package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/middleware"
)

// okHandler marks passage through every budget layer.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func newRemoteRequest(body string) *http.Request {
	if body == "" {
		return httptest.NewRequest(http.MethodPost, "/mcp", nil)
	}
	return httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
}

func decodeRemoteError(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var payload map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return payload
}

// Bodies that declare themselves larger than the cap are rejected with 413
// before the transport reads them (#185).
func TestRemoteLimitsRejectOversizedBody(t *testing.T) {
	handler := withRemoteLimits(nil, RemoteLimits{MaxBodyBytes: 16}, okHandler())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newRemoteRequest(`{"jsonrpc":"2.0"}`))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
	if payload := decodeRemoteError(t, rec); payload["error"] != "request_too_large" {
		t.Fatalf("error = %q, want request_too_large", payload["error"])
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, newRemoteRequest(`{"a":1}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d for a body within the cap, want %d", rec.Code, http.StatusOK)
	}
}

// Undeclared or chunked bodies are capped by http.MaxBytesReader: the read
// fails once it passes the cap instead of buffering unbounded data (#185).
func TestRemoteLimitsCapUndeclaredBody(t *testing.T) {
	reading := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	// A plain io.Reader leaves ContentLength unknown (-1), defeating the
	// pre-check; only MaxBytesReader can stop the read.
	handler := withRemoteLimits(nil, RemoteLimits{MaxBodyBytes: 16}, reading)

	request := httptest.NewRequest(http.MethodPost, "/mcp", struct{ io.Reader }{strings.NewReader("twenty bytes of body")})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d from the capped read", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

// Zero-valued RemoteLimits fall back to the server-wide body default rather
// than disabling the cap (#185).
func TestRemoteLimitsDefaultBodyBudget(t *testing.T) {
	limits := RemoteLimits{}
	if got, want := limits.EffectiveBodyBytes(), middleware.DefaultMaxRequestBodyBytes; got != want {
		t.Fatalf("default body budget = %d, want %d", got, want)
	}
	if got, want := limits.EffectiveConcurrent(), DefaultRemoteMaxConcurrentRequests; got != want {
		t.Fatalf("default concurrency = %d, want %d", got, want)
	}

	handler := withRemoteLimits(nil, limits, okHandler())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(strings.Repeat("a", int(middleware.DefaultMaxRequestBodyBytes)+1))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d one byte over the default budget", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

// Requests beyond the concurrency cap get 503 + Retry-After instead of
// queueing, and release the slot when the request finishes (#186).
func TestRemoteLimitsRejectConcurrentOverflow(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	blocked := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})
	handler := withRemoteLimits(nil, RemoteLimits{MaxBodyBytes: 1 << 20, MaxConcurrent: 1}, blocked)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newRemoteRequest("{}"))
		done <- rec
	}()
	<-started

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newRemoteRequest("{}"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want \"1\"", got)
	}
	if payload := decodeRemoteError(t, rec); payload["error"] != "service_busy" {
		t.Fatalf("error = %q, want service_busy", payload["error"])
	}

	close(release)
	if first := <-done; first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d after release", first.Code, http.StatusOK)
	}

	// The slot is reusable once the blocked request drained.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, newRemoteRequest("{}"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status after slot release = %d, want %d", rec.Code, http.StatusOK)
	}
	<-started
}

// The remote entrance borrows the shared per-client rate limiter: a burst
// over the configured limit gets 429 + Retry-After (#185).
func TestRemoteLimitsApplySharedRateLimiter(t *testing.T) {
	limiter := middleware.NewRateLimiter(middleware.RateLimiterConfig{
		RPM:             60,
		Burst:           1,
		CleanupInterval: time.Hour,
	})
	handler := withRemoteLimits(limiter, RemoteLimits{MaxBodyBytes: 1 << 20, MaxConcurrent: 64}, okHandler())

	// httptest requests share RemoteAddr, so both hit one peer bucket.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newRemoteRequest("{}"))
	if rec.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d", rec.Code, http.StatusOK)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, newRemoteRequest("{}"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second immediate request status = %d, want %d (body %s)", rec.Code, http.StatusTooManyRequests, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want \"1\" (one token per second at 60 RPM)", got)
	}
	if payload := decodeRemoteError(t, rec); payload["error"] != "rate_limit_exceeded" {
		t.Fatalf("error = %q, want rate_limit_exceeded", payload["error"])
	}
}
