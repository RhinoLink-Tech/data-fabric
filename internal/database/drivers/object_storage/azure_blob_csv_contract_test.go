package object_storage_test

import (
	"context"
	"encoding/base64"
	"fmt"
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

func TestAzureBlobCSVProtocolFixture(t *testing.T) {
	data := []byte("generated report\nid;name;active;note\n1;Ada;true;NULL\n2;Lin;false;NULL\n3;Cara;true;NULL\n")
	var getCalls atomic.Int64
	var headLies atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-container/contract/records.csv" {
			http.Error(w, "unexpected blob path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "" && r.URL.Query().Get("sig") != "fixture" {
			http.Error(w, "missing Azure authentication", http.StatusForbidden)
			return
		}
		w.Header().Set("ETag", "\"azure-csv-fixture\"")
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("x-ms-blob-type", "BlockBlob")
		w.Header().Set("x-ms-request-id", "fixture-request")
		w.Header().Set("x-ms-version", "2023-11-03")
		switch r.Method {
		case http.MethodHead:
			contentLength := len(data)
			if headLies.Load() {
				contentLength = 1
			}
			w.Header().Set("Content-Length", strconv.Itoa(contentLength))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			getCalls.Add(1)
			start, end := int64(0), int64(len(data))-1
			if value := r.Header.Get("Range"); value != "" {
				if _, err := fmt.Sscanf(value, "bytes=%d-%d", &start, &end); err != nil || start < 0 || end < start || start >= int64(len(data)) {
					http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
					return
				}
				if end >= int64(len(data)) {
					end = int64(len(data)) - 1
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
				w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
				w.WriteHeader(http.StatusPartialContent)
			} else {
				w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			}
			_, _ = w.Write(data[start : end+1])
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	const objectKey = "contract/records.csv"
	accountKey := base64.StdEncoding.EncodeToString([]byte("fixture-account-key"))
	source := &model.DataSource{
		ID: "azure-blob-csv-protocol-fixture", Type: model.DatabaseTypeAzureBlobCSV,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: "test-container", Username: "testaccount", Password: accountKey,
			AdditionalProps: map[string]interface{}{
				"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
				"delimiter": ";", "skip_header_rows": 1, "null_values": []string{"NULL"},
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate Azure Blob CSV config: %v", err)
	}
	invalid := source.Config
	invalid.Password = ""
	if err := checker.ValidateDataSourceConfiguration(&invalid, source.Type); err == nil {
		t.Fatal("Azure Blob CSV validation accepted missing key and SAS credentials")
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
	if err != nil {
		t.Fatalf("check Azure Blob CSV connectivity: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("Azure Blob CSV health status = %q (message: %s)", health.Status, health.Message)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(context.Background(), source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list Azure Blob CSV tables = %+v, error = %v", tables, err)
	}
	table, err := extractor.ExtractTableSchema(context.Background(), source.ID, "records")
	if err != nil {
		t.Fatalf("describe Azure Blob CSV table: %v", err)
	}
	if len(table.Columns) != 4 || table.Columns[0].Name != "id" || table.Columns[1].Name != "name" || table.Columns[2].Name != "active" || table.Columns[3].Name != "note" {
		t.Fatalf("Azure Blob CSV columns = %+v", table.Columns)
	}

	db, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("open Azure Blob CSV adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD configured Azure blob: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT name AS person, id FROM records WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query Azure Blob CSV object: %v", err)
	}
	columns, err := rows.Columns()
	if err != nil || strings.Join(columns, ",") != "person,id" {
		t.Fatalf("Azure Blob CSV query columns = %v, error = %v", columns, err)
	}
	if !rows.Next() {
		t.Fatalf("expected one Azure Blob CSV row, rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan Azure Blob CSV row: %v", err)
	}
	if person != "Ada" || id != 1 {
		t.Fatalf("Azure Blob CSV row = (%q, %d), want (Ada, 1)", person, id)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("Azure Blob CSV query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("count Azure Blob CSV rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("Azure Blob CSV active row count = %d, want 2", count)
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
		t.Fatal("Azure Blob CSV adapter accepted a write")
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection downloaded the blob: before=%d after=%d", beforeWrite, afterWrite)
	}

	data = []byte(strings.Repeat("x", 65))
	headLies.Store(true)
	limitedConfig := source.Config
	limitedConfig.AdditionalProps = map[string]interface{}{
		"object_key": objectKey, "endpoint_url": server.URL, "max_file_bytes": 64,
	}
	limited, err := (&object_storage.AzureBlobCSVDriver{}).OpenWithConfig(context.Background(), &limitedConfig)
	if err != nil {
		t.Fatalf("open size-limited Azure Blob CSV adapter: %v", err)
	}
	defer limited.Close()
	if err := limited.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD with intentionally inaccurate Azure blob size: %v", err)
	}
	if _, err := limited.QueryContext(context.Background(), "SELECT * FROM records"); err == nil || !strings.Contains(err.Error(), "64 bytes") {
		t.Fatalf("oversized Azure Blob CSV query error = %v, want 64-byte limit", err)
	}

	data = []byte("id\n" + strings.Repeat("1\n", 100001))
	headLies.Store(false)
	rowLimitedConfig := source.Config
	rowLimitedConfig.AdditionalProps = map[string]interface{}{"object_key": objectKey, "endpoint_url": server.URL}
	rowLimited, err := (&object_storage.AzureBlobCSVDriver{}).OpenWithConfig(context.Background(), &rowLimitedConfig)
	if err != nil {
		t.Fatalf("open row-limited Azure Blob CSV adapter: %v", err)
	}
	defer rowLimited.Close()
	if _, err := rowLimited.QueryContext(context.Background(), "SELECT COUNT(*) FROM records"); err == nil || !strings.Contains(err.Error(), "100000") {
		t.Fatalf("row-limit error = %v, want 100,000-row limit", err)
	}

	data = []byte("id\n1\n")
	headLies.Store(false)
	sasClient, err := object_storage.NewAzureBlobClient(context.Background(), &object_storage.AzureBlobConfig{
		AccountName: "testaccount", ContainerName: "test-container",
		SASToken: "sv=2023-11-03&sig=fixture", Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("create Azure SAS protocol client: %v", err)
	}
	headLies.Store(false)
	if _, err := sasClient.GetBlobLimited(context.Background(), objectKey, 128); err != nil {
		t.Fatalf("download CSV blob using SAS query token: %v", err)
	}
}

func TestAzureBlobCSVDriverConfigurationAndDSN(t *testing.T) {
	config := &model.DataSourceConfig{
		Host: "warehouse/events.csv", Database: "analytics", Username: "account", Password: "account-key",
		AdditionalProps: map[string]interface{}{"endpoint_url": "https://blob.example.test"},
	}
	normalized, err := object_storage.NormalizeAzureBlobCSVConfig(config)
	if err != nil {
		t.Fatalf("normalize Azure Blob CSV config: %v", err)
	}
	if normalized.AdditionalProps["endpoint_url"] != config.AdditionalProps["endpoint_url"] {
		t.Fatalf("normalization changed the endpoint: %#v", normalized.AdditionalProps)
	}
	driver := &object_storage.AzureBlobCSVDriver{}
	dsn := driver.BuildDSN(config)
	if dsn != "azure-csv://analytics/warehouse/events.csv" {
		t.Fatalf("BuildDSN() = %q", dsn)
	}
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
	for _, invalid := range []string{
		"azure-csv://container/events.txt", "azure-csv://container/../events.csv",
		"azure-csv://container//events.csv",
	} {
		if err := driver.ValidateDSN(invalid); err == nil {
			t.Errorf("ValidateDSN(%q) unexpectedly succeeded", invalid)
		}
	}
	sasConfig := *config
	sasConfig.Password = ""
	sasConfig.AdditionalProps = map[string]interface{}{"sas_token": "?sv=2023-11-03&sig=fixture"}
	if err := object_storage.ValidateAzureBlobCSVConfig(&sasConfig); err != nil {
		t.Fatalf("validate SAS-authenticated config: %v", err)
	}
}

func TestAzureBlobCSVConfigRejectsInvalidSettings(t *testing.T) {
	base := func() *model.DataSourceConfig {
		return &model.DataSourceConfig{
			Host: "events.csv", Database: "analytics", Username: "account", Password: "account-key",
			AdditionalProps: map[string]interface{}{},
		}
	}
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{"unsafe key", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "../events.csv" }, "safe relative"},
		{"wrong extension", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "events.txt" }, "safe relative"},
		{"empty container", func(c *model.DataSourceConfig) { c.Database = "" }, "container"},
		{"missing account name", func(c *model.DataSourceConfig) { c.Username = "" }, "account name"},
		{"missing credentials", func(c *model.DataSourceConfig) { c.Password = "" }, "account key or sas_token"},
		{"invalid SAS token", func(c *model.DataSourceConfig) { c.Password = ""; c.AdditionalProps["sas_token"] = "%" }, "valid query string"},
		{"endpoint query", func(c *model.DataSourceConfig) {
			c.AdditionalProps["endpoint_url"] = "https://blob.example.test/?sig=x"
		}, "query"},
		{"multi-character delimiter", func(c *model.DataSourceConfig) { c.AdditionalProps["delimiter"] = "||" }, "exactly one character"},
		{"unsupported quote", func(c *model.DataSourceConfig) { c.AdditionalProps["quote_char"] = "'" }, "double-quote"},
		{"negative skipped rows", func(c *model.DataSourceConfig) { c.AdditionalProps["skip_header_rows"] = -1 }, "non-negative integer"},
		{"fractional skipped rows", func(c *model.DataSourceConfig) { c.AdditionalProps["skip_header_rows"] = 1.5 }, "non-negative integer"},
		{"invalid header flag", func(c *model.DataSourceConfig) { c.AdditionalProps["has_header"] = "sometimes" }, "has_header must be true or false"},
		{"oversize limit", func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = 64<<20 + 1 }, "between 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base()
			test.mutate(config)
			err := object_storage.ValidateAzureBlobCSVConfig(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateAzureBlobCSVConfig() error = %v, want containing %q", err, test.want)
			}
		})
	}
}
