package traditional

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/denisenkom/go-mssqldb/msdsn"
)

const (
	sqlServerTLSContractCACertEnv = "DATA_FABRIC_SQLSERVER_TLS_CA_CERT"
	sqlServerTLSContractBadCAEnv  = "DATA_FABRIC_SQLSERVER_TLS_BAD_CA"
)

func TestSQLServerTLSCertificateVerification(t *testing.T) {
	host := os.Getenv(sqlServerContractHostEnv)
	if host == "" {
		t.Skipf("set %s to run the SQL Server TLS contract", sqlServerContractHostEnv)
	}
	port, err := strconv.Atoi(os.Getenv(sqlServerContractPortEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", sqlServerContractPortEnv)
	}
	rootCA := os.Getenv(sqlServerTLSContractCACertEnv)
	badCA := os.Getenv(sqlServerTLSContractBadCAEnv)
	username := os.Getenv(sqlServerContractUserEnv)
	password := os.Getenv(sqlServerContractPasswordEnv)
	databaseName := os.Getenv(sqlServerContractDatabaseEnv)
	expectedVersion := os.Getenv(sqlServerExpectedVersionEnv)
	if rootCA == "" || badCA == "" || username == "" || password == "" || databaseName == "" || expectedVersion == "" {
		t.Fatal("SQL Server TLS CA, bad CA, credentials, database, and expected version are required")
	}

	driver := NewSQLServerDriver()
	trusted := &model.DataSourceConfig{
		Host: host, Port: port, Database: databaseName, Username: username, Password: password,
		SSL: true, SSLRootCert: rootCA,
	}
	db := openSQLServerTLSContractDB(t, driver, trusted)
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var serverVersion string
	if err := db.QueryRowContext(ctx, "SELECT CONVERT(nvarchar(128), SERVERPROPERTY('ProductVersion'))").Scan(&serverVersion); err != nil {
		t.Fatalf("read SQL Server version over verified TLS: %v", err)
	}
	if serverVersion != expectedVersion {
		t.Fatalf("SQL Server version = %q, want %q", serverVersion, expectedVersion)
	}
	assertSQLServerTLSState(t, ctx, db, true)

	t.Run("untrusted CA is rejected", func(t *testing.T) {
		config := *trusted
		config.SSLRootCert = badCA
		assertSQLServerTLSRejected(t, driver, &config)
	})

	t.Run("hostname mismatch is rejected", func(t *testing.T) {
		config := *trusted
		config.Host = "127.0.0.1"
		if host == config.Host {
			config.Host = "localhost"
		}
		assertSQLServerTLSRejected(t, driver, &config)
	})

	t.Run("legacy ssl true with explicit certificate bypass remains encrypted", func(t *testing.T) {
		config := *trusted
		config.SSLRootCert = ""
		config.AdditionalProps = map[string]interface{}{"trustservercertificate": true}
		legacyDB := openSQLServerTLSContractDB(t, driver, &config)
		defer legacyDB.Close()
		assertSQLServerTLSState(t, ctx, legacyDB, true)
	})

	t.Run("ssl false remains unencrypted", func(t *testing.T) {
		config := *trusted
		config.SSL = false
		config.SSLRootCert = ""
		config.AdditionalProps = nil
		plainDB := openSQLServerTLSContractDB(t, driver, &config)
		defer plainDB.Close()
		assertSQLServerTLSState(t, ctx, plainDB, false)
	})
}

func openSQLServerTLSContractDB(t *testing.T, driver *SQLServerDriver, config *model.DataSourceConfig) *sql.DB {
	t.Helper()
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("validate SQL Server TLS DSN: %v", err)
	}
	parsed, _, err := msdsn.Parse(dsn)
	if err != nil {
		t.Fatalf("parse SQL Server TLS DSN: %v", err)
	}
	if config.SSL && (parsed.TLSConfig == nil || parsed.TLSConfig.InsecureSkipVerify) {
		t.Fatal("SQL Server TLS contract must verify the server certificate")
	}
	db, err := driver.Open(dsn)
	if err != nil {
		t.Fatalf("open SQL Server TLS connection: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		t.Fatalf("connect to SQL Server: %v", err)
	}
	return db
}

func assertSQLServerTLSRejected(t *testing.T, driver *SQLServerDriver, config *model.DataSourceConfig) {
	t.Helper()
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("TLS DSN should validate before the server handshake: %v", err)
	}
	db, err := driver.Open(dsn)
	if err != nil {
		t.Fatalf("open SQL Server TLS connection: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err == nil {
		t.Fatal("connection succeeded with an untrusted certificate or mismatched hostname")
	}
}

func assertSQLServerTLSState(t *testing.T, ctx context.Context, db *sql.DB, wantTLS bool) {
	t.Helper()
	var state string
	if err := db.QueryRowContext(ctx, "SELECT encrypt_option FROM sys.dm_exec_connections WHERE session_id = @@SPID").Scan(&state); err != nil {
		t.Fatalf("read SQL Server TLS state: %v", err)
	}
	if got := strings.EqualFold(state, "TRUE"); got != wantTLS {
		t.Fatalf("SQL Server encrypt_option = %q, want TLS %t", state, wantTLS)
	}
}
