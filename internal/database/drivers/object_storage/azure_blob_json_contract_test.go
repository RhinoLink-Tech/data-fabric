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

func TestAzureBlobJSONProtocolFixture(t *testing.T) {
	var data atomic.Value
	data.Store([]byte(`{"id":1,"name":"Ada","active":true,"optional":null}`))
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
		payload := data.Load().([]byte)
		w.Header().Set("ETag", `"azure-json-fixture"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-ms-blob-type", "BlockBlob")
		w.Header().Set("x-ms-request-id", "fixture-request")
		w.Header().Set("x-ms-version", "2023-11-03")
		switch r.Method {
		case http.MethodHead:
			contentLength := len(payload)
			if headLies.Load() {
				contentLength = 1
			}
			w.Header().Set("Content-Length", strconv.Itoa(contentLength))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			getCalls.Add(1)
			start, end := int64(0), int64(len(payload))-1
			if value := r.Header.Get("Range"); value != "" {
				if _, err := fmt.Sscanf(value, "bytes=%d-%d", &start, &end); err != nil || start < 0 || end < start || start >= int64(len(payload)) {
					http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
					return
				}
				if end >= int64(len(payload)) {
					end = int64(len(payload)) - 1
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
				w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
				w.WriteHeader(http.StatusPartialContent)
			} else {
				w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			}
			_, _ = w.Write(payload[start : end+1])
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	accountKey := base64.StdEncoding.EncodeToString([]byte("fixture-account-key"))
	fixtures := []struct {
		name string
		key  string
		data string
		rows int64
	}{
		{name: "object", key: "contract/one.json", data: `{"id":3,"name":"Grace","active":true,"optional":null}`, rows: 1},
		{name: "array", key: "contract/array.json", data: `[{"id":1,"name":"Ada","active":true,"optional":"x"},{"id":2,"name":"Linus","active":false},{"id":3,"name":"Grace","active":true,"optional":null}]`, rows: 3},
		{name: "json-lines", key: "contract/records.ndjson", data: "{\"id\":1,\"name\":\"Ada\",\"active\":true,\"optional\":\"x\"}\n{\"id\":2,\"name\":\"Linus\",\"active\":false}\n{\"id\":3,\"name\":\"Grace\",\"active\":true,\"optional\":null}\n", rows: 3},
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			data.Store([]byte(fixture.data))
			source := &model.DataSource{
				ID: "azure-blob-json-" + fixture.name, Type: model.DatabaseTypeAzureBlobJSON,
				Status: model.DataSourceStatusActive,
				Config: model.DataSourceConfig{
					Host: fixture.key, Database: "test-container", Username: "testaccount", Password: accountKey,
					AdditionalProps: map[string]interface{}{
						"object_key": fixture.key, "table_name": "records", "endpoint_url": server.URL,
					},
				},
			}
			pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
			t.Cleanup(func() { _ = pool.CloseAll() })
			checker := database.NewHealthChecker(pool)
			if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
				t.Fatalf("validate Azure Blob JSON config: %v", err)
			}
			health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
			if err != nil || health.Status != "healthy" {
				t.Fatalf("Azure Blob JSON health = %+v, error = %v", health, err)
			}
			extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
			tables, err := extractor.ListTables(context.Background(), source.ID)
			if err != nil || len(tables) != 1 || tables[0].Name != "records" {
				t.Fatalf("list Azure Blob JSON tables = %+v, error = %v", tables, err)
			}
			table, err := extractor.ExtractTableSchema(context.Background(), source.ID, "records")
			if err != nil {
				t.Fatalf("describe Azure Blob JSON table: %v", err)
			}
			columns := make(map[string]metadata.ColumnSchema, len(table.Columns))
			for _, column := range table.Columns {
				columns[column.Name] = column
			}
			if len(columns) != 4 || columns["id"].Type != "BIGINT" || columns["name"].Type != "VARCHAR" ||
				columns["active"].Type != "BOOLEAN" || !columns["optional"].Nullable {
				t.Fatalf("Azure Blob JSON columns = %+v", table.Columns)
			}

			db, err := pool.GetConnection(context.Background(), source)
			if err != nil {
				t.Fatalf("open Azure Blob JSON adapter: %v", err)
			}
			if err := db.PingContext(context.Background()); err != nil {
				t.Fatalf("HEAD configured Azure blob: %v", err)
			}
			var count int64
			if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
				t.Fatalf("count Azure Blob JSON rows: %v", err)
			}
			wantCount := int64(2)
			if fixture.rows == 1 {
				wantCount = 1
			}
			if count != wantCount {
				t.Fatalf("Azure Blob JSON active count = %d, want %d", count, wantCount)
			}
			rows, err := db.QueryContext(context.Background(), "SELECT name AS person, id FROM records ORDER BY id ASC LIMIT 1")
			if err != nil {
				t.Fatalf("query Azure Blob JSON object: %v", err)
			}
			if !rows.Next() {
				t.Fatalf("expected a JSON row, rows error: %v", rows.Err())
			}
			var person string
			var id int64
			if err := rows.Scan(&person, &id); err != nil {
				t.Fatalf("scan Azure Blob JSON row: %v", err)
			}
			if person == "" || id <= 0 {
				t.Fatalf("Azure Blob JSON first row = (%q, %d)", person, id)
			}
			_ = rows.Close()
			beforeWrite := getCalls.Load()
			if _, err := db.ExecContext(context.Background(), "DELETE FROM records"); err == nil {
				t.Fatal("Azure Blob JSON adapter accepted a write")
			}
			if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
				t.Fatalf("write rejection fetched the blob: before=%d after=%d", beforeWrite, afterWrite)
			}
		})
	}

	const objectKey = "contract/oversized.json"
	data.Store([]byte(strings.Repeat("x", 65)))
	headLies.Store(true)
	limitedConfig := &model.DataSourceConfig{
		Host: objectKey, Database: "test-container", Username: "testaccount", Password: accountKey,
		AdditionalProps: map[string]interface{}{"object_key": objectKey, "endpoint_url": server.URL, "max_file_bytes": 64},
	}
	limited, err := (&object_storage.AzureBlobJSONDriver{}).OpenWithConfig(context.Background(), limitedConfig)
	if err != nil {
		t.Fatalf("open size-limited Azure Blob JSON adapter: %v", err)
	}
	defer limited.Close()
	if err := limited.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD with intentionally inaccurate Azure blob size: %v", err)
	}
	if _, err := limited.QueryContext(context.Background(), "SELECT * FROM oversized"); err == nil || !strings.Contains(err.Error(), "64 bytes") {
		t.Fatalf("oversized Azure Blob JSON query error = %v, want 64-byte limit", err)
	}

	headLies.Store(false)
	data.Store([]byte("{\"id\":1}\n" + strings.Repeat("{\"id\":1}\n", 100000)))
	recordConfig := &model.DataSourceConfig{
		Host: "contract/rows.ndjson", Database: "test-container", Username: "testaccount", Password: accountKey,
		AdditionalProps: map[string]interface{}{"object_key": "contract/rows.ndjson", "table_name": "records", "endpoint_url": server.URL},
	}
	recordLimited, err := (&object_storage.AzureBlobJSONDriver{}).OpenWithConfig(context.Background(), recordConfig)
	if err != nil {
		t.Fatalf("open record-limited Azure Blob JSON adapter: %v", err)
	}
	defer recordLimited.Close()
	if _, err := recordLimited.QueryContext(context.Background(), "SELECT COUNT(*) FROM records"); err == nil || !strings.Contains(err.Error(), "100000") {
		t.Fatalf("record-limit error = %v, want 100,000-record limit", err)
	}

	data.Store([]byte(`{"id":1}`))
	sasClient, err := object_storage.NewAzureBlobClient(context.Background(), &object_storage.AzureBlobConfig{
		AccountName: "testaccount", ContainerName: "test-container",
		SASToken: "sv=2023-11-03&sig=fixture", Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("create Azure SAS protocol client: %v", err)
	}
	if _, err := sasClient.GetBlobLimited(context.Background(), "contract/one.json", 128); err != nil {
		t.Fatalf("download JSON blob using SAS query token: %v", err)
	}
}

func TestAzureBlobJSONDriverConfigurationAndDSN(t *testing.T) {
	config := &model.DataSourceConfig{
		Host: "warehouse/events.json", Database: "analytics", Username: "account", Password: "account-key",
		AdditionalProps: map[string]interface{}{"endpoint_url": "https://blob.example.test"},
	}
	normalized, err := object_storage.NormalizeAzureBlobJSONConfig(config)
	if err != nil {
		t.Fatalf("normalize Azure Blob JSON config: %v", err)
	}
	if normalized.AdditionalProps["endpoint_url"] != config.AdditionalProps["endpoint_url"] {
		t.Fatalf("normalization changed the endpoint: %#v", normalized.AdditionalProps)
	}
	driver := &object_storage.AzureBlobJSONDriver{}
	dsn := driver.BuildDSN(config)
	if dsn != "azure-json://analytics/warehouse/events.json" {
		t.Fatalf("BuildDSN() = %q", dsn)
	}
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
	for _, invalid := range []string{
		"azure-json://container/events.txt", "azure-json://container/../events.json",
		"azure-json://container//events.json",
	} {
		if err := driver.ValidateDSN(invalid); err == nil {
			t.Errorf("ValidateDSN(%q) unexpectedly succeeded", invalid)
		}
	}
	sasConfig := *config
	sasConfig.Password = ""
	sasConfig.AdditionalProps = map[string]interface{}{"sas_token": "?sv=2023-11-03&sig=fixture"}
	if err := object_storage.ValidateAzureBlobJSONConfig(&sasConfig); err != nil {
		t.Fatalf("validate SAS-authenticated config: %v", err)
	}
}

func TestAzureBlobJSONConfigRejectsInvalidSettings(t *testing.T) {
	base := func() *model.DataSourceConfig {
		return &model.DataSourceConfig{
			Host: "events.json", Database: "analytics", Username: "account", Password: "account-key",
			AdditionalProps: map[string]interface{}{},
		}
	}
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{"unsafe key", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "../events.json" }, "safe relative"},
		{"unsupported extension", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "events.txt" }, "safe relative"},
		{"empty container", func(c *model.DataSourceConfig) { c.Database = "" }, "container"},
		{"missing account name", func(c *model.DataSourceConfig) { c.Username = "" }, "account name"},
		{"missing credentials", func(c *model.DataSourceConfig) { c.Password = "" }, "account key or sas_token"},
		{"invalid SAS token", func(c *model.DataSourceConfig) { c.Password = ""; c.AdditionalProps["sas_token"] = "%" }, "valid query string"},
		{"endpoint query", func(c *model.DataSourceConfig) {
			c.AdditionalProps["endpoint_url"] = "https://blob.example.test/?sig=x"
		}, "query"},
		{"oversize limit", func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = 64<<20 + 1 }, "between 1"},
		{"invalid record limit", func(c *model.DataSourceConfig) { c.AdditionalProps["max_records"] = 100001 }, "between 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base()
			test.mutate(config)
			err := object_storage.ValidateAzureBlobJSONConfig(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateAzureBlobJSONConfig() error = %v, want containing %q", err, test.want)
			}
		})
	}
}
