package object_storage

import (
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestMinIOIcebergConfigMapsCatalogAndStorageSettings(t *testing.T) {
	config := &model.DataSourceConfig{
		Host: "http://catalog.example:8181/api", Database: "warehouse", Schema: "sales",
		Username: "minio-access", Password: "minio-secret",
		AdditionalProps: map[string]interface{}{
			"table_name": "orders", "endpoint_url": "http://minio.example:9000",
			"region": "us-west-2", "session_token": "temporary-token",
			"force_path_style": true, "catalog_token": "catalog-token",
		},
	}
	normalized, err := minIOIcebergDataSourceConfig(config)
	if err != nil {
		t.Fatalf("normalize MinIO Iceberg source: %v", err)
	}
	for key, want := range map[string]string{
		"catalog_uri":                 "http://catalog.example:8181/api",
		"warehouse":                   "warehouse",
		"namespace":                   "sales",
		"table_name":                  "orders",
		"s3.endpoint":                 "http://minio.example:9000",
		"s3.region":                   "us-west-2",
		"s3.access-key-id":            "minio-access",
		"s3.secret-access-key":        "minio-secret",
		"s3.session-token":            "temporary-token",
		"s3.force-virtual-addressing": "false",
		"token":                       "catalog-token",
	} {
		if got := configString(normalized.AdditionalProps, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if normalized.Username != "" || normalized.Password != "" {
		t.Errorf("MinIO credentials must not be forwarded as REST catalog credentials: username=%q password=%q", normalized.Username, normalized.Password)
	}
	if config.AdditionalProps["s3.endpoint"] != nil {
		t.Fatal("normalization mutated the caller's AdditionalProps")
	}
	if err := ValidateMinIOIcebergConfig(config); err != nil {
		t.Fatalf("validate normalized MinIO Iceberg source: %v", err)
	}
}

func TestMinIOIcebergConfigRejectsIncompleteOrUnsafeSources(t *testing.T) {
	base := &model.DataSourceConfig{
		Host: "http://catalog.example:8181", Database: "warehouse", Schema: "sales",
		Username: "access", Password: "secret",
		AdditionalProps: map[string]interface{}{
			"table_name": "orders", "endpoint_url": "http://minio.example:9000",
		},
	}
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
	}{
		{name: "missing table", mutate: func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "table_name") }},
		{name: "qualified table", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["table_name"] = "sales.orders" }},
		{name: "catalog credentials in URL", mutate: func(c *model.DataSourceConfig) { c.Host = "http://user:secret@catalog.example" }},
		{name: "endpoint path", mutate: func(c *model.DataSourceConfig) {
			c.AdditionalProps["endpoint_url"] = "http://minio.example:9000/prefix"
		}},
		{name: "missing access key", mutate: func(c *model.DataSourceConfig) { c.Username = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := *base
			config.AdditionalProps = make(map[string]interface{}, len(base.AdditionalProps))
			for key, value := range base.AdditionalProps {
				config.AdditionalProps[key] = value
			}
			test.mutate(&config)
			if err := ValidateMinIOIcebergConfig(&config); err == nil {
				t.Fatal("invalid MinIO Iceberg config was accepted")
			}
		})
	}
}
