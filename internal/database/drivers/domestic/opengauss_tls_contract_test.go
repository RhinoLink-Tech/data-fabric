package domestic

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
	openGaussTLSContractHostEnv     = "DATA_FABRIC_OPENGAUSS_TLS_CONTRACT_HOST"
	openGaussTLSContractPortEnv     = "DATA_FABRIC_OPENGAUSS_TLS_CONTRACT_PORT"
	openGaussTLSContractUserEnv     = "DATA_FABRIC_OPENGAUSS_TLS_CONTRACT_USERNAME"
	openGaussTLSContractPasswordEnv = "DATA_FABRIC_OPENGAUSS_TLS_CONTRACT_PASSWORD"
	openGaussTLSContractDatabaseEnv = "DATA_FABRIC_OPENGAUSS_TLS_CONTRACT_DATABASE"
	openGaussTLSContractRootCAEnv   = "DATA_FABRIC_OPENGAUSS_TLS_ROOT_CA"
	openGaussTLSContractBadCAEnv    = "DATA_FABRIC_OPENGAUSS_TLS_BAD_CA"
	openGaussTLSContractVersionEnv  = "DATA_FABRIC_OPENGAUSS_TLS_EXPECTED_VERSION"
)

func TestOpenGaussTLSVerification(t *testing.T) {
	host := os.Getenv(openGaussTLSContractHostEnv)
	if host == "" {
		t.Skipf("set %s to run the openGauss TLS contract", openGaussTLSContractHostEnv)
	}
	port, err := strconv.Atoi(os.Getenv(openGaussTLSContractPortEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", openGaussTLSContractPortEnv)
	}
	username := os.Getenv(openGaussTLSContractUserEnv)
	password := os.Getenv(openGaussTLSContractPasswordEnv)
	databaseName := os.Getenv(openGaussTLSContractDatabaseEnv)
	rootCA := os.Getenv(openGaussTLSContractRootCAEnv)
	badCA := os.Getenv(openGaussTLSContractBadCAEnv)
	expectedVersion := os.Getenv(openGaussTLSContractVersionEnv)
	if username == "" || password == "" || databaseName == "" || rootCA == "" || badCA == "" || expectedVersion == "" {
		t.Fatal("openGauss TLS contract credentials, database, CA paths, and expected version are required")
	}

	// Keep legacy sslmode=require behavior deterministic even if the runner has
	// a personal ~/.postgresql/root.crt file.
	t.Setenv("HOME", t.TempDir())

	driver := &OpenGaussDriver{}
	trusted := &model.DataSourceConfig{
		Host: host, Port: port, Database: databaseName, Username: username, Password: password,
		SSL: true, SSLMode: "verify-full", SSLRootCert: rootCA,
	}
	db := openOpenGaussTLSContractDB(t, driver, trusted)
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var version string
	if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&version); err != nil {
		t.Fatalf("read openGauss version over verified TLS: %v", err)
	}
	if !strings.Contains(strings.ToLower(version), "opengauss") || !strings.Contains(version, expectedVersion) {
		t.Fatalf("openGauss version = %q, want version containing %q", version, expectedVersion)
	}

	t.Run("verify-ca trusts the private CA without requiring hostname match", func(t *testing.T) {
		config := *trusted
		config.Host = "127.0.0.1"
		config.SSLMode = "verify-ca"
		caDB := openOpenGaussTLSContractDB(t, driver, &config)
		defer caDB.Close()
		var result int
		if err := caDB.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil || result != 1 {
			t.Fatalf("query over verify-ca TLS: result = %d, error = %v", result, err)
		}
	})

	t.Run("untrusted CA is rejected", func(t *testing.T) {
		config := *trusted
		config.SSLRootCert = badCA
		assertOpenGaussTLSRejected(t, driver, &config)
	})

	t.Run("hostname mismatch is rejected", func(t *testing.T) {
		config := *trusted
		config.Host = "127.0.0.1"
		assertOpenGaussTLSRejected(t, driver, &config)
	})

	t.Run("system roots reject the private server CA", func(t *testing.T) {
		config := *trusted
		config.SSLRootCert = ""
		assertOpenGaussTLSRejected(t, driver, &config)
	})

	t.Run("legacy ssl true still requires encrypted transport", func(t *testing.T) {
		config := *trusted
		config.SSLMode = ""
		config.SSLRootCert = ""
		legacyDB := openOpenGaussTLSContractDB(t, driver, &config)
		defer legacyDB.Close()
		var result int
		if err := legacyDB.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil || result != 1 {
			t.Fatalf("query over legacy ssl=true connection: result = %d, error = %v", result, err)
		}
	})

	t.Run("legacy ssl false remains plaintext", func(t *testing.T) {
		config := *trusted
		config.SSL = false
		config.SSLMode = ""
		config.SSLRootCert = ""
		plainDB := openOpenGaussTLSContractDB(t, driver, &config)
		defer plainDB.Close()
		var result int
		if err := plainDB.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil || result != 1 {
			t.Fatalf("query over legacy ssl=false connection: result = %d, error = %v", result, err)
		}
	})
}

func openOpenGaussTLSContractDB(t *testing.T, driver *OpenGaussDriver, config *model.DataSourceConfig) *sql.DB {
	t.Helper()
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("validate openGauss TLS DSN: %v", err)
	}
	db, err := driver.Open(dsn)
	if err != nil {
		t.Fatalf("connect to openGauss TLS contract service: %v", err)
	}
	return db
}

func assertOpenGaussTLSRejected(t *testing.T, driver *OpenGaussDriver, config *model.DataSourceConfig) {
	t.Helper()
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("TLS DSN should validate before the server handshake: %v", err)
	}
	if db, err := driver.Open(dsn); err == nil {
		db.Close()
		t.Fatal("connection succeeded with an untrusted CA or mismatched hostname")
	}
}
