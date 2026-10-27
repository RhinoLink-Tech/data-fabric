package olap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	clickHouseContractHostEnv     = "DATA_FABRIC_CLICKHOUSE_CONTRACT_HOST"
	clickHouseContractPortEnv     = "DATA_FABRIC_CLICKHOUSE_CONTRACT_PORT"
	clickHouseContractUserEnv     = "DATA_FABRIC_CLICKHOUSE_CONTRACT_USERNAME"
	clickHouseContractPasswordEnv = "DATA_FABRIC_CLICKHOUSE_CONTRACT_PASSWORD"
	clickHouseContractDatabaseEnv = "DATA_FABRIC_CLICKHOUSE_CONTRACT_DATABASE"
	clickHouseExpectedVersionEnv  = "DATA_FABRIC_CLICKHOUSE_EXPECTED_VERSION"
)

func TestClickHouseSQLDriverRegistered(t *testing.T) {
	for _, name := range sql.Drivers() {
		if name == "clickhouse" {
			return
		}
	}
	t.Fatalf("ClickHouse database/sql driver is not registered; registered drivers: %v", sql.Drivers())
}

func TestClickHouseDriverContract(t *testing.T) {
	host := os.Getenv(clickHouseContractHostEnv)
	if host == "" {
		t.Skipf("set %s to run the ClickHouse server contract", clickHouseContractHostEnv)
	}
	port, err := strconv.Atoi(os.Getenv(clickHouseContractPortEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", clickHouseContractPortEnv)
	}
	username := os.Getenv(clickHouseContractUserEnv)
	password := os.Getenv(clickHouseContractPasswordEnv)
	databaseName := os.Getenv(clickHouseContractDatabaseEnv)
	expectedVersion := os.Getenv(clickHouseExpectedVersionEnv)
	if username == "" || password == "" || databaseName == "" || expectedVersion == "" {
		t.Fatalf("ClickHouse contract username, password, database, and expected server version are required")
	}

	driver := &ClickHouseDriver{}
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
	if driver.GetDefaultPort() != 9000 {
		t.Fatalf("default port = %d, want 9000", driver.GetDefaultPort())
	}

	db, err := driver.Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := driver.TestConnection(db); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}

	t.Run("exact server version and selected database", func(t *testing.T) {
		var serverVersion string
		if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&serverVersion); err != nil {
			t.Fatalf("read server version: %v", err)
		}
		if serverVersion != expectedVersion {
			t.Fatalf("server version = %q, want exact contract version %q", serverVersion, expectedVersion)
		}

		var selectedDatabase string
		if err := db.QueryRowContext(ctx, "SELECT currentDatabase()").Scan(&selectedDatabase); err != nil {
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
		err := db.QueryRowContext(ctx, "SELECT toInt64(42), toString('contract'), toUInt8(1), CAST(NULL, 'Nullable(String)'), toString(toDecimal64(1234.56, 2))").
			Scan(&integer, &label, &enabled, &nullable, &amount)
		if err != nil {
			t.Fatalf("query and scan ClickHouse values: %v", err)
		}
		if integer != 42 || label != "contract" || !enabled || nullable.Valid || amount != "1234.56" {
			t.Fatalf("scanned values = (%d, %q, %t, %+v, %q)", integer, label, enabled, nullable, amount)
		}
	})

	t.Run("schema discovery", func(t *testing.T) {
		table := fmt.Sprintf("data_fabric_contract_schema_%d", time.Now().UnixNano())
		if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (id Int64, label String) ENGINE = Memory"); err != nil {
			t.Fatalf("create schema contract table: %v", err)
		}
		defer db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table)

		rows, err := db.QueryContext(ctx, "SELECT name, type FROM system.columns WHERE database = currentDatabase() AND table = '"+table+"' ORDER BY position")
		if err != nil {
			t.Fatalf("query ClickHouse schema metadata: %v", err)
		}
		defer rows.Close()
		var columns []string
		for rows.Next() {
			var name, columnType string
			if err := rows.Scan(&name, &columnType); err != nil {
				t.Fatalf("scan ClickHouse schema metadata: %v", err)
			}
			columns = append(columns, name+" "+columnType)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read ClickHouse schema metadata: %v", err)
		}
		if fmt.Sprint(columns) != "[id Int64 label String]" {
			t.Fatalf("schema columns = %v, want [id Int64 label String]", columns)
		}
	})

	t.Run("batch pagination", func(t *testing.T) {
		query, err := driver.ApplyBatchPagination("SELECT number FROM numbers(4) ORDER BY number", 2, 1)
		if err != nil {
			t.Fatalf("ApplyBatchPagination: %v", err)
		}
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			t.Fatalf("execute paginated query %q: %v", query, err)
		}
		defer rows.Close()
		var got []uint64
		for rows.Next() {
			var number uint64
			if err := rows.Scan(&number); err != nil {
				t.Fatalf("scan paginated result: %v", err)
			}
			got = append(got, number)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read paginated result: %v", err)
		}
		if fmt.Sprint(got) != "[1 2]" {
			t.Fatalf("paginated rows = %v, want [1 2]", got)
		}
	})

	t.Run("query cancellation", func(t *testing.T) {
		queryCtx, stop := context.WithTimeout(ctx, 250*time.Millisecond)
		defer stop()
		deadline, _ := queryCtx.Deadline()
		started := time.Now()
		var result uint8
		err := db.QueryRowContext(queryCtx, "SELECT sleep(2)").Scan(&result)
		returnedAt := time.Now()
		if err == nil {
			t.Fatal("canceled query unexpectedly succeeded")
		}
		if returnedAt.Before(deadline) {
			t.Fatalf("query failed before the context deadline: %v", err)
		}
		// The socket deadline can fire before the context's cancellation callback.
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("query returned an unrelated error: %v", err)
		}
		if elapsed := returnedAt.Sub(started); elapsed > 3*time.Second {
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
