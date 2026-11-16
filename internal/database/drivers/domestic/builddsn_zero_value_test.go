package domestic

import (
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// The driver registry hands back a zero-value *Driver{} (d.config == nil).
// BuildDSN on those drivers must build the DSN from the supplied
// *model.DataSourceConfig and must not dereference a nil d.config, which
// panics every registry-sourced BuildDSN call (same trap the snowflake and
// gaussdb drivers already fixed).

func bootstrapConfig() *model.DataSourceConfig {
	return &model.DataSourceConfig{
		Host:     "host",
		Port:     3306,
		Database: "prod",
		Username: "app",
		Password: "secret",
	}
}

func TestTDSQLBuildDSNFromConfig(t *testing.T) {
	d := &TDSQLDriver{}                  // zero value, exactly as registered
	dsn := d.BuildDSN(bootstrapConfig()) // must not panic
	if !strings.Contains(dsn, "app:secret@tcp(host:3306)/prod?charset=utf8mb4") {
		t.Errorf("BuildDSN(cfg) = %q, want DSN sourced from the passed config", dsn)
	}
}

func TestOscarBuildDSNFromConfig(t *testing.T) {
	d := &OscarDriver{}
	dsn := d.BuildDSN(bootstrapConfig()) // must not panic
	if !strings.Contains(dsn, "host=host port=3306 dbname=prod user=app password=secret") {
		t.Errorf("BuildDSN(cfg) = %q, want DSN sourced from the passed config", dsn)
	}
	if !strings.Contains(dsn, "client_encoding=UTF8") {
		t.Errorf("BuildDSN(cfg) = %q, want default charset when driver has no config", dsn)
	}
}

func TestKingbaseesBuildDSNFromConfig(t *testing.T) {
	d := &KingbaseESDriver{}
	dsn := d.BuildDSN(bootstrapConfig()) // must not panic
	if !strings.Contains(dsn, "host=host port=3306 dbname=prod user=app password=secret") {
		t.Errorf("BuildDSN(cfg) = %q, want DSN sourced from the passed config", dsn)
	}
	if !strings.Contains(dsn, "sslmode=disable") {
		t.Errorf("BuildDSN(cfg) = %q, want sslmode=disable default when driver has no config", dsn)
	}
}

func TestGBaseBuildDSNFromConfig(t *testing.T) {
	d := &GBaseDriver{}
	dsn := d.BuildDSN(bootstrapConfig()) // must not panic
	if !strings.Contains(dsn, "app:secret@tcp(host:3306)/prod?charset=utf8") {
		t.Errorf("BuildDSN(cfg) = %q, want DSN sourced from the passed config", dsn)
	}
}
