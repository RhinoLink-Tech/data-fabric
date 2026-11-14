package main

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

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSelfServiceSignedOIDCTokenAndGrant(t *testing.T) {
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
	oidc, err := security.NewOIDCTokenValidatorWithHTTPClient(context.Background(), security.OIDCTokenConfig{Issuer: provider.URL, Audience: "data-fabric-mcp", RequiredScopes: []string{"data-fabric.query"}}, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE data_sources (owner_id text,tenant_id text)").Error; err != nil {
		t.Fatal(err)
	}
	if err = onboarding.MigrateGrants(db); err != nil {
		t.Fatal(err)
	}
	if err = auditlog.Migrate(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	v := &selfServiceValidator{next: oidc, issuer: provider.URL, provision: func(ctx context.Context, subject, tenant string) error {
		return provisionSelfServiceGrant(ctx, db, provider.URL, subject, tenant, strings.Repeat("k", 32), nil)
	}}
	token := func(subject string, verified any) string {
		now := time.Now()
		claims := jwt.MapClaims{"iss": provider.URL, "sub": subject, "aud": "data-fabric-mcp", "scope": "openid data-fabric.query", "exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "email_verified": verified}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "test"
		raw, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if _, err = v.ValidateMCPToken(ctx, token("unverified", false)); err == nil {
		t.Fatal("unverified user was provisioned")
	}
	alice, err := v.ValidateMCPToken(ctx, token("alice", true))
	if err != nil {
		t.Fatal(err)
	}
	bob, err := v.ValidateMCPToken(ctx, token("bob", true))
	if err != nil {
		t.Fatal(err)
	}
	if alice.TenantID == "" || alice.TenantID == bob.TenantID {
		t.Fatal("tenants not independent")
	}
	store, _ := onboarding.NewGrantStore(db)
	_, tenant, ok, err := store.Resolve(ctx, provider.URL, "alice")
	if err != nil || !ok || tenant != alice.TenantID {
		t.Fatal("grant does not match authenticated identity")
	}
	if err = store.Revoke(ctx, provider.URL, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err = v.ValidateMCPToken(ctx, token("alice", true)); err == nil {
		t.Fatal("revoked grant restored")
	}
}
