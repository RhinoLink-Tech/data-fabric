package object_storage_test

import (
	"context"
	"encoding/base64"
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

func TestAzureBlobORCProtocolFixture(t *testing.T) {
	payload := s3ORCContractFixture(t)
	var inaccurateHead atomic.Bool
	var getCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-container/contract/records.orc" {
			http.Error(w, "unexpected blob path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "" && r.URL.Query().Get("sig") != "fixture" {
			http.Error(w, "missing Azure authentication", http.StatusForbidden)
			return
		}
		w.Header().Set("ETag", "\"azure-orc-fixture\"")
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.Header().Set("Content-Type", "application/vnd.apache.orc")
		w.Header().Set("x-ms-blob-type", "BlockBlob")
		w.Header().Set("x-ms-request-id", "fixture-request")
		w.Header().Set("x-ms-version", "2023-11-03")
		switch r.Method {
		case http.MethodHead:
			contentLength := len(payload)
			if inaccurateHead.Load() {
				contentLength = 1
			}
			w.Header().Set("Content-Length", strconv.Itoa(contentLength))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			getCalls.Add(1)
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	accountKey := base64.StdEncoding.EncodeToString([]byte("fixture-account-key"))
	const objectKey = "contract/records.orc"
	source := &model.DataSource{
		ID: "azure-blob-orc-contract", Type: model.DatabaseTypeAzureBlobORC,
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
		t.Fatalf("validate Azure Blob ORC config: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("Azure Blob ORC health = %+v, error = %v", health, err)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(context.Background(), source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list Azure Blob ORC tables = %+v, error = %v", tables, err)
	}
	schema, err := extractor.ExtractTableSchema(context.Background(), source.ID, "records")
	if err != nil {
		t.Fatalf("describe Azure Blob ORC table: %v", err)
	}
	if len(schema.Columns) != 5 || schema.Columns[0].Name != "userid" || schema.Columns[0].Type != "BIGINT" ||
		schema.Columns[1].Name != "string1" || schema.Columns[1].Type != "VARCHAR" {
		t.Fatalf("Azure Blob ORC columns = %+v", schema.Columns)
	}

	db, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("open Azure Blob ORC adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD configured Azure ORC blob: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT userid, string1 FROM records WHERE userid = 2 ORDER BY string1 LIMIT 1")
	if err != nil {
		t.Fatalf("query Azure Blob ORC: %v", err)
	}
	if !rows.Next() {
		_ = rows.Close()
		t.Fatalf("expected filtered ORC row, rows error: %v", rows.Err())
	}
	var userID int64
	var value string
	if err := rows.Scan(&userID, &value); err != nil {
		_ = rows.Close()
		t.Fatalf("scan Azure ORC row: %v", err)
	}
	if userID != 2 || value != "foo" {
		_ = rows.Close()
		t.Fatalf("Azure ORC row = (%d, %q), want (2, foo)", userID, value)
	}
	if rows.Next() || rows.Err() != nil {
		_ = rows.Close()
		t.Fatalf("Azure ORC query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records").Scan(&count); err != nil || count != 25000 {
		t.Fatalf("Azure Blob ORC row count = %d, error = %v; want 25000", count, err)
	}
	beforeWrite := getCalls.Load()
	if _, err := db.ExecContext(context.Background(), "DELETE FROM records"); err == nil || !strings.Contains(err.Error(), "read-only SELECT") {
		t.Fatalf("write rejection error = %v, want read-only SELECT rejection", err)
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection downloaded the object: before=%d after=%d", beforeWrite, afterWrite)
	}

	inaccurateHead.Store(true)
	sizeConfig := &model.DataSourceConfig{
		Host: objectKey, Database: "test-container", Username: "testaccount", Password: accountKey,
		AdditionalProps: map[string]interface{}{
			"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL, "max_file_bytes": 64,
		},
	}
	sizeLimited, err := (&object_storage.AzureBlobORCDriver{}).OpenWithConfig(context.Background(), sizeConfig)
	if err != nil {
		t.Fatalf("open size-limited Azure Blob ORC adapter: %v", err)
	}
	defer sizeLimited.Close()
	if err := sizeLimited.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD with intentionally inaccurate Azure ORC size: %v", err)
	}
	if _, err := sizeLimited.QueryContext(context.Background(), "SELECT * FROM records"); err == nil || !strings.Contains(err.Error(), "64") {
		t.Fatalf("oversized Azure ORC query error = %v, want 64-byte limit", err)
	}

	inaccurateHead.Store(false)
	rowConfig := *sizeConfig
	rowConfig.AdditionalProps = map[string]interface{}{
		"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL, "max_rows": 1,
	}
	rowLimited, err := (&object_storage.AzureBlobORCDriver{}).OpenWithConfig(context.Background(), &rowConfig)
	if err != nil {
		t.Fatalf("open row-limited Azure Blob ORC adapter: %v", err)
	}
	defer rowLimited.Close()
	if _, err := rowLimited.QueryContext(context.Background(), "SELECT COUNT(*) FROM records"); err == nil || !strings.Contains(err.Error(), "maximum 1") {
		t.Fatalf("row-limit query error = %v, want one-row limit", err)
	}

	sasConfig := &model.DataSourceConfig{
		Host: objectKey, Database: "test-container", Username: "testaccount",
		AdditionalProps: map[string]interface{}{
			"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
			"sas_token": "?sv=2023-11-03&sig=fixture",
		},
	}
	sasDB, err := (&object_storage.AzureBlobORCDriver{}).OpenWithConfig(context.Background(), sasConfig)
	if err != nil {
		t.Fatalf("open SAS-authenticated Azure Blob ORC adapter: %v", err)
	}
	defer sasDB.Close()
	if err := sasDB.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD Azure ORC blob using SAS: %v", err)
	}
	if err := sasDB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records").Scan(&count); err != nil || count != 25000 {
		t.Fatalf("SAS-authenticated ORC row count = %d, error = %v; want 25000", count, err)
	}
}

