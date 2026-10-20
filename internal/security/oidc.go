package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
)

const (
	maxOIDCDiscoveryBytes = 256 << 10
	maxOIDCJWKSBytes      = 1 << 20
	maxOIDCAccessTokenLen = 64 << 10
	maxOIDCJWKSKeyCount   = 128
	maxOIDCRefreshBackoff = 30 * time.Second
	oidcHTTPTimeout       = 5 * time.Second
)

var allowedOIDCSignatureAlgorithms = []jose.SignatureAlgorithm{
	jose.RS256, jose.RS384, jose.RS512,
	jose.PS256, jose.PS384, jose.PS512,
	jose.ES256, jose.ES384, jose.ES512,
	jose.EdDSA,
}

type OIDCTokenConfig struct {
	Issuer              string
	Audience            string
	RequiredScopes      []string
	ScopeClaim          string
	TenantClaim         string
	RolesClaim          string
	UsernameClaim       string
	JWKSRefreshInterval time.Duration
}

// OIDCTokenValidator validates signed OAuth access tokens against a trusted
// OIDC discovery document and its rotating JWKS.
type OIDCTokenValidator struct {
	issuer          string
	audience        string
	requiredScopes  []string
	scopeClaim      string
	tenantClaim     string
	rolesClaim      string
	usernameClaim   string
	jwksURL         string
	refreshInterval time.Duration
	client          *http.Client

	mu                  sync.Mutex
	keys                []jose.JSONWebKey
	refreshedAt         time.Time
	lastUnknownKIDFetch time.Time
}

func NewOIDCTokenValidator(ctx context.Context, cfg OIDCTokenConfig) (*OIDCTokenValidator, error) {
	return NewOIDCTokenValidatorWithHTTPClient(ctx, cfg, nil)
}

// NewOIDCTokenValidatorWithHTTPClient allows deployments to supply an HTTP
// client with their enterprise CA roots. The client remains restricted to HTTPS.
func NewOIDCTokenValidatorWithHTTPClient(ctx context.Context, cfg OIDCTokenConfig, client *http.Client) (*OIDCTokenValidator, error) {
	cfg.Issuer = strings.TrimSpace(cfg.Issuer)
	cfg.Audience = strings.TrimSpace(cfg.Audience)
	if err := validateOIDCURL(cfg.Issuer); err != nil {
		return nil, fmt.Errorf("invalid OIDC issuer: %w", err)
	}
	if cfg.Audience == "" {
		return nil, errors.New("OIDC audience is required")
	}
	if len(cfg.RequiredScopes) == 0 {
		return nil, errors.New("at least one OIDC scope is required")
	}
	if cfg.JWKSRefreshInterval == 0 {
		cfg.JWKSRefreshInterval = 5 * time.Minute
	}
	if cfg.JWKSRefreshInterval < time.Minute || cfg.JWKSRefreshInterval > time.Hour {
		return nil, errors.New("OIDC JWKS refresh interval must be between 1m and 1h")
	}
	if strings.TrimSpace(cfg.ScopeClaim) == "" {
		cfg.ScopeClaim = "scope"
	}
	if strings.TrimSpace(cfg.TenantClaim) == "" {
		cfg.TenantClaim = "tenant"
	}
	if strings.TrimSpace(cfg.RolesClaim) == "" {
		cfg.RolesClaim = "roles"
	}
	if strings.TrimSpace(cfg.UsernameClaim) == "" {
		cfg.UsernameClaim = "preferred_username"
	}

	if client == nil {
		client = &http.Client{Timeout: oidcHTTPTimeout}
	}
	httpClient := *client
	if httpClient.Timeout == 0 || httpClient.Timeout > oidcHTTPTimeout {
		httpClient.Timeout = oidcHTTPTimeout
	}
	previousRedirectCheck := httpClient.CheckRedirect
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("OIDC redirects must use HTTPS")
		}
		if len(via) >= 5 {
			return errors.New("too many OIDC redirects")
		}
		if previousRedirectCheck != nil {
			return previousRedirectCheck(req, via)
		}
		return nil
	}

	issuerURL, _ := url.Parse(cfg.Issuer)
	discoveryURL := strings.TrimRight(cfg.Issuer, "/") + "/.well-known/openid-configuration"
	discoveryCtx, cancel := context.WithTimeout(ctx, oidcHTTPTimeout)
	defer cancel()
	var metadata map[string]json.RawMessage
	if err := fetchOIDCJSON(discoveryCtx, &httpClient, discoveryURL, maxOIDCDiscoveryBytes, &metadata); err != nil {
		return nil, fmt.Errorf("OIDC discovery failed")
	}
	var discoveredIssuer, jwksURL string
	if err := json.Unmarshal(metadata["issuer"], &discoveredIssuer); err != nil || discoveredIssuer != cfg.Issuer {
		return nil, errors.New("OIDC discovery issuer does not match the configured issuer")
	}
	if err := json.Unmarshal(metadata["jwks_uri"], &jwksURL); err != nil {
		return nil, errors.New("OIDC discovery document does not contain jwks_uri")
	}
	if err := validateOIDCURL(jwksURL); err != nil {
		return nil, errors.New("OIDC jwks_uri must use HTTPS")
	}
	jwksHost, _ := url.Parse(jwksURL)
	if issuerURL.Hostname() == "" || jwksHost.Hostname() == "" {
		return nil, errors.New("OIDC discovery URLs must include a host")
	}

	validator := &OIDCTokenValidator{
		issuer:          cfg.Issuer,
		audience:        cfg.Audience,
		requiredScopes:  uniqueNonEmpty(cfg.RequiredScopes),
		scopeClaim:      strings.TrimSpace(cfg.ScopeClaim),
		tenantClaim:     strings.TrimSpace(cfg.TenantClaim),
		rolesClaim:      strings.TrimSpace(cfg.RolesClaim),
		usernameClaim:   strings.TrimSpace(cfg.UsernameClaim),
		jwksURL:         jwksURL,
		refreshInterval: cfg.JWKSRefreshInterval,
		client:          &httpClient,
	}
	if len(validator.requiredScopes) == 0 {
		return nil, errors.New("at least one non-empty OIDC scope is required")
	}
	if err := validator.refreshKeys(discoveryCtx); err != nil {
		return nil, fmt.Errorf("initial OIDC signing keys could not be loaded")
	}
	return validator, nil
}

func validateOIDCURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("URL must be HTTPS and must not contain user info, query, or fragment")
	}
	return nil
}

func fetchOIDCJSON(ctx context.Context, client *http.Client, endpoint string, maxBytes int64, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OIDC endpoint returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > maxBytes {
		return errors.New("OIDC response exceeded the configured size limit")
	}
	return json.Unmarshal(body, target)
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (v *OIDCTokenValidator) refreshKeys(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, oidcHTTPTimeout)
	defer cancel()
	var document map[string]json.RawMessage
	if err := fetchOIDCJSON(ctx, v.client, v.jwksURL, maxOIDCJWKSBytes, &document); err != nil {
		return err
	}
	var rawKeys []json.RawMessage
	if err := json.Unmarshal(document["keys"], &rawKeys); err != nil || len(rawKeys) == 0 {
		return errors.New("JWKS document contains no keys")
	}
	if len(rawKeys) > maxOIDCJWKSKeyCount {
		return errors.New("JWKS document contains too many keys")
	}
	keys := make([]jose.JSONWebKey, 0, len(rawKeys))
	for _, rawKey := range rawKeys {
		var key jose.JSONWebKey
		if err := json.Unmarshal(rawKey, &key); err != nil {
			continue
		}
		if !key.Valid() || !key.IsPublic() || (key.Use != "" && key.Use != "sig") {
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return errors.New("JWKS document has no usable public signing keys")
	}
	v.keys = keys
	v.refreshedAt = time.Now()
	return nil
}

func (v *OIDCTokenValidator) ValidateMCPToken(ctx context.Context, rawToken string) (*Claims, error) {
	if len(rawToken) == 0 || len(rawToken) > maxOIDCAccessTokenLen {
		return nil, errors.New("invalid bearer token")
	}
	jws, err := jose.ParseSignedCompact(rawToken, allowedOIDCSignatureAlgorithms)
	if err != nil || len(jws.Signatures) != 1 {
		return nil, errors.New("invalid bearer token")
	}
	signature := jws.Signatures[0]
	algorithm := signature.Header.Algorithm
	keyID := signature.Header.KeyID

	v.mu.Lock()
	if time.Since(v.refreshedAt) >= v.refreshInterval {
		if err := v.refreshKeys(ctx); err != nil {
			v.mu.Unlock()
			return nil, errors.New("OIDC signing keys are temporarily unavailable")
		}
	}
	candidates := v.findKeys(keyID, jose.SignatureAlgorithm(algorithm))
	if len(candidates) == 0 && time.Since(v.lastUnknownKIDFetch) >= maxOIDCRefreshBackoff {
		v.lastUnknownKIDFetch = time.Now()
		if err := v.refreshKeys(ctx); err == nil {
			candidates = v.findKeys(keyID, jose.SignatureAlgorithm(algorithm))
		}
	}
	v.mu.Unlock()

	var payload []byte
	verified := false
	for i := range candidates {
		payload, err = jws.Verify(&candidates[i])
		if err == nil {
			verified = true
			break
		}
	}
	if !verified {
		return nil, errors.New("invalid bearer token")
	}
	return v.claimsFromVerifiedPayload(payload)
}

func (v *OIDCTokenValidator) findKeys(kid string, algorithm jose.SignatureAlgorithm) []jose.JSONWebKey {
	var matches []jose.JSONWebKey
	for _, key := range v.keys {
		if kid != "" && key.KeyID != kid {
			continue
		}
		if key.Algorithm != "" && key.Algorithm != string(algorithm) {
			continue
		}
		matches = append(matches, key)
	}
	return matches
}

func (v *OIDCTokenValidator) claimsFromVerifiedPayload(payload []byte) (*Claims, error) {
	var rawClaims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &rawClaims); err != nil {
		return nil, errors.New("invalid bearer token claims")
	}
	issuer, err := stringClaim(rawClaims, "iss", true)
	if err != nil || issuer != v.issuer {
		return nil, errors.New("invalid bearer token issuer")
	}
	subject, err := stringClaim(rawClaims, "sub", true)
	if err != nil {
		return nil, errors.New("access token subject is required")
	}
	audiences, err := audienceClaim(rawClaims["aud"])
	if err != nil || !containsString(audiences, v.audience) {
		return nil, errors.New("access token audience is invalid")
	}
	expiry, err := numericDateClaim(rawClaims["exp"], true)
	if err != nil || expiry <= float64(time.Now().Unix()) {
		return nil, errors.New("access token is expired or has no expiry")
	}
	notBefore, err := numericDateClaim(rawClaims["nbf"], false)
	if err != nil || (notBefore > 0 && notBefore > float64(time.Now().Unix())) {
		return nil, errors.New("access token is not yet valid")
	}
	issuedAt, err := numericDateClaim(rawClaims["iat"], false)
	if err != nil || (issuedAt > 0 && issuedAt > float64(time.Now().Unix()+60)) {
		return nil, errors.New("access token issued-at time is invalid")
	}
	scopes, err := scopeListClaimAtPath(rawClaims, v.scopeClaim)
	if err != nil || len(scopes) == 0 {
		return nil, errors.New("access token scope claim is required")
	}
	scope := strings.Join(scopes, " ")
	for _, required := range v.requiredScopes {
		if !containsString(scopes, required) {
			return nil, errors.New("access token does not grant a required scope")
		}
	}
	tenant, err := stringClaimAtPath(rawClaims, v.tenantClaim)
	if err != nil {
		return nil, errors.New("access token tenant claim is invalid")
	}
	username, err := stringClaimAtPath(rawClaims, v.usernameClaim)
	if err != nil {
		return nil, errors.New("access token username claim is invalid")
	}
	roles, err := stringListClaimAtPath(rawClaims, v.rolesClaim)
	if err != nil {
		return nil, errors.New("access token roles claim is invalid")
	}
	expiryTime := time.Unix(int64(expiry), 0)
	registered := jwt.RegisteredClaims{
		Issuer: issuer, Subject: subject, Audience: jwt.ClaimStrings(audiences),
		ExpiresAt: jwt.NewNumericDate(expiryTime),
	}
	if notBefore > 0 {
		registered.NotBefore = jwt.NewNumericDate(time.Unix(int64(notBefore), 0))
	}
	if issuedAt > 0 {
		registered.IssuedAt = jwt.NewNumericDate(time.Unix(int64(issuedAt), 0))
	}
	return &Claims{
		UserID: subject, TenantID: tenant, Username: username,
		Roles: roles, Scope: scope, RegisteredClaims: registered,
	}, nil
}

