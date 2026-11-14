package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/golang-jwt/jwt/v5"
)

type onboardingClaimsValidator struct{ claims *security.Claims }

func (v onboardingClaimsValidator) ValidateMCPToken(_ context.Context, token string) (*security.Claims, error) {
	return v.claims, nil
}
func TestOnboardingIdentityComesFromValidatedClaims(t *testing.T) {
	claims := &security.Claims{UserID: "alice", TenantID: "tenant", Scope: sourceManagementScope, RegisteredClaims: jwt.RegisteredClaims{Issuer: "https://trusted.example", Subject: "alice"}}
	called := false
	h := authenticateMCPRequests(onboardingClaimsValidator{claims}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		m := NewMCPServer(nil, nil, nil)
		actor, caller, ok := m.onboardingActor(r.Context())
		if !ok || caller == nil || actor.Issuer != claims.Issuer || actor.Subject != "alice" || actor.Tenant != "tenant" {
			t.Error("verified identity not bound")
		}
	}))
	req := httptest.NewRequest("POST", "https://mcp.test/mcp", nil)
	req.Header.Set("Authorization", "Bearer synthetic")
	req.Header.Set("X-Subject", "attacker")
	req.Header.Set("X-Tenant", "other")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !called || rec.Code != 200 {
		t.Fatal("validated request not passed")
	}
}
