package security

import (
	"context"
	"testing"
	"time"
)

func TestIDTokenEmailVerificationClaim(t *testing.T) {
	key := newOIDCTestKey(t, "email-claim-key")
	provider := newOIDCTestProvider(t, key)
	verifier, err := NewOIDCIDTokenVerifier(context.Background(), provider.server.URL, "browser-client", provider.server.Client())
	if err != nil {
		t.Fatal(err)
	}
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
			now := time.Now()
			claims := map[string]any{"iss": provider.server.URL, "sub": "alice", "aud": "browser-client", "exp": now.Add(time.Minute).Unix(), "iat": now.Unix(), "nonce": "bound-nonce"}
			if tc.present {
				claims["email_verified"] = tc.value
			}
			got, err := verifier.Verify(context.Background(), provider.token(t, key, claims), "bound-nonce")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && got.EmailVerified != tc.verified {
				t.Fatalf("verified=%v, want %v", got.EmailVerified, tc.verified)
			}
		})
	}
}