func TestAzureBlobORCDriverConfigurationAndDSN(t *testing.T) {
	config := &model.DataSourceConfig{
		Host: "warehouse/events.orc", Database: "analytics", Username: "account", Password: "account-key",
		AdditionalProps: map[string]interface{}{"endpoint_url": "https://blob.example.test"},
	}
	normalized, err := object_storage.NormalizeAzureBlobORCConfig(config)
	if err != nil {
		t.Fatalf("normalize Azure Blob ORC config: %v", err)
	}
	if normalized.AdditionalProps["endpoint_url"] != config.AdditionalProps["endpoint_url"] {
		t.Fatalf("normalization changed endpoint: %#v", normalized.AdditionalProps)
	}
	driver := &object_storage.AzureBlobORCDriver{}
	dsn := driver.BuildDSN(config)
	if dsn != "azure-orc://analytics/warehouse/events.orc" {
		t.Fatalf("BuildDSN() = %q", dsn)
	}
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
	for _, invalid := range []string{
		"azure-orc://container/events.json", "azure-orc://container/../events.orc",
		"azure-orc://container//events.orc",
	} {
		if err := driver.ValidateDSN(invalid); err == nil {
			t.Errorf("ValidateDSN(%q) unexpectedly succeeded", invalid)
		}
	}
	sasConfig := *config
	sasConfig.Password = ""
	sasConfig.AdditionalProps = map[string]interface{}{"sas_token": "?sv=2023-11-03&sig=fixture"}
	if err := object_storage.ValidateAzureBlobORCConfig(&sasConfig); err != nil {
		t.Fatalf("validate SAS-authenticated config: %v", err)
	}
}

func TestAzureBlobORCConfigRejectsInvalidSettings(t *testing.T) {
	base := func() *model.DataSourceConfig {
		return &model.DataSourceConfig{
			Host: "events.orc", Database: "analytics", Username: "account", Password: "account-key",
			AdditionalProps: map[string]interface{}{},
		}
	}
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{"unsafe key", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "../events.orc" }, "safe relative"},
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
			err := object_storage.ValidateAzureBlobORCConfig(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateAzureBlobORCConfig() error = %v, want containing %q", err, test.want)
			}
		})
	}
}
