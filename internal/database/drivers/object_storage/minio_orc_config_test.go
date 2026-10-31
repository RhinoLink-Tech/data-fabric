package object_storage

import (
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestMinIOORCConfigMapsS3CompatibleSettings(t *testing.T) {
	config := &model.DataSourceConfig{
		Host: "warehouse/records.orc", Database: "analytics",
		Username: "minio-access", Password: "minio-secret",
		AdditionalProps: map[string]interface{}{
			"endpoint_url": "http://127.0.0.1:9000", "table_name": "records",
			"session_token": "temporary-token", "region": "us-west-2",
		},
	}
	normalized, err := minIOORCDataSourceConfig(config)
	if err != nil {
		t.Fatalf("normalize MinIO ORC source: %v", err)
	}
	if normalized.Host != "warehouse/records.orc" {
		t.Errorf("object key = %q, want warehouse/records.orc", normalized.Host)
	}
	for key, want := range map[string]string{
		"object_key": "warehouse/records.orc", "endpoint_url": "http://127.0.0.1:9000",
		"region": "us-west-2", "session_token": "temporary-token",
	} {
		if got := configString(normalized.AdditionalProps, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if !configBool(normalized.AdditionalProps, "force_path_style", false) {
		t.Fatal("MinIO must default to S3 path-style addressing")
	}
	if config.AdditionalProps["force_path_style"] != nil {
		t.Fatal("normalization mutated the caller's AdditionalProps")
	}
	if err := ValidateMinIOORCConfig(config); err != nil {
		t.Fatalf("validate MinIO ORC source: %v", err)
	}
	driver := &MinIOORCDriver{}
	if err := driver.ValidateDSN(driver.BuildDSN(config)); err != nil {
		t.Fatalf("validate generated MinIO ORC DSN: %v", err)
	}
}

func TestMinIOORCConfigRejectsInvalidSettings(t *testing.T) {
	base := &model.DataSourceConfig{
		Host: "records.orc", Database: "analytics", Username: "access", Password: "secret",
		AdditionalProps: map[string]interface{}{
			"endpoint_url": "http://127.0.0.1:9000", "table_name": "records",
		},
	}
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
	}{
		{name: "missing bucket", mutate: func(c *model.DataSourceConfig) { c.Database = "" }},
		{name: "missing access key", mutate: func(c *model.DataSourceConfig) { c.Username = "" }},
		{name: "missing secret key", mutate: func(c *model.DataSourceConfig) { c.Password = "" }},
		{name: "missing object key", mutate: func(c *model.DataSourceConfig) { c.Host = "" }},
		{name: "missing endpoint", mutate: func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "endpoint_url") }},
		{name: "endpoint with path", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["endpoint_url"] = "http://127.0.0.1:9000/prefix" }},
		{name: "endpoint with credentials", mutate: func(c *model.DataSourceConfig) {
			c.AdditionalProps["endpoint_url"] = "http://user:secret@127.0.0.1:9000"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := *base
			config.AdditionalProps = make(map[string]interface{}, len(base.AdditionalProps))
			for key, value := range base.AdditionalProps {
				config.AdditionalProps[key] = value
			}
			test.mutate(&config)
			if err := ValidateMinIOORCConfig(&config); err == nil {
				t.Fatal("invalid MinIO ORC config was accepted")
			}
		})
	}
}
