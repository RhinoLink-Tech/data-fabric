package security

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// OIDCBrowserIdentity represents authentication only; it does not grant any
// data-source permissions, select a tenant, or establish a browser session.
type OIDCBrowserIdentity struct {
	Issuer, Subject string
	ExpiresAt       time.Time
}

type OIDCIDTokenVerifier struct {
	keys     *OIDCTokenValidator
	clientID string
}

// NewOIDCIDTokenVerifier creates a verifier for a fixed trusted issuer and
// client ID. A custom HTTPS client may provide enterprise trust roots.
func NewOIDCIDTokenVerifier(ctx context.Context, issuer, clientID string, client *http.Client) (*OIDCIDTokenVerifier, error) {
	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(clientID) != clientID {
		return nil, errors.New("client ID is required")
	}
	// The shared constructor configures discovery, HTTPS, and signing keys;
	// ID tokens are never passed through the access-token scope parser.
	keys, err := NewOIDCTokenValidatorWithHTTPClient(ctx, OIDCTokenConfig{Issuer: issuer, Audience: clientID, RequiredScopes: []string{"internal-id-token-verifier"}}, client)
	if err != nil {
		return nil, err
	}
	return &OIDCIDTokenVerifier{keys: keys, clientID: clientID}, nil
}

// Issuer returns the verifier fixed trusted issuer.
func (v *OIDCIDTokenVerifier) Issuer() string {
	if v == nil || v.keys == nil {
		return ""
	}
	return v.keys.issuer
}

// Verify validates an ID token from a code exchange against the nonce bound to
// that one-time login request. Callers must separately enforce single-use
// state, browser binding, tenant mapping and management authorization.
func (v *OIDCIDTokenVerifier) Verify(ctx context.Context, rawToken, expectedNonce string) (*OIDCBrowserIdentity, error) {
	if v == nil || v.keys == nil || expectedNonce == "" || len(rawToken) == 0 || len(rawToken) > maxOIDCAccessTokenLen {
		return nil, errors.New("invalid ID token")
	}
	// Reject tokens explicitly marked as OAuth access tokens. Type alone does
	// not prove an ID token: this input must come from the bound code exchange.
	parsed, err := jose.ParseSignedCompact(rawToken, allowedOIDCSignatureAlgorithms)
	if err != nil || len(parsed.Signatures) != 1 {
		return nil, errors.New("invalid ID token")
	}
	// RS256 is the OIDC default when no explicit client registration is
	// supported. Other algorithms require an explicit trusted configuration.
	if parsed.Signatures[0].Header.Algorithm != "RS256" {
		return nil, errors.New("invalid ID token")
	}
	if rawType, present := parsed.Signatures[0].Header.ExtraHeaders[jose.HeaderKey("typ")]; present {
		typ, ok := rawType.(string)
		if !ok || strings.EqualFold(typ, "at+jwt") || strings.EqualFold(typ, "application/at+jwt") {
			return nil, errors.New("invalid ID token")
		}
	}
	payload, err := v.keys.verifySignedOIDCToken(ctx, rawToken)
	if err != nil {
		return nil, errors.New("invalid ID token")
	}
	var c map[string]json.RawMessage
	if json.Unmarshal(payload, &c) != nil {
		return nil, errors.New("invalid ID token")
	}
	issuer, err := stringClaim(c, "iss", true)
	if err != nil || issuer != v.keys.issuer {
		return nil, errors.New("invalid ID token")
	}
	subject, err := stringClaim(c, "sub", true)
	if err != nil || len(subject) > 255 {
		return nil, errors.New("invalid ID token")
	}
	for _, b := range []byte(subject) {
		if b > 127 {
			return nil, errors.New("invalid ID token")
		}
	}
	aud, err := audienceClaim(c["aud"])
	if err != nil || !containsString(aud, v.clientID) {
		return nil, errors.New("invalid ID token")
	}
	// No additional audience is trusted by default, even when azp matches.
	for _, audience := range aud {
		if audience != v.clientID {
			return nil, errors.New("invalid ID token")
		}
	}
	azp, err := stringClaim(c, "azp", false)
	if err != nil || (len(aud) > 1 && azp == "") || (azp != "" && azp != v.clientID) {
		return nil, errors.New("invalid ID token")
	}
	nonce, err := stringClaim(c, "nonce", true)
	if err != nil || subtle.ConstantTimeCompare([]byte(nonce), []byte(expectedNonce)) != 1 {
		return nil, errors.New("invalid ID token")
	}
	now := float64(time.Now().Unix())
	exp, err := numericDateClaim(c["exp"], true)
	if err != nil || exp <= now {
		return nil, errors.New("invalid ID token")
	}
	iat, err := numericDateClaim(c["iat"], true)
	if err != nil || iat <= 0 || iat > now+60 || iat > exp {
		return nil, errors.New("invalid ID token")
	}
	nbf, err := numericDateClaim(c["nbf"], false)
	if err != nil || nbf > now {
		return nil, errors.New("invalid ID token")
	}
	return &OIDCBrowserIdentity{Issuer: issuer, Subject: subject, ExpiresAt: time.Unix(int64(exp), 0)}, nil
}
