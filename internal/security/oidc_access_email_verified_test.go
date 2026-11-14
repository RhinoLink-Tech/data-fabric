package security

import (
	"context"
	"testing"
)

func TestAccessTokenEmailVerificationClaim(t *testing.T) {
	key := newOIDCTestKey(t, "access-email-claim")
	provider := newOIDCTestProvider(t, key)
	verifier := provider.validator(t)
	for _, tc := range []struct {
		name                     string
		value                    any
		present, valid, verified bool
	}{
		{"missing", nil, false, true, false},
		{"verified", true, true, true, true},
		{"unverified", false, true, true, false},
		{"string", "true", true, false, false},
		{"null", nil, true, false, false},
		{"number", 1, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := validOIDCClaims(provider.server.URL)
			if tc.present {
				claims["email_verified"] = tc.value
			}
			got, err := verifier.ValidateMCPToken(context.Background(), provider.token(t, key, claims))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && got.EmailVerified != tc.verified {
				t.Fatalf("verified=%v, want %v", got.EmailVerified, tc.verified)
			}
		})
	}
}
