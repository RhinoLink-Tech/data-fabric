package olap

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	starRocksContractHostEnv     = "DATA_FABRIC_STARROCKS_CONTRACT_HOST"
	starRocksContractPortEnv     = "DATA_FABRIC_STARROCKS_CONTRACT_PORT"
	starRocksContractUserEnv     = "DATA_FABRIC_STARROCKS_CONTRACT_USERNAME"
	starRocksContractPasswordEnv = "DATA_FABRIC_STARROCKS_CONTRACT_PASSWORD"
	starRocksContractDatabaseEnv = "DATA_FABRIC_STARROCKS_CONTRACT_DATABASE"
	starRocksExpectedVersionEnv  = "DATA_FABRIC_STARROCKS_EXPECTED_VERSION"
)

func TestStarRocksDriverContract(t *testing.T) {
	host := os.Getenv(starRocksContractHostEnv)
	if host == "" {
		t.Skipf("set %s to run the StarRocks server contract", starRocksContractHostEnv)
	}
	port, err := strconv.Atoi(os.Getenv(starRocksContractPortEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", starRocksContractPortEnv)
	}
	username := os.Getenv(starRocksContractUserEnv)
	password := os.Getenv(starRocksContractPasswordEnv)
	databaseName := os.Getenv(starRocksContractDatabaseEnv)
	expectedVersion := os.Getenv(starRocksExpectedVersionEnv)
	if username == "" || password == "" || expectedVersion == "" {
		t.Fatal("StarRocks contract username, password, and expected server version are required")
	}
	if username != "data_fabric_contract" || databaseName != "data_fabric_contract" {
		t.Fatalf("contract must use the disposable data_fabric_contract account and database")
	}

	driver := &StarRocksDriver{}
	if driver.GetDefaultPort() != 9030 {
		t.Fatalf("default port = %d, want 9030", driver.GetDefaultPort())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	adminDSN := driver.BuildDSN(&model.DataSourceConfig{
		Host: host, Port: port, Username: "root",
	})
	adminDB, err := driver.Open(adminDSN)
	if err != nil {
		t.Fatalf("open StarRocks administrator connection: %v", err)
	}
	defer adminDB.Close()
	if _, err := adminDB.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS "+databaseName); err != nil {
		t.Fatalf("create contract database: %v", err)
	}
	if _, err := adminDB.ExecContext(ctx, "DROP USER IF EXISTS '"+username+"'@'%'"); err != nil {
		t.Fatalf("drop leftover contract account: %v", err)
	}
	if _, err := adminDB.ExecContext(ctx, "CREATE USER '"+username+"' IDENTIFIED BY '"+password+"'"); err != nil {
		t.Fatalf("create contract account: %v", err)
	}

	contractDSN := driver.BuildDSN(&model.DataSourceConfig{
		Host: host, Port: port, Database: databaseName, Username: "root",
	})
	setupDB, err := driver.Open(contractDSN)
	if err != nil {
		t.Fatalf("open StarRocks setup connection: %v", err)
	}
	defer setupDB.Close()
	table := "df_contract_records"
	if _, err := setupDB.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatalf("drop leftover contract table: %v", err)
	}
	createTableSQL := "CREATE TABLE df_contract_records (id BIGINT NOT NULL, label VARCHAR(40), active TINYINT) DUPLICATE KEY(id) DISTRIBUTED BY HASH(id) BUCKETS 1 PROPERTIES ('replication_num' = '1')"
	if _, err := setupDB.ExecContext(ctx, createTableSQL); err != nil {
		t.Fatalf("create contract table: %v", err)
	}
	if _, err := setupDB.ExecContext(ctx, "INSERT INTO "+table+" VALUES (1, 'Alice', 1), (2, 'Bob', 0), (3, 'Cara', 1), (4, 'Dana', 1)"); err != nil {
		t.Fatalf("insert contract rows: %v", err)
	}
	if _, err := setupDB.ExecContext(ctx, "GRANT SELECT ON TABLE "+table+" TO USER '"+username+"'@'%'"); err != nil {
		t.Fatalf("grant contract table read privilege: %v", err)
	}

	config := &model.DataSourceConfig{
		Host: host, Port: port, Database: databaseName, Username: username, Password: password,
	}
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN: %v", err)
	}
	db, err := driver.Open(dsn)
	if err != nil {
		t.Fatalf("Open with the contract account: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)
	if err := driver.TestConnection(db); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}

	var serverVersion string
	if err := db.QueryRowContext(ctx, "SELECT current_version()").Scan(&serverVersion); err != nil {
		t.Fatalf("read server version: %v", err)
	}
	t.Logf("StarRocks server version: %s", serverVersion)
	if !strings.HasPrefix(serverVersion, expectedVersion) {
		t.Fatalf("server version = %q, want version prefix %q", serverVersion, expectedVersion)
	}
	var selectedDatabase string
	if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&selectedDatabase); err != nil {
		t.Fatalf("read selected database: %v", err)
	}
	if selectedDatabase != databaseName {
		t.Fatalf("selected database = %q, want %q", selectedDatabase, databaseName)
	}

	t.Run("query values and count", func(t *testing.T) {
		var id int64
		var label string
		var active int64
		err := db.QueryRowContext(ctx, "SELECT id, label, active FROM "+table+" WHERE id = 3").Scan(&id, &label, &active)
		if err != nil {
			t.Fatalf("query and scan StarRocks values: %v", err)
		}
		if id != 3 || label != "Cara" || active != 1 {
			t.Fatalf("scanned values = (%d, %q, %d), want (3, Cara, 1)", id, label, active)
		}
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE active = 1").Scan(&count); err != nil {
			t.Fatalf("query StarRocks count: %v", err)
		}
		if count != 3 {
			t.Fatalf("active row count = %d, want 3", count)
		}
	})

	t.Run("schema discovery and pagination", func(t *testing.T) {
		rows, err := db.QueryContext(ctx, "SHOW COLUMNS FROM "+table)
		if err != nil {
			t.Fatalf("SHOW COLUMNS: %v", err)
		}
		defer rows.Close()
		var columns []string
		for rows.Next() {
			var field, typ, null, key string
			var defaultValue, extra *string
			if err := rows.Scan(&field, &typ, &null, &key, &defaultValue, &extra); err != nil {
				t.Fatalf("scan StarRocks schema column: %v", err)
			}
			columns = append(columns, fmt.Sprintf("%s %s", field, strings.ToUpper(typ)))
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read StarRocks schema rows: %v", err)
		}
		expectedColumns := []string{"id BIGINT", "label VARCHAR(40)", "active TINYINT"}
		if len(columns) != len(expectedColumns) {
			t.Fatalf("schema columns = %v, want %v", columns, expectedColumns)
		}
		for i, got := range columns {
			if !strings.EqualFold(got, expectedColumns[i]) {
				t.Fatalf("schema column %d = %q, want %q", i, got, expectedColumns[i])
			}
		}

		query, err := driver.ApplyBatchPagination("SELECT id FROM "+table+" ORDER BY id", 1, 1)
		if err != nil {
			t.Fatalf("ApplyBatchPagination: %v", err)
		}
		var id int64
		if err := db.QueryRowContext(ctx, query).Scan(&id); err != nil {
			t.Fatalf("query paginated result: %v", err)
		}
		if id != 2 {
			t.Fatalf("paginated row id = %d, want 2", id)
		}
	})

	t.Run("server query errors are returned", func(t *testing.T) {
		var value string
		if err := db.QueryRowContext(ctx, "SELECT missing_contract_column FROM "+table).Scan(&value); err == nil {
			t.Fatal("query with a missing column unexpectedly succeeded")
		}
	})
}
