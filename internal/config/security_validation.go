package config

import (
	"fmt"
	"strings"
)

// knownPlaceholderSecrets intercepts placeholder JWT secrets that shipped in
// code defaults, docs, or the historical default config. The list only
// catches known defaults; real strength is enforced by minJWTSecretLength.
var knownPlaceholderSecrets = map[string]struct{}{
	"your-secret-key": {},
	"your-super-secret-jwt-key-change-in-production": {},
	"your-jwt-secret": {},
	"change-me":       {},
	"changeme":        {},
	"secret":          {},
}

// minJWTSecretLength matches the HMAC-SHA256 digest size recommendation.
const minJWTSecretLength = 32

const jwtSecretHelp = "set the JWT_SECRET environment variable or jwt_secret in configs/config.yaml (generate one with: openssl rand -base64 32)"

// ValidateSecurity fails closed on insecure authentication settings:
// authentication cannot be disabled in release mode, and the JWT secret must
// be real whenever the HTTP server runs — even with authentication disabled —
// because the fetch API always authenticates. It runs before the metadata
// database is touched so misconfiguration is reported without side effects.
// Only the HTTP server calls it; MCP stdio mode does not serve HTTP routes.
func (c *Config) ValidateSecurity() error {
	if c.Server.Mode == "release" && !c.Security.EnableAuth {
		return fmt.Errorf("security.enable_auth cannot be disabled in release mode: enable authentication or run an explicit non-release mode")
	}

	// The /fetch API requires a signed JWT regardless of security.enable_auth
	// (#6), so a forgeable secret is never acceptable on the HTTP path.
	secret := strings.TrimSpace(c.Security.JWTSecret)
	if secret == "" {
		return fmt.Errorf("security.jwt_secret is required even when security.enable_auth is false (the /fetch API always authenticates): %s", jwtSecretHelp)
	}
	if _, placeholder := knownPlaceholderSecrets[strings.ToLower(secret)]; placeholder {
		return fmt.Errorf("security.jwt_secret is a known placeholder value: %s", jwtSecretHelp)
	}
	if len(secret) < minJWTSecretLength {
		return fmt.Errorf("security.jwt_secret must be at least %d bytes (the /fetch API always authenticates): %s", minJWTSecretLength, jwtSecretHelp)
	}

	if !c.Security.EnableAuth {
		return nil
	}
	if c.Security.JWTExpiration <= 0 {
		return fmt.Errorf("security.jwt_expiration must be positive when security.enable_auth is true")
	}
	return nil
}
