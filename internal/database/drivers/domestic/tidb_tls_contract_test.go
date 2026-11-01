package domestic

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	tidbTLSContractHostEnv    = "DATA_FABRIC_TIDB_TLS_CONTRACT_HOST"
	tidbTLSContractPortEnv    = "DATA_FABRIC_TIDB_TLS_CONTRACT_PORT"
	tidbTLSContractRootCAEnv  = "DATA_FABRIC_TIDB_TLS_ROOT_CA"
	tidbTLSContractBadCAEnv   = "DATA_FABRIC_TIDB_TLS_BAD_CA"
	tidbTLSContractVersionEnv = "DATA_FABRIC_TIDB_TLS_EXPECTED_VERSION"
)

func TestTiDBTLSVerification(t *testing.T) {
	host := os.Getenv(tidbTLSContractHostEnv)
	if host == "" {
		t.Skipf("set %s to run the TiDB TLS contract", tidbTLSContractHostEnv)
	}
	port, err := strconv.Atoi(os.Getenv(tidbTLSContractPortEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", tidbTLSContractPortEnv)
	}
	rootCA := os.Getenv(tidbTLSContractRootCAEnv)
	badCA := os.Getenv(tidbTLSContractBadCAEnv)
	expectedVersion := os.Getenv(tidbTLSContractVersionEnv)
	if rootCA == "" || badCA == "" || expectedVersion == "" {
		t.Fatalf("%s, %s, and %s are required when the TiDB TLS host is set", tidbTLSContractRootCAEnv, tidbTLSContractBadCAEnv, tidbTLSContractVersionEnv)
	}

	driver := &TiDBDriver{}
	trusted := &model.DataSourceConfig{
		Host: host, Port: port, Database: "test", Username: "root",
		SSL: true, SSLRootCert: rootCA,
	}
	db := openTiDBTLSContractDB(t, driver, trusted)
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		t.Fatalf("read TiDB version over verified TLS: %v", err)
	}
	if version != expectedVersion {
		t.Fatalf("TiDB version = %q, want %q", version, expectedVersion)
	}
	assertTiDBTLSState(t, ctx, db, true)

	t.Run("untrusted CA is rejected", func(t *testing.T) {
		config := *trusted
		config.SSLRootCert = badCA
		assertTiDBTLSRejected(t, driver, &config)
	})

	t.Run("hostname mismatch is rejected", func(t *testing.T) {
		config := *trusted
		config.Host = "127.0.0.1"
		assertTiDBTLSRejected(t, driver, &config)
	})

	t.Run("system roots reject an untrusted server CA", func(t *testing.T) {
		config := *trusted
		config.SSLRootCert = ""
		assertTiDBTLSRejected(t, driver, &config)
	})

	t.Run("legacy ssl false remains plaintext", func(t *testing.T) {
		config := *trusted
		config.SSL = false
		config.SSLRootCert = ""
		plainDB := openTiDBTLSContractDB(t, driver, &config)
		defer plainDB.Close()
		assertTiDBTLSState(t, ctx, plainDB, false)
	})
}

func openTiDBTLSContractDB(t *testing.T, driver *TiDBDriver, config *model.DataSourceConfig) *sql.DB {
	t.Helper()
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("validate TiDB TLS DSN: %v", err)
	}
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		db, err := driver.Open(dsn)
		if err == nil {
			return db
		}
		lastErr = err
		time.Sleep(time.Second)
	}
	t.Fatalf("connect to TiDB TLS contract service: %v", lastErr)
	return nil
}

func assertTiDBTLSRejected(t *testing.T, driver *TiDBDriver, config *model.DataSourceConfig) {
	t.Helper()
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("TLS DSN should validate before the server handshake: %v", err)
	}
	db, err := driver.Open(dsn)
	if err == nil {
		db.Close()
		t.Fatal("connection succeeded with an untrusted CA or mismatched hostname")
	}
}

func assertTiDBTLSState(t *testing.T, ctx context.Context, db *sql.DB, wantTLS bool) {
	t.Helper()
	var name, value string
	if err := db.QueryRowContext(ctx, "SHOW STATUS LIKE 'Ssl_cipher'").Scan(&name, &value); err != nil {
		t.Fatalf("read TiDB TLS status: %v", err)
	}
	gotTLS := value != ""
	if gotTLS != wantTLS {
		t.Fatalf("TiDB TLS state = %t (%q), want %t", gotTLS, value, wantTLS)
	}
}
