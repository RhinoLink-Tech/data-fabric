package main

import (
	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"testing"
)

func TestWorkbuddyRequiresIsolatedRestrictedConfiguration(t *testing.T) {
	t.Setenv("DATA_FABRIC_WORKBUDDY_TIDB", "")
	t.Setenv("DATA_FABRIC_WORKBUDDY_MARIADB", "")
	t.Setenv("DATA_FABRIC_WORKBUDDY_OPENGAUSS", "")
	t.Setenv("DATA_FABRIC_WORKBUDDY_SOURCE_UPDATES", "")
	t.Setenv("DATA_FABRIC_WORKBUDDY_ONBOARDING", "")
	t.Setenv("DATA_FABRIC_WORKBUDDY_TIDB", "true")
	if _, err := readWorkbuddyConfig(nil); err == nil {
		t.Fatal("TiDB without onboarding accepted")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_TIDB", "invalid")
	if _, err := readWorkbuddyConfig(nil); err == nil {
		t.Fatal("invalid TiDB toggle accepted")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_TIDB", "")
	t.Setenv("DATA_FABRIC_WORKBUDDY_OPENGAUSS", "true")
	if _, err := readWorkbuddyConfig(nil); err == nil {
		t.Fatal("openGauss without onboarding accepted")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_OPENGAUSS", "invalid")
	if _, err := readWorkbuddyConfig(nil); err == nil {
		t.Fatal("invalid openGauss toggle accepted")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_OPENGAUSS", "")
	t.Setenv("DATA_FABRIC_WORKBUDDY_MARIADB", "true")
	if _, err := readWorkbuddyConfig(nil); err == nil {
		t.Fatal("MariaDB without onboarding accepted")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_MARIADB", "invalid")
	if _, err := readWorkbuddyConfig(nil); err == nil {
		t.Fatal("invalid MariaDB toggle accepted")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_MARIADB", "")
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
	t.Setenv("DATA_FABRIC_WORKBUDDY_SOURCE_UPDATES", "true")
	if _, err := readWorkbuddyConfig(nil); err == nil {
		t.Fatal("source updates without onboarding accepted")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_SOURCE_UPDATES", "invalid")
	if _, err := readWorkbuddyConfig(nil); err == nil {
		t.Fatal("invalid source updates accepted")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_SOURCE_UPDATES", "")
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
	if c.policy.AllowsSourceType(model.DatabaseTypeMariaDB) {
		t.Fatal("MariaDB should be off by default")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_MARIADB", "true")
	enabled, err := readWorkbuddyConfig(cfg)
	if err != nil || !enabled.policy.AllowsSourceType(model.DatabaseTypeMariaDB) {
		t.Fatal("explicit MariaDB rejected", err)
	}
	if c.policy.AllowsSourceType(model.DatabaseTypeMariaDB) {
		t.Fatal("existing policy must remain immutable")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_MARIADB", "false")
	if c.policy.AllowsSourceType(model.DatabaseTypeOpenGauss) {
		t.Fatal("openGauss should be off by default")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_OPENGAUSS", "true")
	enabledGauss, err := readWorkbuddyConfig(cfg)
	if database.GetDriverRegistry().IsSupported(model.DatabaseTypeOpenGauss) {
		if err != nil || !enabledGauss.policy.AllowsSourceType(model.DatabaseTypeOpenGauss) {
			t.Fatal("explicit openGauss rejected", err)
		}
	} else if err == nil {
		t.Fatal("build without openGauss driver accepted opt-in")
	}
	if c.policy.AllowsSourceType(model.DatabaseTypeOpenGauss) {
		t.Fatal("existing policy must remain immutable")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_OPENGAUSS", "false")
	if c.policy.AllowsSourceType(model.DatabaseTypeTiDB) {
		t.Fatal("TiDB should be off by default")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_TIDB", "true")
	enabledTiDB, err := readWorkbuddyConfig(cfg)
	if database.GetDriverRegistry().IsSupported(model.DatabaseTypeTiDB) {
		if err != nil || !enabledTiDB.policy.AllowsSourceType(model.DatabaseTypeTiDB) {
			t.Fatal("explicit TiDB rejected", err)
		}
	} else if err == nil {
		t.Fatal("build without TiDB driver accepted opt-in")
	}
	if c.policy.AllowsSourceType(model.DatabaseTypeTiDB) {
		t.Fatal("existing policy must remain immutable")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_TIDB", "false")
	if c.sourceUpdates {
		t.Fatal("source updates should be off by default")
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_SOURCE_UPDATES", "true")
	c, err = readWorkbuddyConfig(cfg)
	if err != nil || !c.sourceUpdates {
		t.Fatal("explicit source updates rejected", err)
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_SOURCE_UPDATES", "false")
	c, err = readWorkbuddyConfig(cfg)
	if err != nil || c.sourceUpdates {
		t.Fatal("source updates not disabled", err)
	}
	t.Setenv("DATA_FABRIC_WORKBUDDY_ALLOWED_PORTS", "5432,garbage")
	if _, err = readWorkbuddyConfig(cfg); err == nil {
		t.Fatal("invalid port accepted")
	}
}
