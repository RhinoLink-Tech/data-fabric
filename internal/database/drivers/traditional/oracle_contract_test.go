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

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	oracleContractHostEnv      = "DATA_FABRIC_ORACLE_CONTRACT_HOST"
	oracleContractPortEnv      = "DATA_FABRIC_ORACLE_CONTRACT_PORT"
	oracleContractUserEnv      = "DATA_FABRIC_ORACLE_CONTRACT_USERNAME"
	oracleContractPasswordEnv  = "DATA_FABRIC_ORACLE_CONTRACT_PASSWORD"
	oracleContractServiceEnv   = "DATA_FABRIC_ORACLE_CONTRACT_SERVICE"
	oracleContractAdminUserEnv = "DATA_FABRIC_ORACLE_ADMIN_USERNAME"
	oracleContractAdminPassEnv = "DATA_FABRIC_ORACLE_ADMIN_PASSWORD"
	oracleExpectedVersionEnv   = "DATA_FABRIC_ORACLE_EXPECTED_VERSION"
)

func TestOracleDriverOpenUsesRegisteredDatabaseDriver(t *testing.T) {
	db, err := (&OracleDriver{}).Open("oracle://user:password@127.0.0.1:1521/FREEPDB1")
	if err != nil {
		t.Fatalf("open registered Oracle database driver: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close Oracle database handle: %v", err)
	}
}

func TestOracleDriverContract(t *testing.T) {
	host := os.Getenv(oracleContractHostEnv)
	if host == "" {
		t.Skipf("set %s to run the Oracle server contract", oracleContractHostEnv)
	}
	port, err := strconv.Atoi(os.Getenv(oracleContractPortEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", oracleContractPortEnv)
	}
	service := os.Getenv(oracleContractServiceEnv)
	appUser, appPassword := os.Getenv(oracleContractUserEnv), os.Getenv(oracleContractPasswordEnv)
	adminUser, adminPassword := os.Getenv(oracleContractAdminUserEnv), os.Getenv(oracleContractAdminPassEnv)
	expectedVersion := os.Getenv(oracleExpectedVersionEnv)
	if service == "" || appUser == "" || appPassword == "" || adminUser == "" || adminPassword == "" || expectedVersion == "" {
		t.Fatal("Oracle contract requires service name, app and admin credentials, and exact server version")
	}

	driver := &OracleDriver{}
	if driver.GetDefaultPort() != 1521 {
		t.Fatalf("default port = %d, want 1521", driver.GetDefaultPort())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	open := func(user, password string) *sql.DB {
		t.Helper()
		dsn := driver.BuildDSN(&model.DataSourceConfig{
			Host: host, Port: port, Database: service, Username: user, Password: password,
		})
		if err := driver.ValidateDSN(dsn); err != nil {
			t.Fatalf("ValidateDSN: %v", err)
		}
		db, err := driver.Open(dsn)
		if err != nil {
			t.Fatalf("Open Oracle connection for %s: %v", user, err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := driver.TestConnection(db); err != nil {
			t.Fatalf("TestConnection for %s: %v", user, err)
		}
		return db
	}

	adminDB := open(adminUser, adminPassword)
	var version string
	if err := adminDB.QueryRowContext(ctx, "SELECT version_full FROM v$instance").Scan(&version); err != nil {
		t.Fatalf("read Oracle server version: %v", err)
	}
	if version != expectedVersion {
		t.Fatalf("server version = %q, want %q", version, expectedVersion)
	}

	db := open(appUser, appPassword)
	t.Run("binds, standard values, and NULL scanning", func(t *testing.T) {
		var integer int64
		var label string
		var nullable sql.NullString
		err := db.QueryRowContext(ctx, "SELECT :1, :2, CAST(NULL AS VARCHAR2(20)) FROM DUAL", 42, "contract").
			Scan(&integer, &label, &nullable)
		if err != nil {
			t.Fatalf("query and scan Oracle values: %v", err)
		}
		if integer != 42 || label != "contract" || nullable.Valid {
			t.Fatalf("scanned values = (%d, %q, %+v)", integer, label, nullable)
		}
		var sum int64
		if err := db.QueryRowContext(ctx, "SELECT :1 + :2 FROM DUAL", 19, 23).Scan(&sum); err != nil {
			t.Fatalf("query bind parameters: %v", err)
		}
		if sum != 42 {
			t.Fatalf("bound sum = %d, want 42", sum)
		}
	})

	t.Run("DML, count, and Oracle pagination", func(t *testing.T) {
		table := fmt.Sprintf("DF_CONTRACT_%d", time.Now().UnixNano())
		if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (ID NUMBER(10) PRIMARY KEY, LABEL VARCHAR2(40))"); err != nil {
			t.Fatalf("create table: %v", err)
		}
		defer func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE "+table+" PURGE") }()
		for id := 1; id <= 4; id++ {
			if _, err := db.ExecContext(ctx, "INSERT INTO "+table+" (ID, LABEL) VALUES (:1, :2)", id, fmt.Sprintf("row-%d", id)); err != nil {
				t.Fatalf("insert row %d: %v", id, err)
			}
		}
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE ID >= :1", 2).Scan(&count); err != nil || count != 3 {
			t.Fatalf("count query = %d, %v; want 3 rows", count, err)
		}
		pageSQL, err := driver.ApplyBatchPagination("SELECT ID FROM "+table+" ORDER BY ID", 2, 1)
		if err != nil {
			t.Fatalf("ApplyBatchPagination: %v", err)
		}
		rows, err := db.QueryContext(ctx, pageSQL)
		if err != nil {
			t.Fatalf("execute paginated query %q: %v", pageSQL, err)
		}
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scan paginated row: %v", err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read paginated rows: %v", err)
		}
		if fmt.Sprint(ids) != "[2 3]" {
			t.Fatalf("page = %v, want [2 3]", ids)
		}
	})

	t.Run("database errors are returned", func(t *testing.T) {
		_, err := db.ExecContext(ctx, "SELECT data_fabric_missing_contract_column FROM DUAL")
		if err == nil || strings.TrimSpace(err.Error()) == "" {
			t.Fatalf("invalid query error = %v, want database error", err)
		}
	})
}
