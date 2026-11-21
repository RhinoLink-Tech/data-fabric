package mcp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
)

func TestOAuthAudienceTransitionHTTP(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var provider *httptest.Server
	provider = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": provider.URL, "jwks_uri": provider.URL + "/jwks"})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	resource := "https://fabric.example/mcp"
	metadata, err := NewProtectedResourceMetadata(resource, provider.URL, resource)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := security.NewOIDCTokenValidatorWithHTTPClient(context.Background(), security.OIDCTokenConfig{Issuer: provider.URL, Audience: resource, LegacyAudience: "data-fabric-mcp", RequiredScopes: []string{"data-fabric.query"}, TenantClaim: "tenant_id"}, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	target := &model.DataSource{OwnerID: "alice", TenantID: "team-a"}
	handler := metadata.Wrap(StrictTenantHTTPHandler(authenticateMCPRequests(validator, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !callerFromContext(r.Context()).CanRead(target) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))))
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "https://fabric.example/.well-known/oauth-protected-resource/mcp", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), resource) {
		t.Fatal("resource metadata unavailable")
	}
	for _, tc := range []struct {
		name, aud, scope, tenant string
		want                     int
	}{
		{"new resource", resource, "data-fabric.query", "team-a", http.StatusNoContent},
		{"legacy workbuddy", "data-fabric-mcp", "data-fabric.query", "team-a", http.StatusNoContent},
		{"wrong audience", "other", "data-fabric.query", "team-a", http.StatusUnauthorized},
		{"legacy lacks scope", "data-fabric-mcp", "openid", "team-a", http.StatusUnauthorized},
		{"other tenant", "data-fabric-mcp", "data-fabric.query", "team-b", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": provider.URL, "sub": "alice", "aud": tc.aud, "scope": tc.scope, "tenant_id": tc.tenant, "exp": time.Now().Add(time.Minute).Unix()})
			token.Header["kid"] = "test"
			signed, err := token.SignedString(key)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, resource, nil)
			req.Header.Set("Authorization", "Bearer "+signed)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d", w.Code, tc.want)
			}
		})
	}
	// Exercise the actual Streamable HTTP initialize path with both audiences.
	transport := metadata.Wrap(StrictTenantHTTPHandler(NewMCPServer(nil, nil, nil).StreamableHTTPHandler(validator)))
	for _, audience := range []string{resource, "data-fabric-mcp"} {
		t.Run("initialize/"+audience, func(t *testing.T) {
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": provider.URL, "sub": "alice", "aud": audience, "scope": "data-fabric.query", "tenant_id": "team-a", "exp": time.Now().Add(time.Minute).Unix()})
			token.Header["kid"] = "test"
			signed, err := token.SignedString(key)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, resource, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"synthetic","version":"1"}}}`))
			req.Header.Set("Authorization", "Bearer "+signed)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			w := httptest.NewRecorder()
			transport.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("initialize status %d", w.Code)
			}
		})
	}
}
