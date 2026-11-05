package object_storage

import (
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestCOSIcebergConfigMapsCatalogAndVirtualHostStorageSettings(t *testing.T) {
	config := &model.DataSourceConfig{
		Host: "http://catalog.example:8181/api", Database: "warehouse", Schema: "sales",
		Username: "secret-id", Password: "secret-key",
		AdditionalProps: map[string]interface{}{
			"table_name": "orders", "region": "ap-guangzhou", "catalog_token": "catalog-token",
		},
	}
	normalized, err := cosIcebergDataSourceConfig(config)
	if err != nil {
		t.Fatalf("normalize COS Iceberg source: %v", err)
	}
	for key, want := range map[string]string{
		"catalog_uri":                 "http://catalog.example:8181/api",
		"warehouse":                   "warehouse",
		"namespace":                   "sales",
		"table_name":                  "orders",
		"s3.endpoint":                 "https://cos.ap-guangzhou.myqcloud.com",
		"s3.region":                   "ap-guangzhou",
		"s3.access-key-id":            "secret-id",
		"s3.secret-access-key":        "secret-key",
		"s3.force-virtual-addressing": "true",
		"token":                       "catalog-token",
	} {
		if got := configString(normalized.AdditionalProps, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if normalized.Username != "" || normalized.Password != "" {
		t.Errorf("COS credentials must not be forwarded as REST catalog credentials: username=%q password=%q", normalized.Username, normalized.Password)
	}
	if config.AdditionalProps["s3.endpoint"] != nil {
		t.Fatal("normalization mutated the caller's AdditionalProps")
	}
	if err := ValidateCOSIcebergConfig(config); err != nil {
		t.Fatalf("validate COS Iceberg source: %v", err)
	}
}

func TestCOSIcebergConfigSupportsCustomEndpointAndPathStyleOverride(t *testing.T) {
	config := &model.DataSourceConfig{
		Host: "https://catalog.example", Database: "warehouse", Schema: "sales",
		Username: "secret-id", Password: "secret-key",
		AdditionalProps: map[string]interface{}{
			"table_name": "orders", "region": "ap-guangzhou",
			"endpoint_url": "http://127.0.0.1:9000", "force_path_style": true,
			"session_token": "temporary-token", "catalog_credential": "catalog-user:catalog-secret",
		},
	}
	normalized, err := cosIcebergDataSourceConfig(config)
	if err != nil {
		t.Fatalf("normalize COS-compatible test endpoint: %v", err)
	}
	for key, want := range map[string]string{
		"s3.endpoint":                 "http://127.0.0.1:9000",
		"s3.force-virtual-addressing": "false",
		"s3.session-token":            "temporary-token",
		"credential":                  "catalog-user:catalog-secret",
	} {
		if got := configString(normalized.AdditionalProps, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestCOSIcebergConfigRejectsIncompleteOrUnsafeSources(t *testing.T) {
	base := &model.DataSourceConfig{
		Host: "http://catalog.example:8181", Database: "warehouse", Schema: "sales",
		Username: "secret-id", Password: "secret-key",
		AdditionalProps: map[string]interface{}{
			"table_name": "orders", "region": "ap-guangzhou", "endpoint_url": "https://cos.ap-guangzhou.myqcloud.com",
		},
	}
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
	}{
		{name: "missing table", mutate: func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "table_name") }},
		{name: "qualified table", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["table_name"] = "sales.orders" }},
		{name: "catalog credentials in URL", mutate: func(c *model.DataSourceConfig) { c.Host = "http://user:secret@catalog.example" }},
		{name: "endpoint path", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["endpoint_url"] = "https://cos.example/prefix" }},
		{name: "missing SecretId", mutate: func(c *model.DataSourceConfig) { c.Username = "" }},
		{name: "missing region", mutate: func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "region") }},
		{name: "invalid region", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["region"] = "../other" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := *base
			config.AdditionalProps = make(map[string]interface{}, len(base.AdditionalProps))
			for key, value := range base.AdditionalProps {
				config.AdditionalProps[key] = value
			}
			test.mutate(&config)
			if err := ValidateCOSIcebergConfig(&config); err == nil {
				t.Fatal("invalid COS Iceberg config was accepted")
			}
		})
	}
}
