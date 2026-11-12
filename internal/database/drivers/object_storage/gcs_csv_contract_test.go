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

func TestGCSCSVProtocolFixture(t *testing.T) {
	data := atomic.Value{}
	data.Store([]byte("id,name,active\n1,Ada,true\n2,Lin,false\n3,Cara,true\n"))
	var signedRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-bucket/contract/records.csv" {
			http.Error(w, "unexpected object path", http.StatusNotFound)
			return
		}
		authorization := r.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 ") ||
			!strings.Contains(authorization, "Credential=test-hmac-id/") ||
			!strings.Contains(authorization, "/us-central1/s3/aws4_request") ||
			!strings.Contains(authorization, "Signature=") {
			http.Error(w, "missing GCS XML API HMAC interoperability signature", http.StatusForbidden)
			return
		}
		signedRequests.Add(1)
		object := data.Load().([]byte)
		w.Header().Set("Content-Length", strconv.Itoa(len(object)))
		w.Header().Set("ETag", "\"gcs-csv-fixture\"")
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			_, _ = w.Write(object)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	const objectKey = "contract/records.csv"
	source := &model.DataSource{
		ID: "gcs-csv-protocol-fixture", Type: model.DatabaseTypeGCSCSV,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: "test-bucket", Username: "test-hmac-id", Password: "test-hmac-secret",
			AdditionalProps: map[string]interface{}{
				"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
				"region": "us-central1", "force_path_style": true,
			},
		},
	}

	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate GCS CSV config: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
	if err != nil {
		t.Fatalf("check GCS CSV connectivity: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("GCS CSV health status = %q (message: %s)", health.Status, health.Message)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(context.Background(), source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list GCS CSV tables = %+v, error = %v", tables, err)
	}
	table, err := extractor.ExtractTableSchema(context.Background(), source.ID, "records")
	if err != nil {
		t.Fatalf("describe GCS CSV table: %v", err)
	}
	if len(table.Columns) != 3 || table.Columns[0].Name != "id" || table.Columns[1].Name != "name" || table.Columns[2].Name != "active" {
		t.Fatalf("GCS CSV columns = %+v", table.Columns)
	}

	db, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("open GCS CSV adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD configured GCS object: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT name AS person, id FROM records WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query GCS CSV object: %v", err)
	}
	columns, err := rows.Columns()
	if err != nil || strings.Join(columns, ",") != "person,id" {
		t.Fatalf("GCS CSV query columns = %v, error = %v", columns, err)
	}
	if !rows.Next() {
		t.Fatalf("expected one GCS CSV row, rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan GCS CSV row: %v", err)
	}
	if person != "Ada" || id != 1 {
		t.Fatalf("GCS CSV row = (%q, %d), want (Ada, 1)", person, id)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("GCS CSV query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("count GCS CSV rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("GCS CSV active row count = %d, want 2", count)
	}
	if _, err := db.ExecContext(context.Background(), "DELETE FROM records"); err == nil || !strings.Contains(err.Error(), "read-only SELECT") {
		t.Fatalf("write rejection error = %v, want read-only SELECT rejection", err)
	}
	if signedRequests.Load() < 5 {
		t.Fatalf("signed GCS requests = %d, want health, metadata, ping, and query requests", signedRequests.Load())
	}
}

