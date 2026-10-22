package traditional

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/readonly"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/denisenkom/go-mssqldb/msdsn"
)

const (
	sqlServerContractHostEnv     = "DATA_FABRIC_SQLSERVER_CONTRACT_HOST"
	sqlServerContractPortEnv     = "DATA_FABRIC_SQLSERVER_CONTRACT_PORT"
	sqlServerContractUserEnv     = "DATA_FABRIC_SQLSERVER_CONTRACT_USERNAME"
	sqlServerContractPasswordEnv = "DATA_FABRIC_SQLSERVER_CONTRACT_PASSWORD"
	sqlServerContractDatabaseEnv = "DATA_FABRIC_SQLSERVER_CONTRACT_DATABASE"
	sqlServerExpectedVersionEnv  = "DATA_FABRIC_SQLSERVER_EXPECTED_VERSION"
)

func TestSQLServerDriverContract(t *testing.T) {
	host := os.Getenv(sqlServerContractHostEnv)
	if host == "" {
		t.Skipf("set %s to run the SQL Server contract", sqlServerContractHostEnv)
	}
	port, err := strconv.Atoi(os.Getenv(sqlServerContractPortEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", sqlServerContractPortEnv)
	}
	username := os.Getenv(sqlServerContractUserEnv)
	password := os.Getenv(sqlServerContractPasswordEnv)
	databaseName := os.Getenv(sqlServerContractDatabaseEnv)
	expectedVersion := os.Getenv(sqlServerExpectedVersionEnv)
	if username == "" || password == "" || databaseName == "" || expectedVersion == "" {
		t.Fatal("SQL Server contract username, password, database, and expected server version are required")
	}

	driver := NewSQLServerDriver()
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
	parsedDSN, _, err := msdsn.Parse(dsn)
	if err != nil {
		t.Fatalf("parse built SQL Server DSN: %v", err)
	}
	if parsedDSN.Host != host || parsedDSN.Port != uint64(port) || parsedDSN.Database != databaseName || parsedDSN.User != username || parsedDSN.Password != password {
		t.Fatalf("built SQL Server DSN did not preserve the configured endpoint and credentials")
	}
	if driver.GetDefaultPort() != 1433 {
		t.Fatalf("default port = %d, want 1433", driver.GetDefaultPort())
	}

	endpoint := net.JoinHostPort(host, strconv.Itoa(port))
	tcpConn, err := net.DialTimeout("tcp", endpoint, 3*time.Second)
	if err != nil {
		t.Fatalf("open TCP connection to SQL Server at %s: %v", endpoint, err)
	}
	if err := tcpConn.Close(); err != nil {
		t.Fatalf("close TCP probe connection: %v", err)
	}

	db, err := driver.Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var lastPingErr error
	for {
		pingErr := db.PingContext(ctx)
		if pingErr == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("wait for SQL Server at %s: %v (last ping error: %v)", endpoint, ctx.Err(), lastPingErr)
		}
		lastPingErr = pingErr
		time.Sleep(250 * time.Millisecond)
	}
	if err := driver.TestConnection(db); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}

	var serverVersion string
	if err := db.QueryRowContext(ctx, "SELECT CONVERT(nvarchar(128), SERVERPROPERTY('ProductVersion'))").Scan(&serverVersion); err != nil {
		t.Fatalf("read server version: %v", err)
	}
	if serverVersion != expectedVersion {
		t.Fatalf("server version = %q, want exact contract version %q", serverVersion, expectedVersion)
	}

	t.Run("database and standard SQL values", func(t *testing.T) {
		var currentDatabase string
		if err := db.QueryRowContext(ctx, "SELECT DB_NAME()").Scan(&currentDatabase); err != nil {
			t.Fatalf("query current database: %v", err)
		}
		if currentDatabase != databaseName {
			t.Fatalf("current database = %q, want %q", currentDatabase, databaseName)
		}

		var integer int64
		var label string
		var enabled bool
		var nullable sql.NullString
		var amount string
		err := db.QueryRowContext(ctx, "SELECT CAST(42 AS BIGINT), CAST(N'contract' AS NVARCHAR(100)), CAST(1 AS BIT), CAST(NULL AS NVARCHAR(20)), CAST(1234.56 AS DECIMAL(10,2))").
			Scan(&integer, &label, &enabled, &nullable, &amount)
		if err != nil {
			t.Fatalf("query and scan SQL Server values: %v", err)
		}
		if integer != 42 || label != "contract" || !enabled || nullable.Valid || amount != "1234.56" {
			t.Fatalf("scanned values = (%d, %q, %t, %+v, %q)", integer, label, enabled, nullable, amount)
		}
	})

	t.Run("batch pagination", func(t *testing.T) {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire connection: %v", err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, "CREATE TABLE #data_fabric_contract_rows (id INTEGER PRIMARY KEY)"); err != nil {
			t.Fatalf("create temporary table: %v", err)
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO #data_fabric_contract_rows (id) VALUES (1), (2), (3), (4)"); err != nil {
			t.Fatalf("insert pagination rows: %v", err)
		}
		query, err := driver.ApplyBatchPagination("SELECT id FROM #data_fabric_contract_rows ORDER BY id", 2, 1)
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

	t.Run("read-only enforcement remains explicitly unsupported", func(t *testing.T) {
		if mode := readonly.ModeFor(model.DatabaseTypeSQLServer); mode != readonly.ModeUnsupported {
			t.Fatalf("database-side read-only mode = %v, want unsupported", mode)
		}
	})

	t.Run("query cancellation", func(t *testing.T) {
		queryCtx, stop := context.WithTimeout(ctx, 250*time.Millisecond)
		defer stop()
		started := time.Now()
		var result any
		err := db.QueryRowContext(queryCtx, "WAITFOR DELAY '00:00:05'; SELECT 1").Scan(&result)
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
		if !strings.Contains(strings.ToLower(err.Error()), "invalid column") {
			t.Fatalf("missing-column error = %v, want SQL Server invalid-column error", err)
		}
	})
}
