package nosql

import (
	"context"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestMongoDriverCreation(t *testing.T) {
	driver := NewMongoDriver()
	if driver == nil {
		t.Fatal("expected MongoDriver instance, got nil")
	}
	if driver.GetDatabaseTypeName() != string(model.DatabaseTypeMongoDB) {
		t.Fatalf("database type = %q, want %q", driver.GetDatabaseTypeName(), model.DatabaseTypeMongoDB)
	}
	if driver.GetCategory() != drivers.CategoryNoSQL {
		t.Fatalf("category = %q, want %q", driver.GetCategory(), drivers.CategoryNoSQL)
	}
}

func TestMongoDriverCapabilitiesDescribeGatewayAdapter(t *testing.T) {
	caps := NewMongoDriver().GetCapabilities()
	if !caps.SupportsSQL {
		t.Fatal("expected the gateway SQL subset to be available")
	}
	if caps.SupportsTransaction {
		t.Fatal("the SQL adapter does not expose transactions")
	}
	if caps.SupportsSchemaDiscovery {
		t.Fatal("MongoDB schema discovery is not wired into the gateway metadata API")
	}
	if caps.SupportsTimeTravel || caps.RequiresTokenRotation || !caps.SupportsStreaming {
		t.Fatalf("unexpected MongoDB capabilities: %+v", caps)
	}
}

func TestMongoDriverDefaultPortAndDSN(t *testing.T) {
	driver := NewMongoDriver()
	if got := driver.GetDefaultPort(); got != 27017 {
		t.Fatalf("default port = %d, want 27017", got)
	}
	config := &model.DataSourceConfig{
		Host: "localhost", Database: "analytics", Username: "reader", Password: "p@ss",
		AdditionalProps: map[string]interface{}{"authSource": "admin", "tls": true},
	}
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN(%q): %v", dsn, err)
	}
	if !strings.Contains(dsn, "mongodb://reader:p%40ss@localhost:27017/analytics") {
		t.Fatalf("BuildDSN() did not encode credentials or default port: %q", dsn)
	}
	if !strings.Contains(dsn, "authSource=admin") || !strings.Contains(dsn, "tls=true") {
		t.Fatalf("BuildDSN() omitted options: %q", dsn)
	}
	if err := driver.ValidateDSN("mongodb://localhost:27017"); err == nil {
		t.Fatal("ValidateDSN() accepted a URI without a database name")
	}
}

func TestMongoDriverBatchPaginationUsesSQL(t *testing.T) {
	driver := NewMongoDriver()
	got, err := driver.ApplyBatchPagination("SELECT name FROM customers ORDER BY id", 10, 20)
	if err != nil {
		t.Fatalf("ApplyBatchPagination() error = %v", err)
	}
	want := "SELECT * FROM (SELECT name FROM customers ORDER BY id) AS __data_fabric_page LIMIT 10 OFFSET 20"
	if got != want {
		t.Fatalf("ApplyBatchPagination() = %q, want %q", got, want)
	}
	if _, err := driver.ApplyBatchPagination("SELECT * FROM customers", 0, 0); err == nil {
		t.Fatal("ApplyBatchPagination() accepted a non-positive batch size")
	}
}

func TestMongoDriverPingDoesNotReportFalseSuccess(t *testing.T) {
	driver := NewMongoDriver()
	db, err := driver.Open("mongodb://127.0.0.1:1/test?serverSelectionTimeoutMS=100&connectTimeoutMS=100")
	if err != nil {
		t.Fatalf("Open() returned an error before a connection attempt: %v", err)
	}
	defer db.Close()
	if err := driver.TestConnection(db); err == nil {
		t.Fatal("TestConnection() returned success for an unreachable MongoDB endpoint")
	}
}

func TestMongoConnectorSharesClientAndReleasesAfterLastSQLConnection(t *testing.T) {
	connector := &mongoSQLConnector{
		uri:      "mongodb://127.0.0.1:27017/analytics?serverSelectionTimeoutMS=100",
		database: "analytics",
	}
	firstDriverConn, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatalf("first Connect() error = %v", err)
	}
	first := firstDriverConn.(*mongoSQLConn)
	secondDriverConn, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatalf("second Connect() error = %v", err)
	}
	second := secondDriverConn.(*mongoSQLConn)
	if first.client != second.client {
		t.Fatal("database/sql connections do not share the native MongoDB client")
	}
	if connector.refs != 2 {
		t.Fatalf("connector refs = %d, want 2", connector.refs)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if connector.client == nil || connector.refs != 1 {
		t.Fatalf("client released while another SQL connection is active: client=%v refs=%d", connector.client, connector.refs)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if connector.client != nil || connector.refs != 0 {
		t.Fatalf("last Close() did not release client: client=%v refs=%d", connector.client, connector.refs)
	}
}
