package database

import (
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestDomesticConnectionPoolRejectsUnsupportedTLSBeforeOpening(t *testing.T) {
	pool := NewConnectionPool(nil, nil, nil, true)
	for _, dbType := range []model.DatabaseType{
		model.DatabaseTypeTDSQL, model.DatabaseTypeOscar, model.DatabaseTypeKingbaseES,
		model.DatabaseTypeGBase8s, model.DatabaseTypeGBase8t,
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
