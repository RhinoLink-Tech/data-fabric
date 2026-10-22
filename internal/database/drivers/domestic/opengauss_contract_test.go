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
	openGaussContractHostEnv     = "DATA_FABRIC_OPENGAUSS_CONTRACT_HOST"
	openGaussContractPortEnv     = "DATA_FABRIC_OPENGAUSS_CONTRACT_PORT"
	openGaussContractUserEnv     = "DATA_FABRIC_OPENGAUSS_CONTRACT_USERNAME"
	openGaussContractPasswordEnv = "DATA_FABRIC_OPENGAUSS_CONTRACT_PASSWORD"
	openGaussContractDatabaseEnv = "DATA_FABRIC_OPENGAUSS_CONTRACT_DATABASE"
	openGaussExpectedVersionEnv  = "DATA_FABRIC_OPENGAUSS_EXPECTED_VERSION"
)

func TestOpenGaussDriverContract(t *testing.T) {
	host := os.Getenv(openGaussContractHostEnv)
	if host == "" {
		t.Skipf("set %s to run the openGauss server contract", openGaussContractHostEnv)
	}
	port, err := strconv.Atoi(os.Getenv(openGaussContractPortEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", openGaussContractPortEnv)
	}
	username := os.Getenv(openGaussContractUserEnv)
	password := os.Getenv(openGaussContractPasswordEnv)
	databaseName := os.Getenv(openGaussContractDatabaseEnv)
	expectedVersion := os.Getenv(openGaussExpectedVersionEnv)
	if username == "" || password == "" || databaseName == "" || expectedVersion == "" {
		t.Fatal("openGauss contract username, password, database, and expected product version are required")
	}

	// The registry constructs OpenGaussDriver without optional constructor
	// configuration, so this contract follows that production path.
	driver := &OpenGaussDriver{}
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
	if driver.GetDefaultPort() != 5432 {
		t.Fatalf("default port = %d, want 5432", driver.GetDefaultPort())
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

	t.Run("pinned openGauss product version and selected database", func(t *testing.T) {
		var serverVersion string
		if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&serverVersion); err != nil {
			t.Fatalf("read openGauss product version: %v", err)
		}
		if !strings.Contains(serverVersion, expectedVersion) || !strings.Contains(strings.ToLower(serverVersion), "opengauss") {
			t.Fatalf("server version = %q, want openGauss version containing %q", serverVersion, expectedVersion)
		}

		var selectedDatabase string
		if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&selectedDatabase); err != nil {
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
		var occurred time.Time
		var amount string
		err := db.QueryRowContext(ctx, "SELECT 42::BIGINT, 'contract'::TEXT, TRUE, NULL::TEXT, TIMESTAMPTZ '2026-10-22 12:34:56+00', 1234.56::NUMERIC").
			Scan(&integer, &label, &enabled, &nullable, &occurred, &amount)
		if err != nil {
			t.Fatalf("query and scan openGauss values: %v", err)
		}
		if integer != 42 || label != "contract" || !enabled || nullable.Valid || amount != "1234.56" {
			t.Fatalf("scanned values = (%d, %q, %t, %+v, %s, %q)", integer, label, enabled, nullable, occurred, amount)
		}
		wantTime := time.Date(2026, time.October, 22, 12, 34, 56, 0, time.UTC)
		if !occurred.Equal(wantTime) {
			t.Fatalf("timestamp = %s, want %s", occurred, wantTime)
		}
	})

	t.Run("schema discovery", func(t *testing.T) {
		table := fmt.Sprintf("data_fabric_contract_schema_%d", time.Now().UnixNano())
		if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (id BIGINT, label VARCHAR(32))"); err != nil {
			t.Fatalf("create schema contract table: %v", err)
		}
		defer db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table)

		rows, err := db.QueryContext(ctx, "SELECT column_name, data_type FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 ORDER BY ordinal_position", table)
		if err != nil {
			t.Fatalf("query openGauss schema metadata: %v", err)
		}
		defer rows.Close()
		var columns []string
		for rows.Next() {
			var name, columnType string
			if err := rows.Scan(&name, &columnType); err != nil {
				t.Fatalf("scan openGauss schema metadata: %v", err)
			}
			columns = append(columns, name+" "+columnType)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read openGauss schema metadata: %v", err)
		}
		if fmt.Sprint(columns) != "[id bigint label character varying]" {
			t.Fatalf("schema columns = %v, want [id bigint label character varying]", columns)
		}
	})

	t.Run("batch pagination", func(t *testing.T) {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire connection: %v", err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, "CREATE TEMP TABLE data_fabric_contract_rows (id INTEGER PRIMARY KEY)"); err != nil {
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

	t.Run("read-only session enforcement", func(t *testing.T) {
		readOnlyDSN, applied := readonly.ApplyDSN(model.DatabaseTypeOpenGauss, dsn)
		if !applied {
			t.Fatal("openGauss read-only DSN parameter was not applied")
		}
		readOnlyDB, err := driver.Open(readOnlyDSN)
		if err != nil {
			t.Fatalf("open read-only connection: %v", err)
		}
		defer readOnlyDB.Close()
		var setting string
		if err := readOnlyDB.QueryRowContext(ctx, "SHOW default_transaction_read_only").Scan(&setting); err != nil {
			t.Fatalf("read read-only session setting: %v", err)
		}
		if setting != "on" {
			t.Fatalf("default_transaction_read_only = %q, want on", setting)
		}
		if _, err := readOnlyDB.ExecContext(ctx, "CREATE TEMP TABLE data_fabric_contract_readonly (id INTEGER)"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "read-only") {
			t.Fatalf("write in read-only session error = %v, want a read-only transaction error", err)
		}
	})

	t.Run("query cancellation", func(t *testing.T) {
		queryCtx, stop := context.WithTimeout(ctx, 250*time.Millisecond)
		defer stop()
		started := time.Now()
		var result any
		err := db.QueryRowContext(queryCtx, "SELECT pg_sleep(2)").Scan(&result)
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
