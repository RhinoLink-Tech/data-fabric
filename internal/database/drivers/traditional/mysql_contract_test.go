package traditional

import (
	"context"
	"database/sql"
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
	mysqlContractHostEnv       = "DATA_FABRIC_MYSQL_CONTRACT_HOST"
	mysqlContractPortEnv       = "DATA_FABRIC_MYSQL_CONTRACT_PORT"
	mysqlContractUserEnv       = "DATA_FABRIC_MYSQL_CONTRACT_USERNAME"
	mysqlContractPasswordEnv   = "DATA_FABRIC_MYSQL_CONTRACT_PASSWORD"
	mysqlContractDatabaseEnv   = "DATA_FABRIC_MYSQL_CONTRACT_DATABASE"
	mysqlExpectedVersionEnv    = "DATA_FABRIC_MYSQL_EXPECTED_VERSION"
	mariaDBContractHostEnv     = "DATA_FABRIC_MARIADB_CONTRACT_HOST"
	mariaDBContractPortEnv     = "DATA_FABRIC_MARIADB_CONTRACT_PORT"
	mariaDBContractUserEnv     = "DATA_FABRIC_MARIADB_CONTRACT_USERNAME"
	mariaDBContractPasswordEnv = "DATA_FABRIC_MARIADB_CONTRACT_PASSWORD"
	mariaDBContractDatabaseEnv = "DATA_FABRIC_MARIADB_CONTRACT_DATABASE"
	mariaDBExpectedVersionEnv  = "DATA_FABRIC_MARIADB_EXPECTED_VERSION"
)

func TestMySQLDriverContract(t *testing.T) {
	runMySQLProtocolContract(t, model.DatabaseTypeMySQL)
}

func TestMariaDBDriverContract(t *testing.T) {
	if os.Getenv(mariaDBContractHostEnv) == "" {
		t.Skipf("set %s to run the MariaDB server contract", mariaDBContractHostEnv)
	}
	t.Setenv(mysqlContractHostEnv, os.Getenv(mariaDBContractHostEnv))
	t.Setenv(mysqlContractPortEnv, os.Getenv(mariaDBContractPortEnv))
	t.Setenv(mysqlContractUserEnv, os.Getenv(mariaDBContractUserEnv))
	t.Setenv(mysqlContractPasswordEnv, os.Getenv(mariaDBContractPasswordEnv))
	t.Setenv(mysqlContractDatabaseEnv, os.Getenv(mariaDBContractDatabaseEnv))
	t.Setenv(mysqlExpectedVersionEnv, os.Getenv(mariaDBExpectedVersionEnv))
	runMySQLProtocolContract(t, model.DatabaseTypeMariaDB)
}

func runMySQLProtocolContract(t *testing.T, dbType model.DatabaseType) {
	host := os.Getenv(mysqlContractHostEnv)
	if host == "" {
		t.Skipf("set %s to run the MySQL server contract", mysqlContractHostEnv)
	}
	port, err := strconv.Atoi(os.Getenv(mysqlContractPortEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", mysqlContractPortEnv)
	}
	username := os.Getenv(mysqlContractUserEnv)
	password := os.Getenv(mysqlContractPasswordEnv)
	databaseName := os.Getenv(mysqlContractDatabaseEnv)
	expectedVersion := os.Getenv(mysqlExpectedVersionEnv)
	if username == "" || password == "" || databaseName == "" || expectedVersion == "" {
		t.Fatalf("MySQL contract username, password, database, and expected server version are required")
	}

	driver := &MySQLDriver{}
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
	if driver.GetDefaultPort() != 3306 {
		t.Fatalf("default port = %d, want 3306", driver.GetDefaultPort())
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
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&serverVersion); err != nil {
		t.Fatalf("read server version: %v", err)
	}
	observedVersion := serverVersion
	if dbType == model.DatabaseTypeMariaDB {
		if !strings.Contains(strings.ToLower(serverVersion), "mariadb") {
			t.Fatalf("server version %q does not identify MariaDB", serverVersion)
		}
		observedVersion = strings.SplitN(serverVersion, "-", 2)[0]
	}
	if observedVersion != expectedVersion {
		t.Fatalf("%s server version = %q, want exact contract version %q", dbType, observedVersion, expectedVersion)
	}

	t.Run("query schema and standard SQL values", func(t *testing.T) {
		var schema string
		if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schema); err != nil {
			t.Fatalf("query current database: %v", err)
		}
		if schema != databaseName {
			t.Fatalf("current database = %q, want %q", schema, databaseName)
		}

		var integer int64
		var label string
		var enabled bool
		var nullable sql.NullString
		var amount string
		err := db.QueryRowContext(ctx, "SELECT CAST(42 AS SIGNED), CAST('contract' AS CHAR), TRUE, CAST(NULL AS CHAR), CAST(1234.56 AS DECIMAL(10,2))").
			Scan(&integer, &label, &enabled, &nullable, &amount)
		if err != nil {
			t.Fatalf("query and scan MySQL values: %v", err)
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
		if _, err := conn.ExecContext(ctx, "CREATE TEMPORARY TABLE data_fabric_contract_rows (id INTEGER PRIMARY KEY)"); err != nil {
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
		table := fmt.Sprintf("data_fabric_contract_readonly_%d", time.Now().UnixNano())
		if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (id INTEGER PRIMARY KEY)"); err != nil {
			t.Fatalf("create read-only test table: %v", err)
		}
		defer db.ExecContext(context.Background(), "DROP TABLE "+table)

		readOnlyDB, handled, err := readonly.OpenDB(dbType, dsn)
		if err != nil {
			t.Fatalf("open read-only connection: %v", err)
		}
		if !handled {
			t.Fatal("MySQL database-side read-only connector was not selected")
		}
		defer readOnlyDB.Close()
		var count int
		if err := readOnlyDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("read through read-only session: %v", err)
		}
		if _, err := readOnlyDB.ExecContext(ctx, "INSERT INTO "+table+" (id) VALUES (1)"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "read only") {
			t.Fatalf("write in read-only session error = %v, want a read-only transaction error", err)
		}
	})

	t.Run("query cancellation", func(t *testing.T) {
		queryCtx, stop := context.WithTimeout(ctx, 250*time.Millisecond)
		defer stop()
		started := time.Now()
		var result sql.NullInt64
		err := db.QueryRowContext(queryCtx, "SELECT SLEEP(5)").Scan(&result)
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
