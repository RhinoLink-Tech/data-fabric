package object_storage

import (
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func cosDeltaTestConfig() *model.DataSourceConfig {
	return &model.DataSourceConfig{
		Host: "warehouse/events", Port: 443, Database: "analytics",
		Username: "secret-id", Password: "secret-key",
		AdditionalProps: map[string]interface{}{
			"region": "ap-guangzhou", "endpoint_url": "http://cos.example.test",
			"table_path": "warehouse/events",
		},
	}
}

func TestValidateCOSDeltaConfig(t *testing.T) {
	config := cosDeltaTestConfig()
	if err := ValidateCOSDeltaConfig(config); err != nil {
		t.Fatalf("valid COS Delta config rejected: %v", err)
	}
	if got, err := COSDeltaTableName(config); err != nil || got != "events" {
		t.Fatalf("COS Delta table name = %q, error = %v", got, err)
	}
	driver := &COSDeltaDriver{}
	if got := driver.BuildDSN(config); got != "cos-delta://analytics/warehouse/events" {
		t.Fatalf("COS Delta DSN = %q", got)
	}
	if err := driver.ValidateDSN("cos-delta://analytics/warehouse/events"); err != nil {
		t.Fatalf("valid COS Delta DSN rejected: %v", err)
	}
}

func TestValidateCOSDeltaConfigRejectsUnsafeOrUnsupportedValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{name: "missing bucket", mutate: func(c *model.DataSourceConfig) { c.Database = "" }, want: "bucket"},
		{name: "unsafe bucket", mutate: func(c *model.DataSourceConfig) { c.Database = "bucket/path" }, want: "bucket"},
		{name: "missing secret ID", mutate: func(c *model.DataSourceConfig) { c.Username = "" }, want: "secret ID"},
		{name: "missing secret key", mutate: func(c *model.DataSourceConfig) { c.Password = "" }, want: "secret key"},
		{name: "missing region", mutate: func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "region") }, want: "region"},
		{name: "empty table path", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["table_path"] = ""; c.Host = "" }, want: "table_path"},
		{name: "absolute table path", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["table_path"] = "/warehouse/events" }, want: "table_path"},
		{name: "traversal", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["table_path"] = "warehouse/../outside" }, want: "table_path"},
		{name: "endpoint path", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["endpoint_url"] = "http://cos.example.test/gateway" }, want: "endpoint_url"},
		{name: "endpoint credentials", mutate: func(c *model.DataSourceConfig) {
			c.AdditionalProps["endpoint_url"] = "http://user:pass@cos.example.test"
		}, want: "endpoint_url"},
		{name: "bad byte limit type", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["max_table_bytes"] = "large" }, want: "max_table_bytes"},
		{name: "byte limit exceeds maximum", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["max_table_bytes"] = maxCOSDeltaTableBytes + 1 }, want: "max_table_bytes"},
		{name: "invalid batch size", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["batch_size"] = 10_001 }, want: "batch_size"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := cosDeltaTestConfig()
			test.mutate(config)
			if err := ValidateCOSDeltaConfig(config); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateCOSDeltaConfig() error = %v, want substring %q", err, test.want)
			}
		})
	}
}
