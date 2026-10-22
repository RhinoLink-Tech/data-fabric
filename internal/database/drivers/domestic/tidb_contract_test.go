package domestic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/readonly"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	tidbContractHostEnv     = "DATA_FABRIC_TIDB_CONTRACT_HOST"
	tidbContractPortEnv     = "DATA_FABRIC_TIDB_CONTRACT_PORT"
	tidbContractUserEnv     = "DATA_FABRIC_TIDB_CONTRACT_USERNAME"
	tidbContractPasswordEnv = "DATA_FABRIC_TIDB_CONTRACT_PASSWORD"
	tidbContractDatabaseEnv = "DATA_FABRIC_TIDB_CONTRACT_DATABASE"
	tidbExpectedVersionEnv  = "DATA_FABRIC_TIDB_EXPECTED_VERSION"
)

func TestTiDBDriverContract(t *testing.T) {
	host := os.Getenv(tidbContractHostEnv)
	if host == "" {
		t.Skipf("set %s to run the TiDB server contract", tidbContractHostEnv)
	}
	port, err := strconv.Atoi(os.Getenv(tidbContractPortEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", tidbContractPortEnv)
	}
	username := os.Getenv(tidbContractUserEnv)
	password := os.Getenv(tidbContractPasswordEnv)
	databaseName := os.Getenv(tidbContractDatabaseEnv)
	expectedVersion := os.Getenv(tidbExpectedVersionEnv)
	if username == "" || password == "" || databaseName == "" || expectedVersion == "" {
		t.Fatalf("TiDB contract username, password, database, and expected server version are required")
	}
	if !isTiDBContractIdentifier(username) || !isTiDBContractIdentifier(databaseName) {
		t.Fatalf("TiDB contract username and database must contain only letters, digits, and underscores")
	}

	driver := &TiDBDriver{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prepareTiDBContractAccount(t, ctx, driver, host, port, username, password, databaseName)

	config := &model.DataSourceConfig{
		Host:     host,
		Port:     port,
		Database: databaseName,
		Username: username,
		Password: password,
	}
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN: %v", err)
	}
	if driver.GetDefaultPort() != 4000 {
		t.Fatalf("default port = %d, want 4000", driver.GetDefaultPort())
	}

	db, err := driver.Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)
	if err := driver.TestConnection(db); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}

	t.Run("exact TiDB server version and selected database", func(t *testing.T) {
		var serverVersion string
		if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&serverVersion); err != nil {
			t.Fatalf("read TiDB server version: %v", err)
		}
		if serverVersion != expectedVersion || !strings.Contains(strings.ToLower(serverVersion), "tidb") {
			t.Fatalf("server version = %q, want exact TiDB version %q", serverVersion, expectedVersion)
		}

		var selectedDatabase string
		if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&selectedDatabase); err != nil {
			t.Fatalf("read selected database: %v", err)
		}
		if selectedDatabase != databaseName {
			t.Fatalf("selected database = %q, want %q", selectedDatabase, databaseName)
		}
	})

	t.Run("query SQL values and NULL", func(t *testing.T) {
		var integer int64
		var label string
		var enabled bool
		var nullable sql.NullString
		var amount string
		err := db.QueryRowContext(ctx, "SELECT CAST(42 AS SIGNED), CAST('contract' AS CHAR), TRUE, CAST(NULL AS CHAR), CAST(1234.56 AS DECIMAL(10,2))").
			Scan(&integer, &label, &enabled, &nullable, &amount)
		if err != nil {
			t.Fatalf("query and scan TiDB values: %v", err)
		}
		if integer != 42 || label != "contract" || !enabled || nullable.Valid || amount != "1234.56" {
			t.Fatalf("scanned values = (%d, %q, %t, %+v, %q)", integer, label, enabled, nullable, amount)
		}
	})

	t.Run("schema discovery", func(t *testing.T) {
		table := fmt.Sprintf("data_fabric_contract_schema_%d", time.Now().UnixNano())
		if _, err := db.ExecContext(ctx, "CREATE TABLE `"+table+"` (id BIGINT, label VARCHAR(32))"); err != nil {
			t.Fatalf("create schema contract table: %v", err)
		}
		defer db.ExecContext(context.Background(), "DROP TABLE IF EXISTS `"+table+"`")

		rows, err := db.QueryContext(ctx, "SELECT COLUMN_NAME, DATA_TYPE FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? ORDER BY ORDINAL_POSITION", table)
		if err != nil {
			t.Fatalf("query TiDB schema metadata: %v", err)
		}
		defer rows.Close()
		var columns []string
		for rows.Next() {
			var name, columnType string
			if err := rows.Scan(&name, &columnType); err != nil {
				t.Fatalf("scan TiDB schema metadata: %v", err)
			}
			columns = append(columns, name+" "+columnType)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read TiDB schema metadata: %v", err)
		}
		if fmt.Sprint(columns) != "[id bigint label varchar]" {
			t.Fatalf("schema columns = %v, want [id bigint label varchar]", columns)
		}
	})

	t.Run("batch pagination", func(t *testing.T) {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire connection: %v", err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, "CREATE TEMPORARY TABLE data_fabric_contract_rows (id INTEGER PRIMARY KEY)"); err != nil {
			t.Fatalf("create pagination table: %v", err)
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO data_fabric_contract_rows (id) VALUES (1), (2), (3), (4)"); err != nil {
			t.Fatalf("insert pagination rows: %v", err)
		}
		query, err := driver.ApplyBatchPagination("SELECT id FROM data_fabric_contract_rows ORDER BY id", 2, 1)
		if err != nil {
			t.Fatalf("ApplyBatchPagination: %v", err)
		}
		rows, err := conn.QueryContext(ctx, query)
		if err != nil {
			t.Fatalf("execute paginated query %q: %v", query, err)
		}
		defer rows.Close()
		var got []int
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scan paginated result: %v", err)
			}
			got = append(got, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read paginated result: %v", err)
		}
		if fmt.Sprint(got) != "[2 3]" {
			t.Fatalf("paginated rows = %v, want [2 3]", got)
		}
	})

	t.Run("unsupported database-side read-only mode is reported", func(t *testing.T) {
		if readonly.SupportsReadOnlySession(model.DatabaseTypeTiDB) {
			t.Fatal("TiDB must not claim database-side read-only enforcement for the verified v8.5.8 baseline")
		}
		readOnlyDB, handled, err := readonly.OpenDB(model.DatabaseTypeTiDB, dsn)
		if err != nil {
			t.Fatalf("open read-only connection: %v", err)
		}
		if handled || readOnlyDB != nil {
			t.Fatal("TiDB read-only initializer should be reported as unsupported")
		}
	})

	t.Run("query cancellation", func(t *testing.T) {
		queryCtx, stop := context.WithTimeout(ctx, 250*time.Millisecond)
		defer stop()
		started := time.Now()
		var result sql.NullInt64
		err := db.QueryRowContext(queryCtx, "SELECT SLEEP(2)").Scan(&result)
		if err == nil {
			t.Fatal("canceled query unexpectedly succeeded")
		}
		if !errors.Is(queryCtx.Err(), context.DeadlineExceeded) {
			t.Fatalf("query failed before the context deadline: query error = %v, context error = %v", err, queryCtx.Err())
		}
		if elapsed := time.Since(started); elapsed > 3*time.Second {
			t.Fatalf("query cancellation took %s, want under 3s", elapsed)
		}
	})

	t.Run("server errors are returned", func(t *testing.T) {
		var result string
		if err := db.QueryRowContext(ctx, "SELECT data_fabric_missing_contract_column").Scan(&result); err == nil {
			t.Fatal("query with a missing column unexpectedly succeeded")
		}
	})
}

func prepareTiDBContractAccount(t *testing.T, ctx context.Context, driver *TiDBDriver, host string, port int, username, password, databaseName string) {
	t.Helper()
	adminDSN := driver.BuildDSN(&model.DataSourceConfig{
		Host:     host,
		Port:     port,
		Database: "mysql",
		Username: "root",
	})
	adminDB, err := driver.Open(adminDSN)
	if err != nil {
		t.Fatalf("open temporary TiDB root connection: %v", err)
	}
	defer adminDB.Close()

	if _, err := adminDB.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS `"+databaseName+"`"); err != nil {
		t.Fatalf("create contract database: %v", err)
	}
	userLiteral := quoteTiDBSQLString(username) + "@'%'"
	if _, err := adminDB.ExecContext(ctx, "CREATE USER IF NOT EXISTS "+userLiteral+" IDENTIFIED BY "+quoteTiDBSQLString(password)); err != nil {
		t.Fatalf("create contract user: %v", err)
	}
	if _, err := adminDB.ExecContext(ctx, "GRANT ALL PRIVILEGES ON `"+databaseName+"`.* TO "+userLiteral); err != nil {
		t.Fatalf("grant contract database privileges: %v", err)
	}
}

func isTiDBContractIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func quoteTiDBSQLString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
