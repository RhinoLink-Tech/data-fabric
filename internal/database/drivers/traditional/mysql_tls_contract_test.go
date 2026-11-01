package traditional

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
	mysqlTLSContractHostEnv    = "DATA_FABRIC_MYSQL_TLS_CONTRACT_HOST"
	mysqlTLSContractPortEnv    = "DATA_FABRIC_MYSQL_TLS_CONTRACT_PORT"
	mysqlTLSContractUserEnv    = "DATA_FABRIC_MYSQL_TLS_CONTRACT_USERNAME"
	mysqlTLSContractPassEnv    = "DATA_FABRIC_MYSQL_TLS_CONTRACT_PASSWORD"
	mysqlTLSContractDBEnv      = "DATA_FABRIC_MYSQL_TLS_CONTRACT_DATABASE"
	mysqlTLSContractRootCAEnv  = "DATA_FABRIC_MYSQL_TLS_ROOT_CA"
	mysqlTLSContractBadCAEnv   = "DATA_FABRIC_MYSQL_TLS_BAD_CA"
	mysqlTLSContractVersionEnv = "DATA_FABRIC_MYSQL_TLS_EXPECTED_VERSION"
)

func TestMySQLTLSVerification(t *testing.T) {
	host := os.Getenv(mysqlTLSContractHostEnv)
	if host == "" {
		t.Skipf("set %s to run the MySQL TLS contract", mysqlTLSContractHostEnv)
	}
	port, err := strconv.Atoi(os.Getenv(mysqlTLSContractPortEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", mysqlTLSContractPortEnv)
	}
	username := os.Getenv(mysqlTLSContractUserEnv)
	password := os.Getenv(mysqlTLSContractPassEnv)
	databaseName := os.Getenv(mysqlTLSContractDBEnv)
	rootCA := os.Getenv(mysqlTLSContractRootCAEnv)
	badCA := os.Getenv(mysqlTLSContractBadCAEnv)
	expectedVersion := os.Getenv(mysqlTLSContractVersionEnv)
	if username == "" || password == "" || databaseName == "" || rootCA == "" || badCA == "" || expectedVersion == "" {
		t.Fatalf("MySQL TLS contract credentials, database, CA paths, and expected version are required")
	}

	driver := &MySQLDriver{}
	trusted := &model.DataSourceConfig{
		Host: host, Port: port, Database: databaseName, Username: username, Password: password,
		SSL: true, SSLRootCert: rootCA,
	}
	db := openMySQLTLSContractDB(t, driver, trusted)
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		t.Fatalf("read MySQL version over verified TLS: %v", err)
	}
	if version != expectedVersion {
		t.Fatalf("MySQL version = %q, want %q", version, expectedVersion)
	}
	assertMySQLTLSState(t, ctx, db, true)

	t.Run("untrusted CA is rejected", func(t *testing.T) {
		config := *trusted
		config.SSLRootCert = badCA
		assertMySQLTLSRejected(t, driver, &config)
	})

	t.Run("hostname mismatch is rejected", func(t *testing.T) {
		config := *trusted
		config.Host = "127.0.0.1"
		assertMySQLTLSRejected(t, driver, &config)
	})

	t.Run("system roots reject the private server CA", func(t *testing.T) {
		config := *trusted
		config.SSLRootCert = ""
		assertMySQLTLSRejected(t, driver, &config)
	})

	t.Run("legacy ssl false remains plaintext", func(t *testing.T) {
		config := *trusted
		config.SSL = false
		config.SSLRootCert = ""
		plainDB := openMySQLTLSContractDB(t, driver, &config)
		defer plainDB.Close()
		assertMySQLTLSState(t, ctx, plainDB, false)
	})
}

func openMySQLTLSContractDB(t *testing.T, driver *MySQLDriver, config *model.DataSourceConfig) *sql.DB {
	t.Helper()
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("validate MySQL TLS DSN: %v", err)
	}
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		db, err := driver.Open(dsn)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err = db.PingContext(ctx)
			cancel()
			if err == nil {
				return db
			}
			db.Close()
		}
		lastErr = err
		time.Sleep(time.Second)
	}
	t.Fatalf("connect to MySQL TLS contract service: %v", lastErr)
	return nil
}

func assertMySQLTLSRejected(t *testing.T, driver *MySQLDriver, config *model.DataSourceConfig) {
	t.Helper()
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("TLS DSN should validate before the server handshake: %v", err)
	}
	db, err := driver.Open(dsn)
	if err != nil {
		t.Fatalf("Open should defer server verification until connect: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err == nil {
		t.Fatal("connection succeeded with an untrusted CA or mismatched hostname")
	}
}

func assertMySQLTLSState(t *testing.T, ctx context.Context, db *sql.DB, wantTLS bool) {
	t.Helper()
	var name, value string
	if err := db.QueryRowContext(ctx, "SHOW SESSION STATUS LIKE 'Ssl_cipher'").Scan(&name, &value); err != nil {
		t.Fatalf("read MySQL TLS status: %v", err)
	}
	gotTLS := value != ""
	if gotTLS != wantTLS {
		t.Fatalf("MySQL TLS state = %t (%q), want %t", gotTLS, value, wantTLS)
	}
}
