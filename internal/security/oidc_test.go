package security

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type oidcTestProvider struct {
	server *httptest.Server
	mu     sync.RWMutex
	keys   []jwkTestKey
}

type jwkTestKey struct {
	id      string
	private *rsa.PrivateKey
}

func newOIDCTestProvider(t *testing.T, initial ...jwkTestKey) *oidcTestProvider {
	t.Helper()
	provider := &oidcTestProvider{keys: initial}
	provider.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":   provider.server.URL,
				"jwks_uri": provider.server.URL + "/jwks",
			})
		case "/jwks":
			provider.mu.RLock()
			defer provider.mu.RUnlock()
			keys := make([]map[string]string, 0, len(provider.keys))
			for _, key := range provider.keys {
				n := base64.RawURLEncoding.EncodeToString(key.private.PublicKey.N.Bytes())
				e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.private.PublicKey.E)).Bytes())
				keys = append(keys, map[string]string{
					"kty": "RSA", "use": "sig", "alg": "RS256", "kid": key.id, "n": n, "e": e,
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.server.Close)
	return provider
}

func newOIDCTestKey(t *testing.T, id string) jwkTestKey {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return jwkTestKey{id: id, private: privateKey}
}

func (p *oidcTestProvider) validator(t *testing.T) *OIDCTokenValidator {
	t.Helper()
	validator, err := NewOIDCTokenValidatorWithHTTPClient(context.Background(), OIDCTokenConfig{
		Issuer: p.server.URL, Audience: "data-fabric-mcp",
		RequiredScopes: []string{"data-fabric.query"},
		TenantClaim:    "tenant_id", RolesClaim: "realm_access.roles",
		UsernameClaim: "preferred_username", JWKSRefreshInterval: time.Hour,
	}, p.server.Client())
	if err != nil {
		t.Fatalf("NewOIDCTokenValidator() error = %v", err)
	}
	return validator
}

func (p *oidcTestProvider) token(t *testing.T, key jwkTestKey, claims map[string]any) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims(claims))
	token.Header["kid"] = key.id
	signed, err := token.SignedString(key.private)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func validOIDCClaims(issuer string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": issuer, "sub": "user-123", "aud": "data-fabric-mcp",
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(),
		"scope":     "openid data-fabric.query",
		"tenant_id": "tenant-a", "preferred_username": "alice",
		"realm_access": map[string]any{"roles": []string{"reader", "analyst"}},
	}
}

func TestOIDCTokenValidatorMapsIdentityAndSupportsKeyRotation(t *testing.T) {
	first := newOIDCTestKey(t, "key-1")
	provider := newOIDCTestProvider(t, first)
	validator := provider.validator(t)

	claims, err := validator.ValidateMCPToken(context.Background(), provider.token(t, first, validOIDCClaims(provider.server.URL)))
	if err != nil {
		t.Fatalf("ValidateMCPToken() error = %v", err)
	}
	caller := CallerFromClaims(claims)
	if claims.Username != "alice" {
		t.Fatalf("validated username claim = %q, want alice", claims.Username)
	}
	if caller.UserID != "user-123" || caller.TenantID != "tenant-a" {
		t.Fatalf("caller identity = %+v", caller)
	}
	if len(caller.Roles) != 2 || caller.Roles[0] != "reader" || len(caller.Scopes) != 2 || caller.Scopes[1] != "data-fabric.query" {
		t.Fatalf("caller authorization claims = %+v", caller)
	}

	rotated := newOIDCTestKey(t, "key-2")
	provider.mu.Lock()
	provider.keys = []jwkTestKey{rotated}
	provider.mu.Unlock()
	claims, err = validator.ValidateMCPToken(context.Background(), provider.token(t, rotated, validOIDCClaims(provider.server.URL)))
	if err != nil {
		t.Fatalf("ValidateMCPToken() after JWKS key rotation error = %v", err)
	}
	if claims.UserID != "user-123" {
		t.Fatalf("rotated token subject = %q, want user-123", claims.UserID)
	}
}

