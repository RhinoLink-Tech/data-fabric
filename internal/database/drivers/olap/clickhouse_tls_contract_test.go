package olap

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

const (
	clickHouseTLSContractHostEnv    = "DATA_FABRIC_CLICKHOUSE_TLS_CONTRACT_HOST"
	clickHouseTLSContractPortEnv    = "DATA_FABRIC_CLICKHOUSE_TLS_CONTRACT_PORT"
	clickHouseTLSRootCAEnv          = "DATA_FABRIC_CLICKHOUSE_TLS_ROOT_CA"
	clickHouseTLSBadCAEnv           = "DATA_FABRIC_CLICKHOUSE_TLS_BAD_CA"
	clickHouseTLSExpectedVersionEnv = "DATA_FABRIC_CLICKHOUSE_TLS_EXPECTED_VERSION"
)

func TestClickHouseTLSVerification(t *testing.T) {
	host := os.Getenv(clickHouseTLSContractHostEnv)
	if host == "" {
		t.Skipf("set %s to run the ClickHouse TLS contract", clickHouseTLSContractHostEnv)
	}
	port, err := strconv.Atoi(os.Getenv(clickHouseTLSContractPortEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", clickHouseTLSContractPortEnv)
	}
	username := os.Getenv(clickHouseContractUserEnv)
	password := os.Getenv(clickHouseContractPasswordEnv)
	databaseName := os.Getenv(clickHouseContractDatabaseEnv)
	expectedVersion := os.Getenv(clickHouseTLSExpectedVersionEnv)
	rootCA := os.Getenv(clickHouseTLSRootCAEnv)
	badCA := os.Getenv(clickHouseTLSBadCAEnv)
	if username == "" || password == "" || databaseName == "" || expectedVersion == "" || rootCA == "" || badCA == "" {
		t.Fatal("ClickHouse TLS contract credentials, CA files, database, and expected server version are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	driver := &ClickHouseDriver{}

	trusted := clickHouseTLSConfig(host, port, username, password, databaseName, rootCA)
	db := openClickHouseContractDB(t, driver, trusted)
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("trusted ClickHouse TLS connection failed: %v", err)
	}
	var serverVersion string
	if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&serverVersion); err != nil {
		t.Fatalf("query through verified ClickHouse TLS connection: %v", err)
	}
	if serverVersion != expectedVersion {
		t.Fatalf("server version = %q, want %q", serverVersion, expectedVersion)
	}

	t.Run("system roots reject the private test CA", func(t *testing.T) {
		config := clickHouseTLSConfig(host, port, username, password, databaseName, "")
		assertClickHouseTLSRejected(t, driver, config)
	})
	t.Run("untrusted CA is rejected", func(t *testing.T) {
		config := clickHouseTLSConfig(host, port, username, password, databaseName, badCA)
		assertClickHouseTLSRejected(t, driver, config)
	})
	t.Run("hostname mismatch is rejected", func(t *testing.T) {
		config := clickHouseTLSConfig("localhost", port, username, password, databaseName, rootCA)
		assertClickHouseTLSRejected(t, driver, config)
	})
}

func clickHouseTLSConfig(host string, port int, username, password, databaseName, rootCA string) *model.DataSourceConfig {
	return &model.DataSourceConfig{
		Host:        host,
		Port:        port,
		Database:    databaseName,
		Username:    username,
		Password:    password,
		SSL:         true,
		SSLRootCert: rootCA,
	}
}

func openClickHouseContractDB(t *testing.T, driver *ClickHouseDriver, config *model.DataSourceConfig) *sql.DB {
	t.Helper()
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN: %v", err)
	}
	db, err := driver.Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db.SetMaxOpenConns(2)
	return db
}

func assertClickHouseTLSRejected(t *testing.T, driver *ClickHouseDriver, config *model.DataSourceConfig) {
	t.Helper()
	db := openClickHouseContractDB(t, driver, config)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := db.PingContext(ctx)
	if err == nil {
		t.Fatal("TLS connection unexpectedly succeeded")
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "certificate") && !strings.Contains(message, "tls") && !strings.Contains(message, "x509") {
		t.Fatalf("TLS connection failed for an unrelated reason: %v", err)
	}
}
