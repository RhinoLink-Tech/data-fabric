package traditional

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	postgresTLSContractDSNEnv     = "DATA_FABRIC_POSTGRES_TLS_CONTRACT_DSN"
	postgresTLSContractBadCAEnv   = "DATA_FABRIC_POSTGRES_TLS_BAD_CA"
	postgresTLSContractVersionEnv = "DATA_FABRIC_POSTGRES_TLS_EXPECTED_VERSION"
)

func TestPostgreSQLTLSVerification(t *testing.T) {
	rawDSN := os.Getenv(postgresTLSContractDSNEnv)
	if rawDSN == "" {
		t.Skipf("set %s to run the PostgreSQL TLS contract", postgresTLSContractDSNEnv)
	}
	badCA := os.Getenv(postgresTLSContractBadCAEnv)
	expectedVersion := os.Getenv(postgresTLSContractVersionEnv)
	if badCA == "" || expectedVersion == "" {
		t.Fatalf("%s and %s are required when the PostgreSQL TLS DSN is set", postgresTLSContractBadCAEnv, postgresTLSContractVersionEnv)
	}

	driver := &PostgreSQLDriver{}
	trusted := postgresConfigFromURL(t, rawDSN)
	if trusted.SSLMode != "verify-full" || trusted.SSLRootCert == "" {
		t.Fatal("TLS contract DSN must use sslmode=verify-full and provide sslrootcert")
	}

	db := openPostgreSQLTLSContractDB(t, driver, trusted)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var version string
	if err := db.QueryRowContext(ctx, "SHOW server_version").Scan(&version); err != nil {
		t.Fatalf("read PostgreSQL version over verified TLS: %v", err)
	}
	if version != expectedVersion {
		t.Fatalf("PostgreSQL version = %q, want %q", version, expectedVersion)
	}
	assertPostgreSQLTLSState(t, ctx, db, true)

	t.Run("untrusted CA is rejected", func(t *testing.T) {
		config := *trusted
		config.SSLRootCert = badCA
		assertPostgreSQLTLSRejected(t, driver, &config)
	})

	t.Run("hostname mismatch is rejected", func(t *testing.T) {
		config := *trusted
		config.Host = "127.0.0.1"
		assertPostgreSQLTLSRejected(t, driver, &config)
	})

	t.Run("legacy ssl true still encrypts without certificate verification", func(t *testing.T) {
		config := *trusted
		config.SSL = true
		config.SSLMode = ""
		config.SSLRootCert = ""
		legacyDB := openPostgreSQLTLSContractDB(t, driver, &config)
		defer legacyDB.Close()
		assertPostgreSQLTLSState(t, ctx, legacyDB, true)
	})

	t.Run("legacy ssl false still connects without TLS", func(t *testing.T) {
		config := *trusted
		config.SSL = false
		config.SSLMode = ""
		config.SSLRootCert = ""
		plainDB := openPostgreSQLTLSContractDB(t, driver, &config)
		defer plainDB.Close()
		assertPostgreSQLTLSState(t, ctx, plainDB, false)
	})
}

func openPostgreSQLTLSContractDB(t *testing.T, driver *PostgreSQLDriver, config *model.DataSourceConfig) *sql.DB {
	t.Helper()
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("validate PostgreSQL TLS DSN: %v", err)
	}
	db, err := driver.Open(dsn)
	if err != nil {
		t.Fatalf("open PostgreSQL TLS connection: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	return db
}

func assertPostgreSQLTLSRejected(t *testing.T, driver *PostgreSQLDriver, config *model.DataSourceConfig) {
	t.Helper()
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("TLS DSN should validate before the server handshake: %v", err)
	}
	db, err := driver.Open(dsn)
	if err != nil {
		t.Fatalf("open PostgreSQL TLS connection: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err == nil {
		t.Fatal("connection succeeded with an untrusted certificate or mismatched hostname")
	}
}

func assertPostgreSQLTLSState(t *testing.T, ctx context.Context, db *sql.DB, wantTLS bool) {
	t.Helper()
	var got bool
	if err := db.QueryRowContext(ctx, "SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()").Scan(&got); err != nil {
		t.Fatalf("read current PostgreSQL TLS state: %v", err)
	}
	if got != wantTLS {
		t.Fatalf("PostgreSQL TLS state = %t, want %t", got, wantTLS)
	}
}
