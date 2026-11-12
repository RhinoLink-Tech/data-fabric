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

func TestOSSCSVProtocolFixture(t *testing.T) {
	data := []byte("id,name,active\n1,Ada,true\n2,Lin,false\n3,Cara,true\n")
	var getCalls atomic.Int64
	headLies := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-bucket/contract/records.csv" {
			http.Error(w, "unexpected object path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "" || !strings.Contains(r.Header.Get("Authorization"), "test-access-key") {
			http.Error(w, "missing OSS authorization", http.StatusForbidden)
			return
		}
		contentLength := len(data)
		if headLies && r.Method == http.MethodHead {
			contentLength = 1
		}
		w.Header().Set("Content-Length", strconv.Itoa(contentLength))
		w.Header().Set("ETag", "\"oss-csv-fixture\"")
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
		ID: "oss-csv-protocol-fixture", Type: model.DatabaseTypeOSSCSV,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: "test-bucket", Username: "test-access-key", Password: "test-secret",
			AdditionalProps: map[string]interface{}{
				"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
				"force_path_style": true,
			},
		},
	}

	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate OSS CSV config: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
	if err != nil {
		t.Fatalf("check OSS CSV connectivity: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("OSS CSV health status = %q (message: %s)", health.Status, health.Message)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(context.Background(), source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list OSS CSV tables = %+v, error = %v", tables, err)
	}
	table, err := extractor.ExtractTableSchema(context.Background(), source.ID, "records")
	if err != nil {
		t.Fatalf("describe OSS CSV table: %v", err)
	}
	if len(table.Columns) != 3 || table.Columns[0].Name != "id" || table.Columns[1].Name != "name" || table.Columns[2].Name != "active" {
		t.Fatalf("OSS CSV columns = %+v", table.Columns)
	}

	db, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("open OSS CSV adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD configured OSS object: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT name AS person, id FROM records WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query OSS CSV object: %v", err)
	}
	columns, err := rows.Columns()
	if err != nil || strings.Join(columns, ",") != "person,id" {
		t.Fatalf("OSS CSV query columns = %v, error = %v", columns, err)
	}
	if !rows.Next() {
		t.Fatalf("expected one OSS CSV row, rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan OSS CSV row: %v", err)
	}
	if person != "Ada" || id != 1 {
		t.Fatalf("OSS CSV row = (%q, %d), want (Ada, 1)", person, id)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("OSS CSV query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("count OSS CSV rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("OSS CSV active row count = %d, want 2", count)
	}
	beforeWrite := getCalls.Load()
	if _, err := db.ExecContext(context.Background(), "DELETE FROM records"); err == nil {
		t.Fatal("OSS CSV adapter accepted a write")
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection fetched the object: before=%d after=%d", beforeWrite, afterWrite)
	}

	data = []byte(strings.Repeat("x", 65))
	headLies = true
	limited := source.Config
	limited.AdditionalProps = map[string]interface{}{
		"object_key": objectKey, "endpoint_url": server.URL, "force_path_style": true, "max_file_bytes": 64,
	}
	limitedDB, err := (&object_storage.OSSCSVDriver{}).OpenWithConfig(context.Background(), &limited)
	if err != nil {
		t.Fatalf("open size-limited OSS CSV adapter: %v", err)
	}
	defer limitedDB.Close()
	if err := limitedDB.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD with intentionally inaccurate size: %v", err)
	}
	if _, err := limitedDB.QueryContext(context.Background(), "SELECT * FROM records"); err == nil || !strings.Contains(err.Error(), "64 bytes") {
		t.Fatalf("oversized OSS CSV query error = %v, want 64-byte limit", err)
	}

	data = []byte("id\n" + strings.Repeat("1\n", 100001))
	headLies = false
	rowLimited, err := (&object_storage.OSSCSVDriver{}).OpenWithConfig(context.Background(), &source.Config)
	if err != nil {
		t.Fatalf("open row-limited OSS CSV adapter: %v", err)
	}
	defer rowLimited.Close()
	if _, err := rowLimited.QueryContext(context.Background(), "SELECT COUNT(*) FROM records"); err == nil || !strings.Contains(err.Error(), "100000") {
		t.Fatalf("row limit error = %v, want 100,000-row limit", err)
	}
}

func TestOSSCSVDriverConfigurationAndDSN(t *testing.T) {
	config := &model.DataSourceConfig{
		Host: "warehouse/events.csv", Database: "analytics", Username: "access-key", Password: "secret",
		AdditionalProps: map[string]interface{}{
			"endpoint_url": "oss-cn-hangzhou.aliyuncs.com", "object_key": "warehouse/events.csv",
		},
	}
	normalized, endpoint, err := object_storage.NormalizeOSSCSVConfig(config)
	if err != nil {
		t.Fatalf("normalize OSS CSV config: %v", err)
	}
	if endpoint != "https://oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("endpoint = %q", endpoint)
	}
	if normalized.AdditionalProps["endpoint_url"] != endpoint || config.AdditionalProps["endpoint_url"] != "oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("normalization should copy props: normalized=%#v original=%#v", normalized.AdditionalProps, config.AdditionalProps)
	}
	driver := &object_storage.OSSCSVDriver{}
	dsn := driver.BuildDSN(config)
	if dsn != "oss://analytics/warehouse/events.csv" {
		t.Fatalf("BuildDSN() = %q", dsn)
	}
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
	for _, invalid := range []string{
		"oss://bucket/events.txt", "oss://bucket/../events.csv", "oss://bucket//events.csv",
	} {
		if err := driver.ValidateDSN(invalid); err == nil {
			t.Errorf("ValidateDSN(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestOSSCSVConfigRejectsInvalidSettings(t *testing.T) {
	base := func() *model.DataSourceConfig {
		return &model.DataSourceConfig{
			Host: "events.csv", Database: "analytics", Username: "access-key", Password: "secret",
			AdditionalProps: map[string]interface{}{"endpoint_url": "https://oss.example.test", "object_key": "events.csv"},
		}
	}
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{"missing endpoint", func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "endpoint_url") }, "endpoint_url is required"},
		{"unsafe key", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "../events.csv" }, "safe relative"},
		{"wrong extension", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "events.txt" }, "safe relative"},
		{"empty bucket", func(c *model.DataSourceConfig) { c.Database = "" }, "bucket"},
		{"missing credentials", func(c *model.DataSourceConfig) { c.Password = "" }, "access key ID"},
		{"endpoint path", func(c *model.DataSourceConfig) { c.AdditionalProps["endpoint_url"] = "https://oss.example.test/api" }, "without credentials, path"},
		{"multi-character delimiter", func(c *model.DataSourceConfig) { c.AdditionalProps["delimiter"] = "||" }, "exactly one character"},
		{"unsupported quote", func(c *model.DataSourceConfig) { c.AdditionalProps["quote_char"] = "'" }, "double-quote"},
		{"negative skipped rows", func(c *model.DataSourceConfig) { c.AdditionalProps["skip_header_rows"] = -1 }, "non-negative integer"},
		{"fractional skipped rows", func(c *model.DataSourceConfig) { c.AdditionalProps["skip_header_rows"] = 1.5 }, "non-negative integer"},
		{"oversize limit", func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = 64<<20 + 1 }, "between 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base()
			test.mutate(config)
			err := object_storage.ValidateOSSCSVConfig(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateOSSCSVConfig() error = %v, want containing %q", err, test.want)
			}
		})
	}
}
