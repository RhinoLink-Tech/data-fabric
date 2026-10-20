package security

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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

func TestValidateTokenUsesStandardSubjectAndRequiresIdentity(t *testing.T) {
	jm := newTestJWTManager()
	expiry := jwt.NewNumericDate(time.Now().Add(time.Hour))

	subjectToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "alice",
			ExpiresAt: expiry,
		},
	}).SignedString([]byte(jm.secretKey))
	if err != nil {
		t.Fatal(err)
	}
	claims, err := jm.ValidateToken(subjectToken)
	if err != nil {
		t.Fatalf("standard subject token rejected: %v", err)
	}
	if claims.UserID != "alice" {
		t.Fatalf("UserID = %q, want subject alice", claims.UserID)
	}

	for name, jwtClaims := range map[string]*Claims{
		"missing identity": {
			Username: "alice",
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: expiry,
			},
		},
		"conflicting identity": {
			UserID: "alice",
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   "bob",
				ExpiresAt: expiry,
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			tokenString, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwtClaims).SignedString([]byte(jm.secretKey))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := jm.ValidateToken(tokenString); err == nil {
				t.Fatal("token with invalid identity was accepted")
			}
		})
	}
}

func TestGenerateTokenRequiresUserID(t *testing.T) {
	if _, err := newTestJWTManager().GenerateToken("  ", "", "alice", nil); err == nil {
		t.Fatal("GenerateToken accepted an empty user ID")
	}
}

func TestExtractTokenFromHeaderAcceptsStandardBearerScheme(t *testing.T) {
	jm := newTestJWTManager()
	for _, header := range []string{"Bearer token", "bearer token", "BEARER   token"} {
		token, err := jm.ExtractTokenFromHeader(header)
		if err != nil || token != "token" {
			t.Errorf("ExtractTokenFromHeader(%q) = %q, %v; want token", header, token, err)
		}
	}
	for _, header := range []string{"Basic token", "Bearer", "Bearer token extra", " token"} {
		if _, err := jm.ExtractTokenFromHeader(header); err == nil {
			t.Errorf("ExtractTokenFromHeader(%q) unexpectedly succeeded", header)
		}
	}
}
