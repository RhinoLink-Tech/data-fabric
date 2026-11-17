package domestic

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

type buildAndValidate interface {
	BuildDSN(*model.DataSourceConfig) string
	ValidateDSN(string) error
	Open(string) (*sql.DB, error)
}

func TestReviewDomesticDriversRejectUnsupportedTLS(t *testing.T) {
	drivers := map[string]buildAndValidate{
		"tdsql":      &TDSQLDriver{},
		"oscar":      &OscarDriver{},
		"kingbasees": &KingbaseESDriver{},
		"gbase":      &GBaseDriver{},
	}
	for name, driver := range drivers {
		t.Run(name, func(t *testing.T) {
			for _, change := range []func(*model.DataSourceConfig){
				func(c *model.DataSourceConfig) { c.SSL = true },
				func(c *model.DataSourceConfig) { c.SSLMode = "verify-full" },
				func(c *model.DataSourceConfig) { c.SSLRootCert = "/synthetic/ca.pem" },
				func(c *model.DataSourceConfig) { c.TLSCA = "synthetic" },
			} {
				c := bootstrapConfig()
				change(c)
				dsn := driver.BuildDSN(c)
				err := driver.ValidateDSN(dsn)
				if err == nil || !strings.Contains(err.Error(), "TLS") {
					t.Fatal("unsupported TLS settings were not explicitly rejected")
				}
				if db, err := driver.Open(dsn); err == nil || !strings.Contains(err.Error(), "TLS") {
					if db != nil {
						db.Close()
					}
					t.Fatal("Open must reject unsupported TLS before opening a connection")
				}
			}
			c := bootstrapConfig()
			c.SSLMode = "disable"
			if err := driver.ValidateDSN(driver.BuildDSN(c)); err != nil {
				t.Fatalf("legacy plaintext config rejected: %v", err)
			}
		})
	}
}

func TestReviewKingbaseConstructorKeepsMatchingTLSMode(t *testing.T) {
	d := &KingbaseESDriver{config: &KingbaseESConfig{SSLMode: "require"}}
	c := bootstrapConfig()
	c.SSL = true
	dsn := d.BuildDSN(c)
	if !strings.Contains(dsn, "sslmode=require") || d.ValidateDSN(dsn) != nil {
		t.Fatal("matching constructor TLS configuration was not preserved")
	}
}
