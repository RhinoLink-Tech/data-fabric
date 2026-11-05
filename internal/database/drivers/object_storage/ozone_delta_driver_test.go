package object_storage

import (
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func ozoneDeltaTestConfig() *model.DataSourceConfig {
	return &model.DataSourceConfig{
		Host: "warehouse/events", Port: 9878, Database: "analytics",
		Username: "access-key", Password: "secret-key",
		AdditionalProps: map[string]interface{}{
			"endpoint_url": "http://ozone.example.test", "table_path": "warehouse/events",
		},
	}
}

func TestValidateOzoneDeltaConfig(t *testing.T) {
	config := ozoneDeltaTestConfig()
	if err := ValidateOzoneDeltaConfig(config); err != nil {
		t.Fatalf("valid Ozone Delta config rejected: %v", err)
	}
	if got, err := OzoneDeltaEndpointFromConfig(config); err != nil || got != "http://ozone.example.test:9878" {
		t.Fatalf("Ozone Delta endpoint = %q, error = %v", got, err)
	}
	if got, err := OzoneDeltaTableName(config); err != nil || got != "events" {
		t.Fatalf("Ozone Delta table name = %q, error = %v", got, err)
	}
	if got := (&OzoneDeltaDriver{}).BuildDSN(config); got != "ozone-s3://analytics/warehouse/events" {
		t.Fatalf("Ozone Delta DSN = %q", got)
	}
	if err := (&OzoneDeltaDriver{}).ValidateDSN("ozone-s3://analytics/warehouse/events"); err != nil {
		t.Fatalf("valid Ozone Delta DSN rejected: %v", err)
	}
}

func TestValidateOzoneDeltaConfigRejectsUnsafeOrUnsupportedValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{name: "missing bucket", mutate: func(c *model.DataSourceConfig) { c.Database = "" }, want: "bucket"},
		{name: "unsafe bucket", mutate: func(c *model.DataSourceConfig) { c.Database = "bucket/path" }, want: "bucket"},
		{name: "missing access key", mutate: func(c *model.DataSourceConfig) { c.Username = "" }, want: "access key"},
		{name: "missing secret", mutate: func(c *model.DataSourceConfig) { c.Password = "" }, want: "access key"},
		{name: "empty table path", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["table_path"] = ""; c.Host = "" }, want: "table_path"},
		{name: "absolute table path", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["table_path"] = "/warehouse/events" }, want: "table_path"},
		{name: "traversal", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["table_path"] = "warehouse/../outside" }, want: "table_path"},
		{name: "backslash", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["table_path"] = `warehouse\\events` }, want: "table_path"},
		{name: "endpoint path", mutate: func(c *model.DataSourceConfig) {
			c.AdditionalProps["endpoint_url"] = "http://ozone.example.test/gateway"
		}, want: "endpoint_url"},
		{name: "endpoint credentials", mutate: func(c *model.DataSourceConfig) {
			c.AdditionalProps["endpoint_url"] = "http://user:pass@ozone.example.test"
		}, want: "endpoint_url"},
		{name: "invalid port", mutate: func(c *model.DataSourceConfig) { c.Port = 70000 }, want: "port"},
		{name: "bad byte limit type", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["max_table_bytes"] = "large" }, want: "max_table_bytes"},
		{name: "byte limit exceeds maximum", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["max_table_bytes"] = maxOzoneDeltaTableBytes + 1 }, want: "max_table_bytes"},
		{name: "invalid batch size", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["batch_size"] = 10_001 }, want: "batch_size"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := ozoneDeltaTestConfig()
			test.mutate(config)
			if err := ValidateOzoneDeltaConfig(config); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateOzoneDeltaConfig() error = %v, want substring %q", err, test.want)
			}
		})
	}
}
