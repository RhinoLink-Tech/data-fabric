package domestic

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	mysql "github.com/go-sql-driver/mysql"
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

	t.Run("least privilege account over verified TLS", func(t *testing.T) {
		// The server is a disposable pinned fixture. The root connection only
		// provisions data and grants; the application uses the restricted user.
		for _, statement := range []string{
			"CREATE DATABASE df_readonly_contract",
			"CREATE TABLE df_readonly_contract.orders (id BIGINT PRIMARY KEY)",
			"CREATE TABLE df_readonly_contract.private_records (id BIGINT PRIMARY KEY)",
			"INSERT INTO df_readonly_contract.orders VALUES (1), (2)",
			"CREATE USER 'df_readonly_contract'@'%' IDENTIFIED BY 'isolated-test-password'",
			"GRANT SELECT ON df_readonly_contract.orders TO 'df_readonly_contract'@'%'",
		} {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				t.Fatalf("prepare isolated read-only fixture: %v", err)
			}
		}
		t.Cleanup(func() {
			_, _ = db.ExecContext(context.Background(), "DROP USER IF EXISTS 'df_readonly_contract'@'%'")
			_, _ = db.ExecContext(context.Background(), "DROP DATABASE IF EXISTS df_readonly_contract")
		})

		config := *trusted
		config.Database = "df_readonly_contract"
		config.Username = "df_readonly_contract"
		config.Password = "isolated-test-password"
		reader := openTiDBTLSContractDB(t, driver, &config)
		defer reader.Close()
		assertTiDBTLSState(t, ctx, reader, true)
		var count int
		if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM orders").Scan(&count); err != nil || count != 2 {
			t.Fatalf("restricted account orders count = %d, error = %v", count, err)
		}
		var columns int
		if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'orders'").Scan(&columns); err != nil || columns != 1 {
			t.Fatalf("restricted account visible column count = %d, error = %v", columns, err)
		}
		for _, statement := range []string{
			"INSERT INTO orders VALUES (3)",
			"UPDATE orders SET id = 3 WHERE id = 1",
			"DELETE FROM orders WHERE id = 1",
			"CREATE TABLE forbidden_table (id INT)",
			"DROP TABLE orders",
			"SELECT id FROM private_records",
		} {
			_, err := reader.ExecContext(ctx, statement)
			var serverError *mysql.MySQLError
			if !errors.As(err, &serverError) || (serverError.Number != 1044 && serverError.Number != 1142 && serverError.Number != 1143) {
				t.Fatalf("restricted statement %q error = %v, want TiDB access-denied error", statement, err)
			}
		}
		if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM orders").Scan(&count); err != nil || count != 2 {
			t.Fatalf("restricted write attempt changed fixture: count = %d, error = %v", count, err)
		}
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
