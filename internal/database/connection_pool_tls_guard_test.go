package database

import (
	"context"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestDomesticConnectionPoolRejectsUnsupportedTLSBeforeOpening(t *testing.T) {
	pool := NewConnectionPool(nil, nil, nil, true)
	for _, dbType := range []model.DatabaseType{
		model.DatabaseTypeTDSQL, model.DatabaseTypeOscar, model.DatabaseTypeKingbaseES, model.DatabaseTypeOceanBaseOracle,
		model.DatabaseTypeGBase8s, model.DatabaseTypeGBase8t,
		model.DatabaseTypeGaussDBMySQL, model.DatabaseTypeGaussDBPostgres,
	} {
		t.Run(string(dbType), func(t *testing.T) {
			config := &model.DataSourceConfig{Host: "example.test", Port: 5432, Database: "db", Username: "user", SSL: true, SSLMode: "verify-full"}
			if _, err := pool.getConnectUrl(dbType, config); err == nil || !strings.Contains(err.Error(), "TLS") {
				t.Fatalf("requested TLS should be rejected before opening, got %v", err)
			}
			config.SSL = false
			config.SSLMode = "disable"
			if dsn, err := pool.getConnectUrl(dbType, config); err != nil || dsn == "" {
				t.Fatalf("existing plaintext config rejected: %v", err)
			}
		})
	}
}

func TestGaussDBTLSHealthAndPoolRejectWithoutNetwork(t *testing.T) {
	pool := NewConnectionPool(nil, nil, nil, true)
	checker := NewHealthChecker(pool)
	for _, dbType := range []model.DatabaseType{model.DatabaseTypeGaussDBMySQL, model.DatabaseTypeGaussDBPostgres} {
		for _, configure := range []func(*model.DataSourceConfig){
			func(c *model.DataSourceConfig) { c.SSL = true },
			func(c *model.DataSourceConfig) { c.SSLRootCert = "test-ca" },
			func(c *model.DataSourceConfig) { c.SSLMode = "verify-full" },
		} {
			config := &model.DataSourceConfig{Host: "example.invalid", Port: 8000, Database: "db", Username: "app"}
			configure(config)
			if _, err := pool.getConnectUrl(dbType, config); err == nil || !strings.Contains(err.Error(), "TLS") {
				t.Fatalf("%s pool failed to reject TLS: %v", dbType, err)
			}
			result, err := checker.CheckDataSourceConnectivity(context.Background(), config, dbType)
			if err != nil || result == nil || result.Status != "error" || !strings.Contains(result.Message, "TLS") {
				t.Fatalf("%s health check: %+v %v", dbType, result, err)
			}
		}
	}
}
