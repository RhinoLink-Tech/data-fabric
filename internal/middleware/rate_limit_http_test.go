package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The net/http adapter keys buckets by peer address: one address exhausting
// its burst does not throttle a different peer, and the rejection carries a
// machine-readable Retry-After.
func TestRateLimitHTTPKeysByPeerAddress(t *testing.T) {
	limiter := NewRateLimiter(RateLimiterConfig{
		RPM:             60,
		Burst:           1,
		CleanupInterval: time.Hour,
	})
	var served int
	handler := limiter.RateLimitHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		w.WriteHeader(http.StatusNoContent)
	}))

	serve := func(remoteAddr string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		request.RemoteAddr = remoteAddr
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	if rec := serve("192.0.2.10:1000"); rec.Code != http.StatusNoContent {
		t.Fatalf("first request from 192.0.2.10: status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	rec := serve("192.0.2.10:1001")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request from 192.0.2.10: status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want \"1\" (one token per second at 60 RPM)", got)
	}
	var payload map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode rejection body %q: %v", rec.Body.String(), err)
	}
	if payload["error"] != "rate_limit_exceeded" {
		t.Fatalf("error = %q, want rate_limit_exceeded", payload["error"])
	}

	// A different peer address has its own bucket and is not collateral damage.
	if rec := serve("192.0.2.11:1000"); rec.Code != http.StatusNoContent {
		t.Fatalf("request from a different peer: status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if served != 2 {
		t.Fatalf("handler served %d requests, want 2", served)
	}
}
