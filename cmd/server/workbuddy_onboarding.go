package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/mcp"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/workbuddy/onboardingruntime"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
)

// This is an intentionally isolated, instance-wide deployment mode. It cannot
// coexist with a separately running unrestricted API over the same sources.
// All nodes must restart/drain to change the fixed-IP policy.
type workbuddyConfig struct {
	origin, clientID, clientSecret, authURL, tokenURL string
	cidrs                                             []string
	ports                                             []int
	policy                                            *database.FixedIPEgressPolicy
	bridge                                            bool
	sourceUpdates                                     bool
}

func readWorkbuddyConfig(cfg *config.Config) (*workbuddyConfig, error) {
	updates := strings.TrimSpace(os.Getenv("DATA_FABRIC_WORKBUDDY_SOURCE_UPDATES"))
	if updates != "" && updates != "false" && updates != "true" {
		return nil, errors.New("invalid WorkBuddy source updates toggle")
	}
	bridge := strings.TrimSpace(os.Getenv("DATA_FABRIC_WORKBUDDY_BRIDGE"))
	if bridge != "" && bridge != "false" && bridge != "true" {
		return nil, errors.New("invalid WorkBuddy bridge toggle")
	}
	raw := strings.TrimSpace(os.Getenv("DATA_FABRIC_WORKBUDDY_ONBOARDING"))
	if raw == "" || raw == "false" {
		if bridge == "true" {
			return nil, errors.New("WorkBuddy bridge requires onboarding")
		}
		if updates == "true" {
			return nil, errors.New("WorkBuddy source updates require onboarding")
		}
		return nil, nil
	}
	if raw != "true" {
		return nil, errors.New("invalid WorkBuddy onboarding toggle")
	}
	if cfg == nil || cfg.MCP.Transport != config.MCPTransportStreamableHTTP || !strings.EqualFold(cfg.MCP.AuthMode, config.MCPAuthModeOIDC) || !cfg.Audit.Enabled {
		return nil, errors.New("WorkBuddy onboarding requires streamable HTTP, OIDC and audit")
	}
	host, _, err := net.SplitHostPort(cfg.MCP.Address)
	if err != nil {
		return nil, errors.New("WorkBuddy listener must bind loopback")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("WorkBuddy listener must bind loopback")
	}
	c := &workbuddyConfig{sourceUpdates: updates == "true", bridge: bridge == "true", origin: os.Getenv("DATA_FABRIC_WORKBUDDY_ORIGIN"), clientID: os.Getenv("DATA_FABRIC_WORKBUDDY_OIDC_CLIENT_ID"), clientSecret: os.Getenv("DATA_FABRIC_WORKBUDDY_OIDC_CLIENT_SECRET"), authURL: os.Getenv("DATA_FABRIC_WORKBUDDY_OIDC_AUTH_URL"), tokenURL: os.Getenv("DATA_FABRIC_WORKBUDDY_OIDC_TOKEN_URL")}
	if c.origin == "" || c.clientID == "" || c.authURL == "" || c.tokenURL == "" || cfg.MCP.OIDCIssuer == "" {
		return nil, errors.New("WorkBuddy OIDC configuration incomplete")
	}
	for _, part := range strings.Split(os.Getenv("DATA_FABRIC_WORKBUDDY_ALLOWED_CIDRS"), ",") {
		if s := strings.TrimSpace(part); s != "" {
			c.cidrs = append(c.cidrs, s)
		}
	}
	for _, part := range strings.Split(os.Getenv("DATA_FABRIC_WORKBUDDY_ALLOWED_PORTS"), ",") {
		n, e := strconv.Atoi(strings.TrimSpace(part))
		if e != nil || n < 1 || n > 65535 {
			return nil, errors.New("invalid WorkBuddy allowed ports")
		}
		c.ports = append(c.ports, n)
	}
	c.policy, err = database.NewFixedIPEgressPolicyWithCA(c.cidrs, c.ports, strings.TrimSpace(os.Getenv("DATA_FABRIC_WORKBUDDY_CA_FILE")))
	if err != nil {
		return nil, errors.New("invalid WorkBuddy egress policy")
	}
	return c, nil
}
func (c *workbuddyConfig) start(ctx context.Context, cfg *config.Config, db *gorm.DB, vault *security.CredentialVault, pool *database.ConnectionPool, server *mcp.MCPServer) (*onboardingruntime.Runtime, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	verifier, err := security.NewOIDCIDTokenVerifier(ctx, cfg.MCP.OIDCIssuer, c.clientID, client)
	if err != nil {
		return nil, errors.New("WorkBuddy OIDC unavailable")
	}
	oauth := oauth2.Config{ClientID: c.clientID, ClientSecret: c.clientSecret, RedirectURL: c.origin + "/onboarding/auth/callback", Scopes: []string{"openid"}, Endpoint: oauth2.Endpoint{AuthURL: c.authURL, TokenURL: c.tokenURL}}
	return onboardingruntime.New(ctx, onboardingruntime.Options{Enabled: true, EnableUpdates: c.sourceUpdates, Origin: c.origin, MCPIssuer: cfg.MCP.OIDCIssuer, AuditKey: cfg.Security.JWTSecret, CIDRs: c.cidrs, Ports: c.ports, DB: db, Vault: vault, Validator: database.NewHealthChecker(pool), MCP: server, Fallback: http.NotFoundHandler(), OAuth: oauth, Verifier: verifier, HTTPClient: client, Pool: pool, Policy: c.policy})
}
