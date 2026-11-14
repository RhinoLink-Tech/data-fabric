package main

import (
	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"testing"
)

func TestWorkbuddyRequiresIsolatedRestrictedConfiguration(t *testing.T) {
	t.Setenv("DATA_FABRIC_WORKBUDDY_ONBOARDING", "")
	t.Setenv("DATA_FABRIC_WORKBUDDY_BRIDGE", "true")
	if _, err := readWorkbuddyConfig(nil); err == nil {
		t.Fatal("bridge without onboarding accepted")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_BRIDGE", "invalid")
	if _, err := readWorkbuddyConfig(nil); err == nil {
		t.Fatal("invalid bridge toggle accepted")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_BRIDGE", "")
	c, err := readWorkbuddyConfig(nil)
	if err != nil || c != nil {
		t.Fatal("default must stay disabled")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_ONBOARDING", "true")
	cfg := &config.Config{}
	if _, err := readWorkbuddyConfig(cfg); err == nil {
		t.Fatal("unsafe configuration accepted")
	}
	cfg.MCP.Transport = config.MCPTransportStreamableHTTP
	cfg.MCP.AuthMode = config.MCPAuthModeOIDC
	cfg.MCP.OIDCIssuer = "https://issuer.test"
	cfg.MCP.Address = "0.0.0.0:8081"
	cfg.Audit.Enabled = true
	if _, err := readWorkbuddyConfig(cfg); err == nil {
		t.Fatal("public listener accepted")
	}
	cfg.MCP.Address = "127.0.0.1:8081"
	t.Setenv("DATA_FABRIC_WORKBUDDY_ORIGIN", "https://fabric.example")
	t.Setenv("DATA_FABRIC_WORKBUDDY_OIDC_CLIENT_ID", "browser")
	t.Setenv("DATA_FABRIC_WORKBUDDY_OIDC_AUTH_URL", "https://issuer.test/auth")
	t.Setenv("DATA_FABRIC_WORKBUDDY_OIDC_TOKEN_URL", "https://issuer.test/token")
	t.Setenv("DATA_FABRIC_WORKBUDDY_ALLOWED_CIDRS", "127.0.0.0/8")
	t.Setenv("DATA_FABRIC_WORKBUDDY_ALLOWED_PORTS", "5432,3306")
	t.Setenv("DATA_FABRIC_WORKBUDDY_BRIDGE", "true")
	c, err = readWorkbuddyConfig(cfg)
	if err != nil || c == nil || c.policy == nil || !c.bridge {
		t.Fatal("valid config rejected", err)
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_ALLOWED_PORTS", "5432,garbage")
	if _, err = readWorkbuddyConfig(cfg); err == nil {
		t.Fatal("invalid port accepted")
	}
}
