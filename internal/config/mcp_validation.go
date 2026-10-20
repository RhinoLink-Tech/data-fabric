package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

const (
	MCPTransportStdio          = "stdio"
	MCPTransportStreamableHTTP = "streamable-http"
	MCPAuthModeLocalJWT        = "local-jwt"
	MCPAuthModeOIDC            = "oidc"
)

// Validate checks the selected MCP transport and prevents an unencrypted
// remote listener from being exposed. Plain HTTP is limited to loopback so a
// TLS reverse proxy can terminate HTTPS in front of the process.
func (c MCPConfig) Validate() error {
	transport := strings.TrimSpace(c.Transport)
	if transport == "" {
		transport = MCPTransportStdio
	}
	if transport == MCPTransportStdio {
		if strings.EqualFold(strings.TrimSpace(c.AuthMode), MCPAuthModeOIDC) {
			return fmt.Errorf("mcp.auth_mode oidc requires the streamable-http transport")
		}
		return nil
	}
	if transport != MCPTransportStreamableHTTP {
		return fmt.Errorf("unsupported MCP transport %q: use %q or %q", transport, MCPTransportStdio, MCPTransportStreamableHTTP)
	}

	authMode := strings.TrimSpace(c.AuthMode)
	if authMode == "" {
		authMode = MCPAuthModeLocalJWT
	}
	switch authMode {
	case MCPAuthModeLocalJWT:
	case MCPAuthModeOIDC:
		if err := validateMCPOIDCConfig(c); err != nil {
			return err
		}
	default:
		return fmt.Errorf("mcp.auth_mode must be local-jwt or oidc")
	}

	certConfigured := strings.TrimSpace(c.TLSCertFile) != ""
	keyConfigured := strings.TrimSpace(c.TLSKeyFile) != ""
	if certConfigured != keyConfigured {
		return fmt.Errorf("mcp.tls_cert_file and mcp.tls_key_file must be configured together")
	}

	address := strings.TrimSpace(c.Address)
	if address == "" {
		return fmt.Errorf("mcp.address is required for the Streamable HTTP transport")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid mcp.address %q: %w", address, err)
	}
	if err := validatePort("mcp.address port", port); err != nil {
		return err
	}
	if !certConfigured && !isLoopbackHost(host) {
		return fmt.Errorf("MCP Streamable HTTP without direct TLS must bind to loopback; expose it through an HTTPS reverse proxy or configure both TLS files")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateMCPOIDCConfig(c MCPConfig) error {
	issuer := strings.TrimSpace(c.OIDCIssuer)
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("mcp.oidc_issuer must be an HTTPS issuer URL without user info, query, or fragment")
	}
	if strings.TrimSpace(c.OIDCAudience) == "" {
		return fmt.Errorf("mcp.oidc_audience is required when mcp.auth_mode is oidc")
	}
	if strings.TrimSpace(c.OIDCRequiredScopes) == "" {
		return fmt.Errorf("mcp.oidc_required_scopes must contain at least one required scope")
	}
	if c.OIDCJWKSRefreshInterval < time.Minute || c.OIDCJWKSRefreshInterval > time.Hour {
		return fmt.Errorf("mcp.oidc_jwks_refresh_interval must be between 1m and 1h")
	}
	for name, claim := range map[string]string{
		"mcp.oidc_tenant_claim":   c.OIDCTenantClaim,
		"mcp.oidc_roles_claim":    c.OIDCRolesClaim,
		"mcp.oidc_username_claim": c.OIDCUsernameClaim,
	} {
		if strings.TrimSpace(claim) == "" {
			return fmt.Errorf("%s must not be empty when OIDC is enabled", name)
		}
	}
	return nil
}
