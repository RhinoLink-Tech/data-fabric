package object_storage_test

import (
	"bytes"
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
	"github.com/linkedin/goavro/v2"
)

const azureBlobAvroContractSchema = "{\"type\":\"record\",\"name\":\"Record\",\"fields\":[{\"name\":\"id\",\"type\":\"long\"},{\"name\":\"name\",\"type\":[\"null\",\"string\"],\"default\":null},{\"name\":\"active\",\"type\":\"boolean\"}]}"

func azureBlobAvroContractFixture(t *testing.T) []byte {
	t.Helper()
	codec, err := goavro.NewCodec(azureBlobAvroContractSchema)
	if err != nil {
		t.Fatalf("create Avro contract schema: %v", err)
	}
	var output bytes.Buffer
	writer, err := goavro.NewOCFWriter(goavro.OCFConfig{
		W: &output, Codec: codec, CompressionName: "null",
		SyncMarker: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
	})
	if err != nil {
		t.Fatalf("create Avro OCF fixture writer: %v", err)
	}
	if err := writer.Append([]interface{}{
		map[string]interface{}{"id": int64(1), "name": goavro.Union("string", "Ada"), "active": true},
		map[string]interface{}{"id": int64(2), "name": nil, "active": false},
		map[string]interface{}{"id": int64(3), "name": goavro.Union("string", "Grace"), "active": true},
	}); err != nil {
		t.Fatalf("write Avro OCF fixture: %v", err)
	}
	return append([]byte(nil), output.Bytes()...)
}

func TestAzureBlobAvroProtocolFixture(t *testing.T) {
	payload := azureBlobAvroContractFixture(t)
	var data atomic.Value
	data.Store(payload)
	var headLies atomic.Bool
	var getCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/test-container/contract/") {
			http.Error(w, "unexpected blob path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "" && r.URL.Query().Get("sig") != "fixture" {
			http.Error(w, "missing Azure authentication", http.StatusForbidden)
			return
		}
		object := data.Load().([]byte)
		w.Header().Set("ETag", "\"azure-avro-fixture\"")
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.Header().Set("Content-Type", "avro/avro")
		w.Header().Set("x-ms-blob-type", "BlockBlob")
		w.Header().Set("x-ms-request-id", "fixture-request")
		w.Header().Set("x-ms-version", "2023-11-03")
		switch r.Method {
		case http.MethodHead:
			contentLength := len(object)
			if headLies.Load() {
				contentLength = 1
			}
			w.Header().Set("Content-Length", strconv.Itoa(contentLength))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			getCalls.Add(1)
			start, end := int64(0), int64(len(object))-1
			if value := r.Header.Get("Range"); value != "" {
				if _, err := fmt.Sscanf(value, "bytes=%d-%d", &start, &end); err != nil || start < 0 || end < start || start >= int64(len(object)) {
					http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
					return
				}
				if end >= int64(len(object)) {
					end = int64(len(object)) - 1
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(object)))
				w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
				w.WriteHeader(http.StatusPartialContent)
			} else {
				w.Header().Set("Content-Length", strconv.Itoa(len(object)))
			}
			_, _ = w.Write(object[start : end+1])
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	accountKey := base64.StdEncoding.EncodeToString([]byte("fixture-account-key"))
	const objectKey = "contract/records.avro"
	source := &model.DataSource{
		ID: "azure-blob-avro-contract", Type: model.DatabaseTypeAzureBlobAvro,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: "test-container", Username: "testaccount", Password: accountKey,
			AdditionalProps: map[string]interface{}{
				"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate Azure Blob Avro config: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("Azure Blob Avro health = %+v, error = %v", health, err)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(context.Background(), source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list Azure Blob Avro tables = %+v, error = %v", tables, err)
	}
	table, err := extractor.ExtractTableSchema(context.Background(), source.ID, "records")
	if err != nil {
		t.Fatalf("describe Azure Blob Avro table: %v", err)
	}
	if len(table.Columns) != 3 || table.Columns[0].Name != "id" || table.Columns[0].Type != "BIGINT" ||
		table.Columns[1].Name != "name" || table.Columns[1].Type != "VARCHAR" || !table.Columns[1].Nullable ||
		table.Columns[2].Name != "active" || table.Columns[2].Type != "BOOLEAN" {
		t.Fatalf("Azure Blob Avro columns = %+v", table.Columns)
	}

	db, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("open Azure Blob Avro adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD configured Azure Avro blob: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT id, name FROM records WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query Azure Blob Avro: %v", err)
	}
	if !rows.Next() {
		_ = rows.Close()
		t.Fatalf("expected the second filtered Avro row, rows error: %v", rows.Err())
	}
	var id int64
	var name string
	if err := rows.Scan(&id, &name); err != nil {
		_ = rows.Close()
		t.Fatalf("scan queried Avro row: %v", err)
	}
	if id != 1 || name != "Ada" {
		_ = rows.Close()
		t.Fatalf("Avro row = (%d, %q), want (1, Ada)", id, name)
	}
	if rows.Next() || rows.Err() != nil {
		_ = rows.Close()
		t.Fatalf("unexpected extra row or iteration error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil || count != 2 {
		t.Fatalf("Azure Blob Avro active count = %d, error = %v; want 2", count, err)
	}
	beforeWrite := getCalls.Load()
	if _, err := db.ExecContext(context.Background(), "DELETE FROM records"); err == nil || !strings.Contains(err.Error(), "read-only SELECT") {
		t.Fatalf("write rejection error = %v, want read-only SELECT rejection", err)
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection downloaded the object: before=%d after=%d", beforeWrite, afterWrite)
	}

	headLies.Store(true)
	limitedConfig := &model.DataSourceConfig{
		Host: objectKey, Database: "test-container", Username: "testaccount", Password: accountKey,
		AdditionalProps: map[string]interface{}{
			"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
			"max_file_bytes": int64(len(payload) - 1),
		},
	}
	limited, err := (&object_storage.AzureBlobAvroDriver{}).OpenWithConfig(context.Background(), limitedConfig)
	if err != nil {
		t.Fatalf("open size-limited Azure Blob Avro adapter: %v", err)
	}
	defer limited.Close()
	if err := limited.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD with intentionally inaccurate Azure Avro size: %v", err)
	}
	if _, err := limited.QueryContext(context.Background(), "SELECT * FROM records"); err == nil || !strings.Contains(err.Error(), "configured maximum") {
		t.Fatalf("oversized Azure Blob Avro query error = %v, want configured-size limit", err)
	}

	headLies.Store(false)
	rowLimitedConfig := *limitedConfig
	rowLimitedConfig.AdditionalProps = map[string]interface{}{
		"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL, "max_rows": 2,
	}
	rowLimited, err := (&object_storage.AzureBlobAvroDriver{}).OpenWithConfig(context.Background(), &rowLimitedConfig)
	if err != nil {
		t.Fatalf("open row-limited Azure Blob Avro adapter: %v", err)
	}
	defer rowLimited.Close()
	if _, err := rowLimited.QueryContext(context.Background(), "SELECT COUNT(*) FROM records"); err == nil || !strings.Contains(err.Error(), "row limit of 2") {
		t.Fatalf("row-limit query error = %v, want two-row limit", err)
	}

	sasConfig := &model.DataSourceConfig{
		Host: objectKey, Database: "test-container", Username: "testaccount",
		AdditionalProps: map[string]interface{}{
			"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
			"sas_token": "?sv=2023-11-03&sig=fixture",
		},
	}
	sasDB, err := (&object_storage.AzureBlobAvroDriver{}).OpenWithConfig(context.Background(), sasConfig)
	if err != nil {
		t.Fatalf("open SAS-authenticated Azure Blob Avro adapter: %v", err)
	}
	defer sasDB.Close()
	if err := sasDB.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD Azure Avro blob using SAS: %v", err)
	}
	if err := sasDB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records").Scan(&count); err != nil || count != 3 {
		t.Fatalf("SAS-authenticated Avro row count = %d, error = %v; want 3", count, err)
	}
}

