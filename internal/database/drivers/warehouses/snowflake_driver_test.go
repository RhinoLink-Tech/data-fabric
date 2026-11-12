package warehouses

import (
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// A zero-value SnowflakeDriver is what the driver registry hands back
// (registry stores &warehouses.SnowflakeDriver{}). BuildDSN must build the DSN
// from the supplied *model.DataSourceConfig, not dereference a nil d.config.
func TestSnowflakeBuildDSNFromConfig(t *testing.T) {
	d := &SnowflakeDriver{} // zero value, exactly as registered
	cfg := &model.DataSourceConfig{
		Host:     "acct.snowflakecomputing.com",
		Port:     443,
		Database: "analytics",
		Schema:   "public",
		Username: "snowman",
		Password: "hunter2",
	}

	dsn := d.BuildDSN(cfg) // must not panic
	if !strings.Contains(dsn, "snowman:hunter2@acct.snowflakecomputing.com/analytics") {
		t.Errorf("BuildDSN(cfg) = %q, want DSN sourced from the passed config (user:pass@host/database)", dsn)
	}
}
