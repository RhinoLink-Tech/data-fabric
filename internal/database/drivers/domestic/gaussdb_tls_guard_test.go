package domestic

import (
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestGaussDBRegistryDriverRejectsUnmappedTLS(t *testing.T) {
	tests := []struct {
		name   string
		change func(*model.DataSourceConfig)
	}{
		{"tls flag", func(c *model.DataSourceConfig) { c.SSL = true }},
		{"tls mode", func(c *model.DataSourceConfig) { c.SSLMode = "verify-full" }},
		{"root certificate", func(c *model.DataSourceConfig) { c.SSLRootCert = "test-ca" }},
		{"CA alias", func(c *model.DataSourceConfig) { c.TLSCA = "test-ca" }},
		{"server name", func(c *model.DataSourceConfig) { c.TLSServerName = "db.example.test" }},
		{"contradictory mode", func(c *model.DataSourceConfig) { c.SSL = true; c.SSLMode = "disable" }},
	}
	d := &GaussDBDriver{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &model.DataSourceConfig{Host: "example.test", Port: 8000, Database: "db", Username: "app"}
			tt.change(config)
			dsn := d.BuildDSN(config)
			if dsn != unsupportedDomesticTLSDSN {
				t.Fatalf("TLS request did not fail closed: %q", dsn)
			}
			if err := d.ValidateDSN(dsn); err == nil || !strings.Contains(err.Error(), "TLS") {
				t.Fatalf("ValidateDSN error = %v", err)
			}
			if db, err := d.Open(dsn); db != nil || err == nil || !strings.Contains(err.Error(), "TLS") {
				t.Fatalf("Open = %v, %v", db, err)
			}
		})
	}
	plain := d.BuildDSN(&model.DataSourceConfig{Host: "example.test", Port: 8000, Database: "db", Username: "app", SSLMode: "disable"})
	if err := d.ValidateDSN(plain); err != nil || !strings.Contains(plain, "sslmode=disable") {
		t.Fatalf("plaintext compatibility: %q %v", plain, err)
	}
}

func TestGaussDBConstructorSecureModeIsPreserved(t *testing.T) {
	d := &GaussDBDriver{config: &GaussDBConfig{SSLMode: "require"}}
	config := &model.DataSourceConfig{Host: "example.test", Port: 8000, Database: "db", Username: "app", SSL: true}
	dsn := d.BuildDSN(config)
	if err := d.ValidateDSN(dsn); err != nil || !strings.Contains(dsn, "sslmode=require") {
		t.Fatalf("secure constructor mode: %q %v", dsn, err)
	}
}