func TestAzureBlobAvroDriverConfigurationAndDSN(t *testing.T) {
	config := &model.DataSourceConfig{
		Host: "warehouse/events.avro", Database: "analytics", Username: "account", Password: "account-key",
		AdditionalProps: map[string]interface{}{"endpoint_url": "https://blob.example.test"},
	}
	normalized, err := object_storage.NormalizeAzureBlobAvroConfig(config)
	if err != nil {
		t.Fatalf("normalize Azure Blob Avro config: %v", err)
	}
	if normalized.AdditionalProps["endpoint_url"] != config.AdditionalProps["endpoint_url"] {
		t.Fatalf("normalization changed the endpoint: %#v", normalized.AdditionalProps)
	}
	driver := &object_storage.AzureBlobAvroDriver{}
	dsn := driver.BuildDSN(config)
	if dsn != "azure-avro://analytics/warehouse/events.avro" {
		t.Fatalf("BuildDSN() = %q", dsn)
	}
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
	for _, invalid := range []string{
		"azure-avro://container/events.json", "azure-avro://container/../events.avro",
		"azure-avro://container//events.avro",
	} {
		if err := driver.ValidateDSN(invalid); err == nil {
			t.Errorf("ValidateDSN(%q) unexpectedly succeeded", invalid)
		}
	}
	sasConfig := *config
	sasConfig.Password = ""
	sasConfig.AdditionalProps = map[string]interface{}{"sas_token": "?sv=2023-11-03&sig=fixture"}
	if err := object_storage.ValidateAzureBlobAvroConfig(&sasConfig); err != nil {
		t.Fatalf("validate SAS-authenticated config: %v", err)
	}
}

func TestAzureBlobAvroConfigRejectsInvalidSettings(t *testing.T) {
	base := func() *model.DataSourceConfig {
		return &model.DataSourceConfig{
			Host: "events.avro", Database: "analytics", Username: "account", Password: "account-key",
			AdditionalProps: map[string]interface{}{},
		}
	}
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{"unsafe key", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "../events.avro" }, "safe relative"},
		{"unsupported extension", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "events.json" }, "safe relative"},
		{"empty container", func(c *model.DataSourceConfig) { c.Database = "" }, "container"},
		{"missing account name", func(c *model.DataSourceConfig) { c.Username = "" }, "account name"},
		{"missing credentials", func(c *model.DataSourceConfig) { c.Password = "" }, "account key or sas_token"},
		{"invalid SAS token", func(c *model.DataSourceConfig) { c.Password = ""; c.AdditionalProps["sas_token"] = "%" }, "valid query string"},
		{"endpoint query", func(c *model.DataSourceConfig) {
			c.AdditionalProps["endpoint_url"] = "https://blob.example.test/?sig=x"
		}, "query"},
		{"oversize limit", func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = 64<<20 + 1 }, "between 1"},
		{"invalid row limit", func(c *model.DataSourceConfig) { c.AdditionalProps["max_rows"] = 100001 }, "between 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base()
			test.mutate(config)
			err := object_storage.ValidateAzureBlobAvroConfig(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateAzureBlobAvroConfig() error = %v, want containing %q", err, test.want)
			}
		})
	}
}
