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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

	t.Run("least privilege identity over verified TLS", func(t *testing.T) {
		const account = "df_contract_reader"
		const accountPassword = "df-synthetic-reader-only"
		const orders = "df_contract_orders"
		const restricted = "df_contract_restricted"
		exec := func(query string) {
			t.Helper()
			if _, err := db.ExecContext(ctx, query); err != nil {
				t.Fatalf("setup or cleanup failed: %v", err)
			}
		}
		exec("DROP USER IF EXISTS " + account)
		exec("DROP TABLE IF EXISTS " + orders)
		exec("DROP TABLE IF EXISTS " + restricted)
		exec("CREATE TABLE " + orders + " (id UInt64, status String) ENGINE = Memory")
		exec("CREATE TABLE " + restricted + " (id UInt64) ENGINE = Memory")
		defer func() {
			exec("DROP USER IF EXISTS " + account)
			exec("DROP TABLE IF EXISTS " + orders)
			exec("DROP TABLE IF EXISTS " + restricted)
		}()
		exec("INSERT INTO " + orders + " VALUES (1, 'new'), (2, 'paid')")
		exec("INSERT INTO " + restricted + " VALUES (9)")
		exec("CREATE USER " + account + " IDENTIFIED WITH sha256_password BY '" + accountPassword + "'")
		exec("GRANT SELECT ON " + databaseName + "." + orders + " TO " + account)

		limited := clickHouseTLSConfig(host, port, account, accountPassword, databaseName, rootCA)
		reader := openClickHouseContractDB(t, driver, limited)
		defer reader.Close()
		if err := reader.PingContext(ctx); err != nil {
			t.Fatalf("restricted TLS login: %v", err)
		}
		var count uint64
		if err := reader.QueryRowContext(ctx, "SELECT count() FROM "+orders).Scan(&count); err != nil || count != 2 {
			t.Fatalf("authorized count=%d err=%v", count, err)
		}
		rows, err := reader.QueryContext(ctx, "SELECT id FROM "+orders+" ORDER BY id")
		if err != nil {
			t.Fatalf("authorized ordered query: %v", err)
		}
		var ids []uint64
		for rows.Next() {
			var id uint64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		iterationErr := rows.Err()
		rows.Close()
		if iterationErr != nil || len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
			t.Fatalf("authorized ordered ids=%v error=%v", ids, iterationErr)
		}
		var column string
		if err := reader.QueryRowContext(ctx, "SELECT name FROM system.columns WHERE database = currentDatabase() AND table = '"+orders+"' ORDER BY position LIMIT 1").Scan(&column); err != nil || column != "id" {
			t.Fatalf("authorized metadata column=%q err=%v", column, err)
		}
		for _, query := range []string{
			"INSERT INTO " + orders + " VALUES (3, 'denied')",
			"CREATE TABLE df_contract_denied (id UInt64) ENGINE = Memory",
			"SELECT id FROM " + restricted,
		} {
			var err error
			if strings.HasPrefix(query, "SELECT ") {
				var value uint64
				err = reader.QueryRowContext(ctx, query).Scan(&value)
			} else {
				_, err = reader.ExecContext(ctx, query)
			}
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "not enough privileges") {
				t.Fatalf("expected privilege denial for %q, got %v", query, err)
			}
		}
		if err := db.QueryRowContext(ctx, "SELECT count() FROM "+orders).Scan(&count); err != nil || count != 2 {
			t.Fatalf("fixture changed: count=%d err=%v", count, err)
		}
		wrong := clickHouseTLSConfig(host, port, account, "wrong-synthetic-password", databaseName, rootCA)
		bad := openClickHouseContractDB(t, driver, wrong)
		defer bad.Close()
		if err := bad.PingContext(ctx); err == nil {
			t.Fatal("incorrect credentials accepted")
		}
		assertClickHouseTLSRejected(t, driver, clickHouseTLSConfig(host, port, account, accountPassword, databaseName, badCA))
		assertClickHouseTLSRejected(t, driver, clickHouseTLSConfig("localhost", port, account, accountPassword, databaseName, rootCA))
	})

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
