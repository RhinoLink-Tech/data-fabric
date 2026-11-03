package object_storage_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestCOSCSVProtocolFixture(t *testing.T) {
	data := []byte("generated report\nid;name;active;note\n1;Ada;true;NULL\n2;Lin;false;NULL\n3;Cara;true;NULL\n")
	var getCalls atomic.Int64
	headLies := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/contract/records.csv" {
			http.Error(w, "unexpected object path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "missing COS authorization", http.StatusForbidden)
			return
		}
		contentLength := len(data)
		if headLies && r.Method == http.MethodHead {
			contentLength = 1
		}
		w.Header().Set("Content-Length", strconv.Itoa(contentLength))
		w.Header().Set("ETag", "\"cos-csv-fixture\"")
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			getCalls.Add(1)
			_, _ = w.Write(data)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	const objectKey = "contract/records.csv"
	source := &model.DataSource{
		ID: "cos-csv-protocol-fixture", Type: model.DatabaseTypeTencentCOSCSV,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: "test-bucket-1250000000", Username: "test-secret-id", Password: "test-secret-key",
			AdditionalProps: map[string]interface{}{
				"object_key": objectKey, "table_name": "records", "region": "ap-guangzhou", "endpoint_url": server.URL,
				"delimiter": ";", "skip_header_rows": 1, "null_values": []string{"NULL"},
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate COS CSV config: %v", err)
	}
	invalid := source.Config
	invalid.AdditionalProps = map[string]interface{}{"object_key": objectKey, "endpoint_url": server.URL}
	if err := checker.ValidateDataSourceConfiguration(&invalid, source.Type); err == nil {
		t.Fatal("COS CSV config validation accepted a missing region")
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
	if err != nil {
		t.Fatalf("check COS CSV connectivity: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("COS CSV health status = %q (message: %s)", health.Status, health.Message)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(context.Background(), source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list COS CSV tables = %+v, error = %v", tables, err)
	}
	table, err := extractor.ExtractTableSchema(context.Background(), source.ID, "records")
	if err != nil {
		t.Fatalf("describe COS CSV table: %v", err)
	}
	if len(table.Columns) != 4 || table.Columns[0].Name != "id" || table.Columns[1].Name != "name" || table.Columns[2].Name != "active" || table.Columns[3].Name != "note" {
		t.Fatalf("COS CSV columns = %+v", table.Columns)
	}

	db, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("open COS CSV adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD configured COS object: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT name AS person, id FROM records WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query COS CSV object: %v", err)
	}
	columns, err := rows.Columns()
	if err != nil || strings.Join(columns, ",") != "person,id" {
		t.Fatalf("COS CSV query columns = %v, error = %v", columns, err)
	}
	if !rows.Next() {
		t.Fatalf("expected one COS CSV row, rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan COS CSV row: %v", err)
	}
	if person != "Ada" || id != 1 {
		t.Fatalf("COS CSV row = (%q, %d), want (Ada, 1)", person, id)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("COS CSV query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("count COS CSV rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("COS CSV active row count = %d, want 2", count)
	}
	var note any
	if err := db.QueryRowContext(context.Background(), "SELECT note FROM records WHERE id = 1").Scan(&note); err != nil {
		t.Fatalf("read configured null marker: %v", err)
	}
	if note != nil {
		t.Fatalf("configured null marker = %#v, want nil", note)
	}
	beforeWrite := getCalls.Load()
	if _, err := db.ExecContext(context.Background(), "DELETE FROM records"); err == nil {
		t.Fatal("COS CSV adapter accepted a write")
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection fetched the object: before=%d after=%d", beforeWrite, afterWrite)
	}

	data = []byte(strings.Repeat("x", 65))
	headLies = true
	limitedConfig := source.Config
	limitedConfig.AdditionalProps = map[string]interface{}{
		"object_key": objectKey, "region": "ap-guangzhou", "endpoint_url": server.URL,
		"max_file_bytes": 64,
	}
	limited, err := (&object_storage.COSCSVDriver{}).OpenWithConfig(context.Background(), &limitedConfig)
	if err != nil {
		t.Fatalf("open size-limited COS CSV adapter: %v", err)
	}
	defer limited.Close()
	if err := limited.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD with intentionally inaccurate size: %v", err)
	}
	if _, err := limited.QueryContext(context.Background(), "SELECT * FROM records"); err == nil || !strings.Contains(err.Error(), "64 bytes") {
		t.Fatalf("oversized COS CSV query error = %v, want 64-byte limit", err)
	}

	data = []byte("id\n" + strings.Repeat("1\n", 100001))
	headLies = false
	rowLimitedConfig := source.Config
	rowLimitedConfig.AdditionalProps = map[string]interface{}{
		"object_key": objectKey, "region": "ap-guangzhou", "endpoint_url": server.URL,
	}
	rowLimited, err := (&object_storage.COSCSVDriver{}).OpenWithConfig(context.Background(), &rowLimitedConfig)
	if err != nil {
		t.Fatalf("open row-limited COS CSV adapter: %v", err)
	}
	defer rowLimited.Close()
	if _, err := rowLimited.QueryContext(context.Background(), "SELECT COUNT(*) FROM records"); err == nil || !strings.Contains(err.Error(), "100000") {
		t.Fatalf("row-limit error = %v, want 100,000-row limit", err)
	}
}

func TestCOSCSVDriverConfigurationAndDSN(t *testing.T) {
	config := &model.DataSourceConfig{
		Host: "warehouse/events.csv", Database: "analytics-1250000000", Username: "secret-id", Password: "secret-key",
		AdditionalProps: map[string]interface{}{"region": "ap-guangzhou", "endpoint_url": "https://cos.example.test"},
	}
	normalized, err := object_storage.NormalizeCOSCSVConfig(config)
	if err != nil {
		t.Fatalf("normalize COS CSV config: %v", err)
	}
	if normalized.AdditionalProps["endpoint_url"] != config.AdditionalProps["endpoint_url"] {
		t.Fatalf("normalization changed the endpoint: %#v", normalized.AdditionalProps)
	}
	driver := &object_storage.COSCSVDriver{}
	dsn := driver.BuildDSN(config)
	if dsn != "cos://analytics-1250000000/warehouse/events.csv" {
		t.Fatalf("BuildDSN() = %q", dsn)
	}
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
	for _, invalid := range []string{
		"cos://bucket/events.txt", "cos://bucket/../events.csv", "cos://bucket//events.csv",
	} {
		if err := driver.ValidateDSN(invalid); err == nil {
			t.Errorf("ValidateDSN(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestCOSCSVConfigRejectsInvalidSettings(t *testing.T) {
	base := func() *model.DataSourceConfig {
		return &model.DataSourceConfig{
			Host: "events.csv", Database: "analytics-1250000000", Username: "secret-id", Password: "secret-key",
			AdditionalProps: map[string]interface{}{"region": "ap-guangzhou"},
		}
	}
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{"missing region", func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "region") }, "region is required"},
		{"unsafe key", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "../events.csv" }, "safe relative"},
		{"wrong extension", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "events.txt" }, "safe relative"},
		{"empty bucket", func(c *model.DataSourceConfig) { c.Database = "" }, "bucket"},
		{"missing credentials", func(c *model.DataSourceConfig) { c.Password = "" }, "SecretId and SecretKey"},
		{"endpoint path", func(c *model.DataSourceConfig) { c.AdditionalProps["endpoint_url"] = "https://cos.example.test/api" }, "without credentials, path"},
		{"multi-character delimiter", func(c *model.DataSourceConfig) { c.AdditionalProps["delimiter"] = "||" }, "exactly one character"},
		{"unsupported quote", func(c *model.DataSourceConfig) { c.AdditionalProps["quote_char"] = "'" }, "double-quote"},
		{"negative skipped rows", func(c *model.DataSourceConfig) { c.AdditionalProps["skip_header_rows"] = -1 }, "non-negative integer"},
		{"fractional skipped rows", func(c *model.DataSourceConfig) { c.AdditionalProps["skip_header_rows"] = 1.5 }, "non-negative integer"},
		{"invalid HTTPS flag", func(c *model.DataSourceConfig) { c.AdditionalProps["https"] = "sometimes" }, "https must be true or false"},
		{"oversize limit", func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = 64<<20 + 1 }, "between 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base()
			test.mutate(config)
			err := object_storage.ValidateCOSCSVConfig(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateCOSCSVConfig() error = %v, want containing %q", err, test.want)
			}
		})
	}
}
