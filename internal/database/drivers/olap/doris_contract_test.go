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
	dorisContractHostEnv     = "DATA_FABRIC_DORIS_CONTRACT_HOST"
	dorisContractPortEnv     = "DATA_FABRIC_DORIS_CONTRACT_PORT"
	dorisContractUserEnv     = "DATA_FABRIC_DORIS_CONTRACT_USERNAME"
	dorisContractPasswordEnv = "DATA_FABRIC_DORIS_CONTRACT_PASSWORD"
	dorisContractDatabaseEnv = "DATA_FABRIC_DORIS_CONTRACT_DATABASE"
	dorisExpectedVersionEnv  = "DATA_FABRIC_DORIS_EXPECTED_VERSION"
)

func TestDorisDriverContract(t *testing.T) {
	host := os.Getenv(dorisContractHostEnv)
	if host == "" {
		t.Skipf("set %s to run the Doris server contract", dorisContractHostEnv)
	}
	port, err := strconv.Atoi(os.Getenv(dorisContractPortEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", dorisContractPortEnv)
	}
	username := os.Getenv(dorisContractUserEnv)
	password := os.Getenv(dorisContractPasswordEnv)
	databaseName := os.Getenv(dorisContractDatabaseEnv)
	expectedVersion := os.Getenv(dorisExpectedVersionEnv)
	if username == "" || password == "" || databaseName == "" || expectedVersion == "" {
		t.Fatal("Doris contract username, password, database, and expected server version are required")
	}
	if databaseName != "data_fabric_contract" {
		t.Fatalf("contract database must be data_fabric_contract, got %q", databaseName)
	}

	driver := &DorisDriver{}
	if driver.GetDefaultPort() != 9030 {
		t.Fatalf("default port = %d, want 9030", driver.GetDefaultPort())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	adminDSN := driver.BuildDSN(&model.DataSourceConfig{
		Host: host, Port: port, Database: "information_schema", Username: "root",
	})
	adminDB, err := driver.Open(adminDSN)
	if err != nil {
		t.Fatalf("open Doris administrator connection: %v", err)
	}
	defer adminDB.Close()
	if _, err := adminDB.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS "+databaseName); err != nil {
		t.Fatalf("create contract database: %v", err)
	}
	if _, err := adminDB.ExecContext(ctx, "CREATE USER '"+username+"' IDENTIFIED BY '"+password+"'"); err != nil {
		t.Fatalf("create contract account: %v", err)
	}
	if _, err := adminDB.ExecContext(ctx, "GRANT ALL ON internal."+databaseName+".* TO '"+username+"'@'%'"); err != nil {
		t.Fatalf("grant contract database privileges: %v", err)
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
	if err := db.QueryRowContext(ctx, "SELECT version FROM frontends() LIMIT 1").Scan(&serverVersion); err != nil {
		t.Fatalf("read server version: %v", err)
	}
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

	table := "df_contract_records"
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatalf("drop leftover contract table: %v", err)
	}
	createTableSQL := "CREATE TABLE df_contract_records (id BIGINT NOT NULL, label VARCHAR(40), active TINYINT) ENGINE=OLAP DUPLICATE KEY(id) DISTRIBUTED BY HASH(id) BUCKETS 1 PROPERTIES ('replication_num' = '1')"
	if _, err := db.ExecContext(ctx, createTableSQL); err != nil {
		t.Fatalf("create contract table: %v", err)
	}
	defer func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table) }()
	if _, err := db.ExecContext(ctx, "INSERT INTO "+table+" VALUES (1, 'Alice', 1), (2, 'Bob', 0), (3, 'Cara', 1), (4, 'Dana', 1)"); err != nil {
		t.Fatalf("insert contract rows: %v", err)
	}

	t.Run("query values and count", func(t *testing.T) {
		var id int64
		var label string
		var active int64
		err := db.QueryRowContext(ctx, "SELECT id, label, active FROM "+table+" WHERE id = 3").Scan(&id, &label, &active)
		if err != nil {
			t.Fatalf("query and scan Doris values: %v", err)
		}
		if id != 3 || label != "Cara" || active != 1 {
			t.Fatalf("scanned values = (%d, %q, %d), want (3, Cara, 1)", id, label, active)
		}
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE active = 1").Scan(&count); err != nil {
			t.Fatalf("query Doris count: %v", err)
		}
		if count != 3 {
			t.Fatalf("active row count = %d, want 3", count)
		}
	})

	t.Run("schema discovery and pagination", func(t *testing.T) {
		schema, err := driver.GetTableSchema(ctx, db, table)
		if err != nil {
			t.Fatalf("GetTableSchema: %v", err)
		}
		var columns []string
		for _, column := range schema.Columns {
			columns = append(columns, fmt.Sprintf("%s %s", derefString(column.Name), derefString(column.Type)))
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

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
