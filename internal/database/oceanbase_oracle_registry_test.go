//go:build !datafabric_core

package database

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestOceanBaseOracleRegistryPoolAndHealthConfiguration(t *testing.T) {
	pool := NewConnectionPool(nil, nil, nil, true)
	cfg := &model.DataSourceConfig{Host: "127.0.0.1", Port: 2883, Database: "svc", Username: "user", Password: "p@ss:/#", Schema: "SALES"}
	dsn, err := pool.getConnectUrl(model.DatabaseTypeOceanBaseOracle, cfg)
	if err != nil || dsn == "" {
		t.Fatalf("registry-created source rejected: %v", err)
	}
	d, err := pool.registry.GetDriver(model.DatabaseTypeOceanBaseOracle)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.ValidateDSN(dsn); err != nil {
		t.Fatalf("invalid registry DSN: %v", err)
	}
	cfg.SSL = true
	if _, err := pool.getConnectUrl(model.DatabaseTypeOceanBaseOracle, cfg); err == nil || !strings.Contains(err.Error(), "TLS") {
		t.Fatalf("pool did not fail closed: %v", err)
	}
	result, err := NewHealthChecker(pool).CheckDataSourceConnectivity(context.Background(), cfg, model.DatabaseTypeOceanBaseOracle)
	if err != nil || result == nil || result.Status != "error" || !strings.Contains(result.Message, "TLS") {
		t.Fatalf("health did not fail closed: %+v, %v", result, err)
	}
}

// Oracle Free is a go-ora wire/session fixture, not a live OceanBase contract.
func TestOceanBaseOracleWireAgainstOracleFixture(t *testing.T) {
	host := os.Getenv("DATA_FABRIC_ORACLE_CONTRACT_HOST")
	if host == "" {
		t.Skip("set Oracle contract environment to use an isolated fixture")
	}
	port, err := strconv.Atoi(os.Getenv("DATA_FABRIC_ORACLE_CONTRACT_PORT"))
	if err != nil {
		t.Fatal("invalid fixture port")
	}
	user := os.Getenv("DATA_FABRIC_ORACLE_CONTRACT_USERNAME")
	cfg := &model.DataSourceConfig{Host: host, Port: port, Database: os.Getenv("DATA_FABRIC_ORACLE_CONTRACT_SERVICE"), Username: user, Password: os.Getenv("DATA_FABRIC_ORACLE_CONTRACT_PASSWORD"), Schema: strings.ToUpper(user)}
	pool := NewConnectionPool(nil, nil, nil, true)
	dsn, err := pool.getConnectUrl(model.DatabaseTypeOceanBaseOracle, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pool.openConfiguredDatabase(ctx, model.DatabaseTypeOceanBaseOracle, dsn, cfg, true)
	if err != nil {
		t.Fatalf("fixture open: %v", err)
	}
	defer db.Close()
	first, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, conn := range []interface {
		QueryRowContext(context.Context, string, ...interface{}) *sql.Row
	}{first, second} {
		var schema string
		if err := conn.QueryRowContext(ctx, "SELECT SYS_CONTEXT('USERENV','CURRENT_SCHEMA') FROM DUAL").Scan(&schema); err != nil || schema != cfg.Schema {
			t.Fatalf("session schema: got %q, error %v", schema, err)
		}
	}
}
