package security

import (
	"testing"
	"time"
)

// Tokens must round-trip the tenant claim (#9): it scopes every data-source
// authorization decision, so losing it in mint/validate would silently move
// a tenant user into the default bucket.

func newTestJWTManager() *JWTManager {
	return NewJWTManager("jwt-test-secret-0123456789abcdef0123456789", time.Hour)
}

func TestGenerateTokenRoundTripsTenantID(t *testing.T) {
	jm := newTestJWTManager()

	token, err := jm.GenerateToken("alice", "acme", "alice", []string{"user"})
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	claims, err := jm.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}
	if claims.UserID != "alice" || claims.Username != "alice" {
		t.Fatalf("claims identity = %q/%q", claims.UserID, claims.Username)
	}
	if claims.TenantID != "acme" {
		t.Fatalf("claims.TenantID = %q, want %q", claims.TenantID, "acme")
	}
	if claims.HasRole("user") != true {
		t.Fatalf("claims.Roles = %v", claims.Roles)
	}

	caller := CallerFromClaims(claims)
	if caller.TenantID != "acme" || caller.UserID != "alice" {
		t.Fatalf("CallerFromClaims() = %+v", caller)
	}
}

func TestGenerateTokenWithoutTenantYieldsDefaultBucketCaller(t *testing.T) {
	jm := newTestJWTManager()

	token, err := jm.GenerateToken("bob", "", "bob", nil)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	claims, err := jm.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}
	if claims.TenantID != "" {
		t.Fatalf("claims.TenantID = %q, want empty", claims.TenantID)
	}
}

func TestRefreshTokenPreservesTenantID(t *testing.T) {
	jm := newTestJWTManager()

	token, err := jm.GenerateToken("alice", "acme", "alice", []string{"user"})
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	claims, err := jm.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}

	refreshed, err := jm.RefreshToken(claims)
	if err != nil {
		t.Fatalf("RefreshToken() error = %v", err)
	}
	refreshedClaims, err := jm.ValidateToken(refreshed)
	if err != nil {
		t.Fatalf("ValidateToken(refreshed) error = %v", err)
	}
	if refreshedClaims.TenantID != "acme" || refreshedClaims.UserID != "alice" {
		t.Fatalf("refreshed claims = %+v, want tenant acme for alice", refreshedClaims)
	}
}