func TestOIDCTokenValidatorSupportsConfiguredScopeArrayClaim(t *testing.T) {
	key := newOIDCTestKey(t, "key-scp")
	provider := newOIDCTestProvider(t, key)
	validator, err := NewOIDCTokenValidatorWithHTTPClient(context.Background(), OIDCTokenConfig{
		Issuer: provider.server.URL, Audience: "data-fabric-mcp",
		RequiredScopes: []string{"data-fabric.query"}, ScopeClaim: "scp",
		TenantClaim: "tenant_id", RolesClaim: "realm_access.roles",
		UsernameClaim: "preferred_username", JWKSRefreshInterval: time.Hour,
	}, provider.server.Client())
	if err != nil {
		t.Fatalf("NewOIDCTokenValidator() error = %v", err)
	}
	tokenClaims := validOIDCClaims(provider.server.URL)
	delete(tokenClaims, "scope")
	tokenClaims["scp"] = []string{"openid", "data-fabric.query"}
	claims, err := validator.ValidateMCPToken(context.Background(), provider.token(t, key, tokenClaims))
	if err != nil {
		t.Fatalf("ValidateMCPToken() with configured scope array error = %v", err)
	}
	if claims.Scope != "openid data-fabric.query" {
		t.Fatalf("mapped scope = %q", claims.Scope)
	}
}

func TestOIDCTokenValidatorRejectsInvalidClaimsAndSignatures(t *testing.T) {
	key := newOIDCTestKey(t, "key-1")
	other := newOIDCTestKey(t, "other")
	provider := newOIDCTestProvider(t, key)
	validator := provider.validator(t)

	tests := []struct {
		name   string
		key    jwkTestKey
		mutate func(map[string]any)
	}{
		{name: "wrong issuer", key: key, mutate: func(c map[string]any) { c["iss"] = "https://attacker.example" }},
		{name: "wrong audience", key: key, mutate: func(c map[string]any) { c["aud"] = "other-api" }},
		{name: "expired", key: key, mutate: func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }},
		{name: "not yet valid", key: key, mutate: func(c map[string]any) { c["nbf"] = time.Now().Add(time.Minute).Unix() }},
		{name: "missing expiry", key: key, mutate: func(c map[string]any) { delete(c, "exp") }},
		{name: "missing subject", key: key, mutate: func(c map[string]any) { delete(c, "sub") }},
		{name: "missing scope", key: key, mutate: func(c map[string]any) { delete(c, "scope") }},
		{name: "insufficient scope", key: key, mutate: func(c map[string]any) { c["scope"] = "openid profile" }},
		{name: "invalid signature", key: other, mutate: func(c map[string]any) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := validOIDCClaims(provider.server.URL)
			tt.mutate(claims)
			if _, err := validator.ValidateMCPToken(context.Background(), provider.token(t, tt.key, claims)); err == nil {
				t.Fatal("ValidateMCPToken() error = nil, want rejection")
			}
		})
	}
}

func TestOIDCTokenValidatorRejectsHMACAccessToken(t *testing.T) {
	key := newOIDCTestKey(t, "key-rs")
	provider := newOIDCTestProvider(t, key)
	validator := provider.validator(t)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(validOIDCClaims(provider.server.URL)))
	token.Header["kid"] = key.id
	signed, err := token.SignedString([]byte("not-an-issuer-key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validator.ValidateMCPToken(context.Background(), signed); err == nil {
		t.Fatal("ValidateMCPToken() error = nil, want HMAC algorithm rejection")
	}
}

func TestNewOIDCTokenValidatorRequiresHTTPSIssuer(t *testing.T) {
	_, err := NewOIDCTokenValidator(context.Background(), OIDCTokenConfig{
		Issuer: "http://issuer.example", Audience: "data-fabric-mcp", RequiredScopes: []string{"data-fabric.query"},
	})
	if err == nil {
		t.Fatal("NewOIDCTokenValidator() error = nil, want HTTPS issuer validation error")
	}
}

func TestOIDCTokenValidatorRejectsOversizedToken(t *testing.T) {
	key := newOIDCTestKey(t, "key-1")
	provider := newOIDCTestProvider(t, key)
	validator := provider.validator(t)
	if _, err := validator.ValidateMCPToken(context.Background(), string(make([]byte, maxOIDCAccessTokenLen+1))); err == nil {
		t.Fatal("ValidateMCPToken() error = nil, want oversized token rejection")
	}
}
