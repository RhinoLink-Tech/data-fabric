package object_storage

import (
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestValidateS3DeltaConfig(t *testing.T) {
	valid := func() *model.DataSourceConfig {
		return &model.DataSourceConfig{
			Host: "warehouse/events", Database: "analytics",
			Username: "access", Password: "secret",
			AdditionalProps: map[string]interface{}{
				"endpoint_url": "https://s3.example.test", "force_path_style": true,
			},
		}
	}

	for _, tt := range []struct {
		name string
		edit func(*model.DataSourceConfig)
		want string
	}{
		{name: "valid static credentials"},
		{name: "valid default credential chain", edit: func(c *model.DataSourceConfig) { c.Username, c.Password = "", "" }},
		{name: "missing bucket", edit: func(c *model.DataSourceConfig) { c.Database = " " }, want: "bucket"},
		{name: "missing table path", edit: func(c *model.DataSourceConfig) { c.Host = "" }, want: "table_path"},
		{name: "escaping table path", edit: func(c *model.DataSourceConfig) { c.Host = "../events" }, want: "path segment"},
		{name: "partial static credentials", edit: func(c *model.DataSourceConfig) { c.Username = "" }, want: "configured together"},
		{name: "session token without static credentials", edit: func(c *model.DataSourceConfig) {
			c.Username, c.Password = "", ""
			c.AdditionalProps["session_token"] = "temporary"
		}, want: "session_token"},
		{name: "external id without role", edit: func(c *model.DataSourceConfig) { c.AdditionalProps["external_id"] = "external" }, want: "external_id"},
		{name: "endpoint with path", edit: func(c *model.DataSourceConfig) { c.AdditionalProps["endpoint_url"] = "https://s3.example.test/proxy" }, want: "HTTP(S) origin"},
		{name: "oversized batch", edit: func(c *model.DataSourceConfig) { c.AdditionalProps["batch_size"] = 10001 }, want: "batch_size"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := valid()
			if tt.edit != nil {
				tt.edit(config)
			}
			err := ValidateS3DeltaConfig(config)
			if tt.want == "" && err != nil {
				t.Fatalf("ValidateS3DeltaConfig() error = %v", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("ValidateS3DeltaConfig() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestS3DeltaTableName(t *testing.T) {
	name, err := S3DeltaTableName(&model.DataSourceConfig{Host: "warehouse/events"})
	if err != nil || name != "events" {
		t.Fatalf("S3DeltaTableName() = %q, %v; want events", name, err)
	}
	name, err = S3DeltaTableName(&model.DataSourceConfig{
		Host: "warehouse/events", AdditionalProps: map[string]interface{}{"table_name": "daily_events"},
	})
	if err != nil || name != "daily_events" {
		t.Fatalf("explicit S3 Delta table name = %q, %v; want daily_events", name, err)
	}
}
