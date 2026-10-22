package metadata

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestDorisMetadataExtractorContract(t *testing.T) {
	runMySQLWireOLAPMetadataContract(t, model.DatabaseTypeApacheDoris, "DATA_FABRIC_DORIS_CONTRACT_HOST", "DATA_FABRIC_DORIS_CONTRACT_PORT", "DATA_FABRIC_DORIS_CONTRACT_USERNAME", "DATA_FABRIC_DORIS_CONTRACT_PASSWORD", "DATA_FABRIC_DORIS_CONTRACT_DATABASE")
}

func TestStarRocksMetadataExtractorContract(t *testing.T) {
	runMySQLWireOLAPMetadataContract(t, model.DatabaseTypeStarRocks, "DATA_FABRIC_STARROCKS_CONTRACT_HOST", "DATA_FABRIC_STARROCKS_CONTRACT_PORT", "DATA_FABRIC_STARROCKS_CONTRACT_USERNAME", "DATA_FABRIC_STARROCKS_CONTRACT_PASSWORD", "DATA_FABRIC_STARROCKS_CONTRACT_DATABASE")
}

func runMySQLWireOLAPMetadataContract(t *testing.T, dbType model.DatabaseType, hostEnv, portEnv, userEnv, passwordEnv, databaseEnv string) {
	t.Helper()
	host := os.Getenv(hostEnv)
	if host == "" {
		t.Skipf("set %s to run the %s metadata contract", hostEnv, dbType)
	}
	port, err := strconv.Atoi(os.Getenv(portEnv))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("%s must be a valid TCP port", portEnv)
	}
	username := os.Getenv(userEnv)
	password := os.Getenv(passwordEnv)
	databaseName := os.Getenv(databaseEnv)
	if username == "" || password == "" || databaseName != "data_fabric_contract" {
		t.Fatalf("%s contract credentials and the fixed test database are required", dbType)
	}

	registry := database.NewDriverRegistry()
	driver, err := registry.GetDriver(dbType)
	if err != nil {
		t.Fatalf("get %s driver: %v", dbType, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	adminConfig := &model.DataSourceConfig{Host: host, Port: port, Database: databaseName, Username: "root"}
	adminDB, err := driver.Open(driver.BuildDSN(adminConfig))
	if err != nil {
		t.Fatalf("open %s fixture setup connection: %v", dbType, err)
	}
	defer adminDB.Close()

	tableName := "df_metadata_contract_records"
	if _, err := adminDB.ExecContext(ctx, "DROP TABLE IF EXISTS "+tableName); err != nil {
		t.Fatalf("drop leftover %s metadata table: %v", dbType, err)
	}
	createTableSQL := fmt.Sprintf("CREATE TABLE %s (id BIGINT NOT NULL, label VARCHAR(40), active TINYINT)", tableName)
	if dbType == model.DatabaseTypeApacheDoris {
		createTableSQL += " ENGINE=OLAP DUPLICATE KEY(id) DISTRIBUTED BY HASH(id) BUCKETS 1 PROPERTIES ('replication_num' = '1')"
	} else {
		createTableSQL += " DUPLICATE KEY(id) DISTRIBUTED BY HASH(id) BUCKETS 1 PROPERTIES ('replication_num' = '1')"
	}
	if _, err := adminDB.ExecContext(ctx, createTableSQL); err != nil {
		t.Fatalf("create %s metadata table: %v", dbType, err)
	}
	defer func() { _, _ = adminDB.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+tableName) }()
	if _, err := adminDB.ExecContext(ctx, "INSERT INTO "+tableName+" VALUES (1, 'metadata-contract', 1)"); err != nil {
		t.Fatalf("insert %s metadata row: %v", dbType, err)
	}
	if dbType == model.DatabaseTypeStarRocks {
		if _, err := adminDB.ExecContext(ctx, "GRANT SELECT ON TABLE "+tableName+" TO USER '"+username+"'@'%'"); err != nil {
			t.Fatalf("grant StarRocks metadata table read privilege: %v", err)
		}
	}

	appConfig := &model.DataSourceConfig{Host: host, Port: port, Database: databaseName, Username: username, Password: password}
	appDB, err := driver.Open(driver.BuildDSN(appConfig))
	if err != nil {
		t.Fatalf("open %s application connection: %v", dbType, err)
	}
	defer appDB.Close()

	source := &model.DataSource{ID: "olap-metadata-contract", Type: dbType, Config: *appConfig}
	extractor := &MetadataExtractor{
		connPool: &cannedConnectionProvider{db: appDB},
		sources:  &dataSourceLookupStub{dataSource: source},
		registry: registry,
	}
	schema, err := extractor.ExtractTableSchemaInSchema(ctx, source.ID, databaseName, tableName)
	if err != nil {
		t.Fatalf("extract %s table schema through metadata API: %v", dbType, err)
	}
	if schema.Name != tableName || schema.Schema != databaseName || len(schema.Columns) != 3 {
		t.Fatalf("%s table metadata = %+v, want table %q in schema %q with 3 columns", dbType, schema, tableName, databaseName)
	}
	expectedNames := []string{"id", "label", "active"}
	for i, column := range schema.Columns {
		if !strings.EqualFold(column.Name, expectedNames[i]) || column.Type == "" {
			t.Fatalf("%s column %d = %+v, want name %q and a nonempty type", dbType, i, column, expectedNames[i])
		}
	}
	if schema.Columns[0].Nullable {
		t.Errorf("%s id column should be non-nullable: %+v", dbType, schema.Columns[0])
	}
	if !schema.Columns[1].Nullable || !schema.Columns[2].Nullable {
		t.Errorf("%s optional columns should be nullable: %+v", dbType, schema.Columns)
	}
}