func stringClaim(claims map[string]json.RawMessage, name string, required bool) (string, error) {
	raw, exists := claims[name]
	if !exists {
		if required {
			return "", fmt.Errorf("missing claim %s", name)
		}
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	if required && strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("empty claim %s", name)
	}
	return value, nil
}

func stringClaimAtPath(claims map[string]json.RawMessage, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	raw, exists := claimAtPath(claims, path)
	if !exists {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func scopeListClaimAtPath(claims map[string]json.RawMessage, path string) ([]string, error) {
	raw, exists := claimAtPath(claims, path)
	if !exists {
		return nil, errors.New("scope claim not found")
	}
	var scopeString string
	if err := json.Unmarshal(raw, &scopeString); err == nil {
		return uniqueNonEmpty(strings.Fields(scopeString)), nil
	}
	var scopes []string
	if err := json.Unmarshal(raw, &scopes); err != nil {
		return nil, err
	}
	var values []string
	for _, scope := range scopes {
		values = append(values, strings.Fields(scope)...)
	}
	return uniqueNonEmpty(values), nil
}

func stringListClaimAtPath(claims map[string]json.RawMessage, path string) ([]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	raw, exists := claimAtPath(claims, path)
	if !exists || string(raw) == "null" {
		return nil, nil
	}
	var roles []string
	if err := json.Unmarshal(raw, &roles); err == nil {
		return uniqueNonEmpty(roles), nil
	}
	var roleString string
	if err := json.Unmarshal(raw, &roleString); err != nil {
		return nil, err
	}
	return uniqueNonEmpty(strings.Fields(roleString)), nil
}

func claimAtPath(claims map[string]json.RawMessage, path string) (json.RawMessage, bool) {
	parts := strings.Split(path, ".")
	var raw json.RawMessage
	var ok bool
	raw, ok = claims[parts[0]]
	if !ok {
		return nil, false
	}
	for _, part := range parts[1:] {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err != nil {
			return nil, false
		}
		raw, ok = nested[part]
		if !ok {
			return nil, false
		}
	}
	return raw, true
}

func audienceClaim(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, errors.New("missing audience")
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return []string{single}, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func numericDateClaim(raw json.RawMessage, required bool) (float64, error) {
	if len(raw) == 0 {
		if required {
			return 0, errors.New("missing numeric date")
		}
		return 0, nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 253402300799 {
		return 0, errors.New("invalid numeric date")
	}
	return value, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
