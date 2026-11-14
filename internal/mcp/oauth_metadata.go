package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type oauthMetadataContextKey struct{}

type ProtectedResourceMetadata struct{ resource, issuer, host, metadata string }

// NewProtectedResourceMetadata requires operator-configured canonical URLs. It
// never derives discovery data from Host or forwarded headers. The validated
// audience must be the resource URI; otherwise resource indicator tokens could
// be advertised to clients even though the gateway would reject them.
func NewProtectedResourceMetadata(resource, issuer, audience string) (*ProtectedResourceMetadata, error) {
	r, err := url.Parse(resource)
	if err != nil || r.Scheme != "https" || r.Hostname() == "" || r.User != nil || r.Path != "/mcp" || r.RawPath != "" || r.RawQuery != "" || r.ForceQuery || r.Fragment != "" || r.Opaque != "" || strings.TrimSpace(resource) != resource || audience != resource || r.String() != resource {
		return nil, errors.New("invalid OAuth resource or audience")
	}
	a, err := url.Parse(issuer)
	if err != nil || a.Scheme != "https" || a.Hostname() == "" || a.User != nil || a.RawQuery != "" || a.ForceQuery || a.Fragment != "" || a.Opaque != "" || strings.TrimSpace(issuer) != issuer || a.String() != issuer {
		return nil, errors.New("invalid OAuth issuer")
	}
	return &ProtectedResourceMetadata{resource: resource, issuer: issuer, host: r.Host, metadata: "https://" + r.Host + "/.well-known/oauth-protected-resource/mcp"}, nil
}

func (m *ProtectedResourceMetadata) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" && r.URL.Path != "/.well-known/oauth-protected-resource/mcp" && r.URL.Path != "/.well-known/oauth-protected-resource" {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.EqualFold(r.Host, m.host) {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/mcp" {
			r = r.Clone(context.WithValue(r.Context(), oauthMetadataContextKey{}, m.metadata))
			// Public Host has already been checked against the fixed resource.
			// The SDK enforces loopback Host for loopback listeners; normalize
			// only after that check, never from forwarded headers.
			r.Host = "localhost"
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": m.resource, "authorization_servers": []string{m.issuer}, "bearer_methods_supported": []string{"header"}})
	})
}
