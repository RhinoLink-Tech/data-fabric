// Package tunnelbridge restricts the same-process host-tunnel listener.
package tunnelbridge

import (
	"net/http"
	"strings"
)

// Handler dispatches only public MCP and onboarding routes. It never logs a
// request, so authorization headers and OAuth query parameters remain private.
func Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" && !strings.HasPrefix(r.URL.Path, "/onboarding/") {
			http.NotFound(w, r)
			return
		}
		r = r.Clone(r.Context())
		// External clients must not choose their apparent address.
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("X-Real-IP")
		r.Header.Del("Forwarded")
		next.ServeHTTP(w, r)
	})
}
