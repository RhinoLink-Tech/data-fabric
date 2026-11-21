package main

import (
	"context"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
)

func TestLegacyMCPAudienceRequiresMatchingDiscovery(t *testing.T) {
	for _, tc := range []struct{ name, auth, resource string }{
		{"missing discovery", config.MCPAuthModeOIDC, ""},
		{"different resource", config.MCPAuthModeOIDC, "https://other.example/mcp"},
		{"local JWT", config.MCPAuthModeLocalJWT, "https://fabric.example/mcp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATA_FABRIC_MCP_OAUTH_RESOURCE", tc.resource)
			cfg := &config.Config{MCP: config.MCPConfig{AuthMode: tc.auth, OIDCAudience: "https://fabric.example/mcp", OIDCLegacyAudience: "old"}}
			if _, err := newMCPTokenValidator(context.Background(), cfg); err == nil {
				t.Fatal("unsafe legacy audience accepted")
			}
		})
	}
}
