package traditional

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/readonly"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	postgresContractDSNEnv     = "DATA_FABRIC_POSTGRES_CONTRACT_DSN"
	postgresExpectedVersionEnv = "DATA_FABRIC_POSTGRES_EXPECTED_VERSION"
)

func TestPostgreSQLDriverContract(t *testing.T) {
	configuredDSN := os.Getenv(postgresContractDSNEnv)
	if configuredDSN == "" {
		t.Skipf("set %s to run the PostgreSQL server contract", postgresContractDSNEnv)
	}
	expectedVersion := os.Getenv(postgresExpectedVersionEnv)
	if expectedVersion == "" {
		t.Fatalf("%s is required when the PostgreSQL contract DSN is set", postgresExpectedVersionEnv)
	}

	driver := &PostgreSQLDriver{}
	config := postgresConfigFromURL(t, configuredDSN)
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

	var serverVersion string
	if err := db.QueryRowContext(ctx, "SHOW server_version").Scan(&serverVersion); err != nil {
		t.Fatalf("read server version: %v", err)
	}
	if serverVersion != expectedVersion {
		t.Fatalf("server version = %q, want exact contract version %q", serverVersion, expectedVersion)
	}

	t.Run("query schema and standard SQL values", func(t *testing.T) {
		var schema string
		if err := db.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
			t.Fatalf("query current schema: %v", err)
		}
		if schema != "public" {
			t.Fatalf("current schema = %q, want public", schema)
		}

		var integer int64
		var label string
		var enabled bool
		var nullable sql.NullString
		var occurred time.Time
		var amount string
		err := db.QueryRowContext(ctx, "SELECT 42::BIGINT, 'contract'::TEXT, TRUE, NULL::TEXT, TIMESTAMPTZ '2026-10-22 12:34:56+00', 1234.56::NUMERIC").
			Scan(&integer, &label, &enabled, &nullable, &occurred, &amount)
		if err != nil {
			t.Fatalf("query and scan PostgreSQL values: %v", err)
		}
		if integer != 42 || label != "contract" || !enabled || nullable.Valid || amount != "1234.56" {
			t.Fatalf("scanned values = (%d, %q, %t, %+v, %q)", integer, label, enabled, nullable, amount)
		}
		wantTime := time.Date(2026, time.October, 22, 12, 34, 56, 0, time.UTC)
		if !occurred.Equal(wantTime) {
			t.Fatalf("timestamp = %s, want %s", occurred, wantTime)
		}
	})

	t.Run("batch pagination", func(t *testing.T) {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire connection: %v", err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, "CREATE TEMP TABLE data_fabric_contract_rows (id INTEGER PRIMARY KEY)"); err != nil {
			t.Fatalf("create temporary table: %v", err)
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
				t.Fatalf("scan page: %v", err)
			}
			got = append(got, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read page: %v", err)
		}
		if fmt.Sprint(got) != "[2 3]" {
			t.Fatalf("page rows = %v, want [2 3]", got)
		}
	})

	t.Run("read-only session", func(t *testing.T) {
		readOnlyDSN, applied := readonly.ApplyDSN(model.DatabaseTypePostgreSQL, dsn)
		if !applied {
			t.Fatal("PostgreSQL read-only DSN parameter was not applied")
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
		err := db.QueryRowContext(queryCtx, "SELECT pg_sleep(5)").Scan(&result)
		if err == nil {
			t.Fatal("canceled query unexpectedly succeeded")
		}
		if elapsed := time.Since(started); elapsed > 3*time.Second {
			t.Fatalf("query cancellation took %s, want under 3s", elapsed)
		}
	})

	t.Run("server errors are returned", func(t *testing.T) {
		var result string
		err := db.QueryRowContext(ctx, "SELECT data_fabric_missing_contract_column").Scan(&result)
		if err == nil {
			t.Fatal("query with a missing column unexpectedly succeeded")
		}
	})
}

func postgresConfigFromURL(t *testing.T, rawDSN string) *model.DataSourceConfig {
	t.Helper()
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse contract DSN: %v", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		t.Fatalf("contract DSN scheme = %q, want postgres or postgresql", parsed.Scheme)
	}
	if parsed.User == nil {
		t.Fatal("contract DSN must include a username and password")
	}
	password, hasPassword := parsed.User.Password()
	if !hasPassword {
		t.Fatal("contract DSN must include a password")
	}
	port := 5432
	if parsed.Port() != "" {
		port, err = strconv.Atoi(parsed.Port())
		if err != nil {
			t.Fatalf("parse contract DSN port: %v", err)
		}
	}
	databaseName, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil {
		t.Fatalf("parse contract DSN database: %v", err)
	}
	return &model.DataSourceConfig{
		Host:     parsed.Hostname(),
		Port:     port,
		Database: databaseName,
		Username: parsed.User.Username(),
		Password: password,
		SSL:      parsed.Query().Get("sslmode") == "require",
	}
}