func TestGCSCSVLimitsAndConfig(t *testing.T) {
	data := atomic.Value{}
	data.Store([]byte("id\n1\n2\n3\n"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-bucket/contract/records.csv" {
			http.Error(w, "unexpected object path", http.StatusNotFound)
			return
		}
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=test-hmac-id/") {
			http.Error(w, "missing signed request", http.StatusForbidden)
			return
		}
		object := data.Load().([]byte)
		w.Header().Set("Content-Length", strconv.Itoa(len(object)))
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			_, _ = w.Write(object)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	config := &model.DataSourceConfig{
		Host: "contract/records.csv", Database: "test-bucket", Username: "test-hmac-id", Password: "test-hmac-secret",
		AdditionalProps: map[string]interface{}{
			"object_key": "contract/records.csv", "table_name": "records", "endpoint_url": server.URL,
			"region": "us-central1", "max_file_bytes": 8,
		},
	}
	normalized, endpoint, err := object_storage.NormalizeGCSCSVConfig(config)
	if err != nil {
		t.Fatalf("normalize GCS CSV config: %v", err)
	}
	if endpoint != server.URL || normalized.AdditionalProps["force_path_style"] != true || normalized.AdditionalProps["region"] != "us-central1" {
		t.Fatalf("normalized endpoint/props = %q / %#v", endpoint, normalized.AdditionalProps)
	}
	if config.AdditionalProps["force_path_style"] != nil {
		t.Fatal("normalization mutated the original additionalProps")
	}

	driver := &object_storage.GCSCSVDriver{}
	if got := driver.BuildDSN(config); got != "gcs://test-bucket/contract/records.csv" {
		t.Fatalf("BuildDSN() = %q", got)
	}
	if err := driver.ValidateDSN(driver.BuildDSN(config)); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
	for _, invalid := range []string{"gcs://bucket/events.txt", "gcs://bucket/../events.csv", "gcs://bucket//events.csv"} {
		if err := driver.ValidateDSN(invalid); err == nil {
			t.Errorf("ValidateDSN(%q) unexpectedly succeeded", invalid)
		}
	}

	limitedDB, err := driver.OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open size-limited GCS CSV adapter: %v", err)
	}
	defer limitedDB.Close()
	if _, err := limitedDB.QueryContext(context.Background(), "SELECT * FROM records"); err == nil || !strings.Contains(err.Error(), "8 bytes") {
		t.Fatalf("oversized GCS CSV query error = %v, want 8-byte limit", err)
	}

	data.Store([]byte("id\n" + strings.Repeat("1\n", 100001)))
	large := *config
	large.AdditionalProps = map[string]interface{}{
		"object_key": "contract/records.csv", "table_name": "records", "endpoint_url": server.URL,
		"region": "us-central1", "max_file_bytes": 1 << 20,
	}
	rowLimitedDB, err := driver.OpenWithConfig(context.Background(), &large)
	if err != nil {
		t.Fatalf("open row-limited GCS CSV adapter: %v", err)
	}
	defer rowLimitedDB.Close()
	if _, err := rowLimitedDB.QueryContext(context.Background(), "SELECT COUNT(*) FROM records"); err == nil || !strings.Contains(err.Error(), "100000") {
		t.Fatalf("row limit error = %v, want 100,000-row limit", err)
	}

	invalid := *config
	invalid.AdditionalProps = map[string]interface{}{
		"object_key": "contract/records.csv", "endpoint_url": "https://storage.googleapis.com/path",
	}
	if err := object_storage.ValidateGCSCSVConfig(&invalid); err == nil || !strings.Contains(err.Error(), "without credentials, path") {
		t.Fatalf("invalid endpoint error = %v", err)
	}
}

func TestGCSCSVConfigValidation(t *testing.T) {
	base := func() *model.DataSourceConfig {
		return &model.DataSourceConfig{
			Host: "events.csv", Database: "analytics", Username: "hmac-id", Password: "hmac-secret",
			AdditionalProps: map[string]interface{}{"object_key": "events.csv"},
		}
	}

	normalized, endpoint, err := object_storage.NormalizeGCSCSVConfig(base())
	if err != nil {
		t.Fatalf("normalize default GCS CSV config: %v", err)
	}
	if endpoint != "https://storage.googleapis.com" ||
		normalized.AdditionalProps["region"] != "us-east-1" ||
		normalized.AdditionalProps["force_path_style"] != true {
		t.Fatalf("unexpected default GCS settings: endpoint=%q props=%#v", endpoint, normalized.AdditionalProps)
	}

	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{"missing bucket", func(c *model.DataSourceConfig) { c.Database = "" }, "bucket"},
		{"missing HMAC access ID", func(c *model.DataSourceConfig) { c.Username = "" }, "HMAC access ID"},
		{"missing HMAC secret", func(c *model.DataSourceConfig) { c.Password = "" }, "HMAC access ID"},
		{"unsafe key", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "../events.csv" }, "safe relative"},
		{"wrong extension", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "events.txt" }, "safe relative"},
		{"endpoint path", func(c *model.DataSourceConfig) {
			c.AdditionalProps["endpoint_url"] = "https://storage.googleapis.com/api"
		}, "without credentials, path"},
		{"invalid region", func(c *model.DataSourceConfig) { c.AdditionalProps["region"] = "us central1" }, "region"},
		{"invalid path style", func(c *model.DataSourceConfig) { c.AdditionalProps["force_path_style"] = "sometimes" }, "force_path_style"},
		{"multi-character delimiter", func(c *model.DataSourceConfig) { c.AdditionalProps["delimiter"] = "||" }, "exactly one character"},
		{"unsupported quote", func(c *model.DataSourceConfig) { c.AdditionalProps["quote_char"] = "'" }, "double-quote"},
		{"negative skipped rows", func(c *model.DataSourceConfig) { c.AdditionalProps["skip_header_rows"] = -1 }, "non-negative integer"},
		{"oversize limit", func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = 64<<20 + 1 }, "between 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base()
			test.mutate(config)
			err := object_storage.ValidateGCSCSVConfig(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateGCSCSVConfig() error = %v, want containing %q", err, test.want)
			}
		})
	}
}
