package security

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestIDTokenVerifier(t *testing.T) {
	key := newOIDCTestKey(t, "browser-key")
	other := newOIDCTestKey(t, "other")
	p := newOIDCTestProvider(t, key)
	v, err := NewOIDCIDTokenVerifier(context.Background(), p.server.URL, "browser-client", p.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	makeClaims := func() map[string]any {
		now := time.Now()
		return map[string]any{"iss": p.server.URL, "sub": "alice", "aud": "browser-client", "exp": now.Add(time.Minute).Unix(), "iat": now.Unix(), "nonce": "bound-nonce"}
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
		nonce  string
		key    jwkTestKey
		valid  bool
	}{
		{"valid", func(c map[string]any) {}, "bound-nonce", key, true},
		{"untrusted multi audience", func(c map[string]any) { c["aud"] = []string{"browser-client", "other"}; c["azp"] = "browser-client" }, "bound-nonce", key, false},
		{"overlong subject", func(c map[string]any) { c["sub"] = strings.Repeat("a", 256) }, "bound-nonce", key, false},
		{"non-ascii subject", func(c map[string]any) { c["sub"] = "你好" }, "bound-nonce", key, false},
		{"wrong issuer", func(c map[string]any) { c["iss"] = "https://example.invalid" }, "bound-nonce", key, false},
		{"wrong audience", func(c map[string]any) { c["aud"] = "other" }, "bound-nonce", key, false},
		{"missing azp", func(c map[string]any) { c["aud"] = []string{"browser-client", "other"} }, "bound-nonce", key, false},
		{"wrong azp", func(c map[string]any) { c["azp"] = "other" }, "bound-nonce", key, false},
		{"wrong nonce", func(c map[string]any) {}, "wrong", key, false},
		{"missing nonce", func(c map[string]any) { delete(c, "nonce") }, "bound-nonce", key, false},
		{"missing iat", func(c map[string]any) { delete(c, "iat") }, "bound-nonce", key, false},
		{"future iat", func(c map[string]any) { c["iat"] = time.Now().Add(5 * time.Minute).Unix() }, "bound-nonce", key, false},
		{"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Second).Unix() }, "bound-nonce", key, false},
		{"not yet valid", func(c map[string]any) { c["nbf"] = time.Now().Add(time.Minute).Unix() }, "bound-nonce", key, false},
		{"wrong signing key", func(c map[string]any) {}, "bound-nonce", other, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := makeClaims()
			tc.mutate(c)
			got, err := v.Verify(context.Background(), p.token(t, tc.key, c), tc.nonce)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.valid && (got.Issuer != p.server.URL || got.Subject != "alice") {
				t.Fatalf("unexpected identity: %+v", got)
			}
		})
	}
	for _, typ := range []string{"at+JWT", "application/at+jwt"} {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims(makeClaims()))
		token.Header["kid"], token.Header["typ"] = key.id, typ
		signed, err := token.SignedString(key.private)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = v.Verify(context.Background(), signed, "bound-nonce"); err == nil {
			t.Fatalf("accepted access-token typ %s", typ)
		}
	}

	if _, err := v.Verify(context.Background(), strings.Repeat("x", maxOIDCAccessTokenLen+1), "bound-nonce"); err == nil {
		t.Fatal("accepted oversized ID token")
	}

}
