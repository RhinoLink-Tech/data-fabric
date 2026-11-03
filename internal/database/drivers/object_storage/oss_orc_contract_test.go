package object_storage_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestOSSORCProtocolFixture(t *testing.T) {
	payload := s3ORCContractFixture(t)
	var inaccurateHead atomic.Bool
	var getCalls atomic.Int64
	var holdGET atomic.Bool
	blockedGETs := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-bucket/contract/records.orc" {
			http.Error(w, "unexpected blob path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "" || !strings.Contains(r.Header.Get("Authorization"), "test-access-key") {
			http.Error(w, "missing OSS authorization", http.StatusForbidden)
			return
		}
		if r.Header.Get("x-oss-security-token") != "test-session-token" {
			http.Error(w, "missing OSS STS token", http.StatusForbidden)
			return
		}
		w.Header().Set("ETag", "\"oss-orc-fixture\"")
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.Header().Set("Content-Type", "application/vnd.apache.orc")
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
			if holdGET.Load() {
				select {
				case blockedGETs <- struct{}{}:
				default:
				}
				<-r.Context().Done()
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	const accountKey = "test-access-secret"
	const objectKey = "contract/records.orc"
	source := &model.DataSource{
		ID: "oss-orc-contract", Type: model.DatabaseTypeAlibabaOSSORC,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: "test-bucket", Username: "test-access-key", Password: accountKey,
			AdditionalProps: map[string]interface{}{
				"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
				"force_path_style": true, "session_token": "test-session-token",
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate OSS ORC config: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("OSS ORC health = %+v, error = %v", health, err)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(context.Background(), source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list OSS ORC tables = %+v, error = %v", tables, err)
	}
	schema, err := extractor.ExtractTableSchema(context.Background(), source.ID, "records")
	if err != nil {
		t.Fatalf("describe OSS ORC table: %v", err)
	}
	if len(schema.Columns) != 5 || schema.Columns[0].Name != "userid" || schema.Columns[0].Type != "BIGINT" ||
		schema.Columns[1].Name != "string1" || schema.Columns[1].Type != "VARCHAR" {
		t.Fatalf("OSS ORC columns = %+v", schema.Columns)
	}

	db, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("open OSS ORC adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD configured OSS ORC blob: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT userid, string1 FROM records WHERE userid = 2 ORDER BY string1 LIMIT 1")
	if err != nil {
		t.Fatalf("query OSS ORC: %v", err)
	}
	if !rows.Next() {
		_ = rows.Close()
		t.Fatalf("expected filtered ORC row, rows error: %v", rows.Err())
	}
	var userID int64
	var value string
	if err := rows.Scan(&userID, &value); err != nil {
		_ = rows.Close()
		t.Fatalf("scan OSS ORC row: %v", err)
	}
	if userID != 2 || value != "foo" {
		_ = rows.Close()
		t.Fatalf("OSS ORC row = (%d, %q), want (2, foo)", userID, value)
	}
	if rows.Next() || rows.Err() != nil {
		_ = rows.Close()
		t.Fatalf("OSS ORC query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records").Scan(&count); err != nil || count != 25000 {
		t.Fatalf("OSS ORC row count = %d, error = %v; want 25000", count, err)
	}
	beforeWrite := getCalls.Load()
	if _, err := db.ExecContext(context.Background(), "DELETE FROM records"); err == nil || !strings.Contains(err.Error(), "read-only SELECT") {
		t.Fatalf("write rejection error = %v, want read-only SELECT rejection", err)
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection downloaded the object: before=%d after=%d", beforeWrite, afterWrite)
	}

	holdGET.Store(true)
	queryCtx, cancelQuery := context.WithCancel(context.Background())
	queryResult := make(chan error, 1)
	go func() {
		queryRows, queryErr := db.QueryContext(queryCtx, "SELECT * FROM records")
		if queryRows != nil {
			_ = queryRows.Close()
		}
		queryResult <- queryErr
	}()
	select {
	case <-blockedGETs:
	case <-time.After(5 * time.Second):
		cancelQuery()
		t.Fatal("OSS ORC fixture did not receive the cancellable GET")
	}
	cancelQuery()
	select {
	case err := <-queryResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled OSS ORC query error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled OSS ORC query did not return")
	}
	holdGET.Store(false)

	inaccurateHead.Store(true)
	sizeConfig := &model.DataSourceConfig{
		Host: objectKey, Database: "test-bucket", Username: "test-access-key", Password: accountKey,
		AdditionalProps: map[string]interface{}{
			"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
			"force_path_style": true, "session_token": "test-session-token", "max_file_bytes": 64,
		},
	}
	sizeLimited, err := (&object_storage.OSSORCDriver{}).OpenWithConfig(context.Background(), sizeConfig)
	if err != nil {
		t.Fatalf("open size-limited OSS ORC adapter: %v", err)
	}
	defer sizeLimited.Close()
	if err := sizeLimited.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD with intentionally inaccurate OSS ORC size: %v", err)
	}
	if _, err := sizeLimited.QueryContext(context.Background(), "SELECT * FROM records"); err == nil || !strings.Contains(err.Error(), "64") {
		t.Fatalf("oversized OSS ORC query error = %v, want 64-byte limit", err)
	}

	inaccurateHead.Store(false)
	rowConfig := *sizeConfig
	rowConfig.AdditionalProps = map[string]interface{}{
		"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
		"force_path_style": true, "session_token": "test-session-token", "max_rows": 1,
	}
	rowLimited, err := (&object_storage.OSSORCDriver{}).OpenWithConfig(context.Background(), &rowConfig)
	if err != nil {
		t.Fatalf("open row-limited OSS ORC adapter: %v", err)
	}
	defer rowLimited.Close()
	if _, err := rowLimited.QueryContext(context.Background(), "SELECT COUNT(*) FROM records"); err == nil || !strings.Contains(err.Error(), "maximum 1") {
		t.Fatalf("row-limit query error = %v, want one-row limit", err)
	}

}

func TestOSSORCDriverConfigurationAndDSN(t *testing.T) {
	config := &model.DataSourceConfig{
		Host: "warehouse/events.orc", Database: "analytics", Username: "access-key", Password: "access-secret",
		AdditionalProps: map[string]interface{}{"endpoint_url": "https://oss.example.test"},
	}
	normalized, err := object_storage.NormalizeOSSORCConfig(config)
	if err != nil {
		t.Fatalf("normalize OSS ORC config: %v", err)
	}
	if normalized.AdditionalProps["endpoint_url"] != config.AdditionalProps["endpoint_url"] {
		t.Fatalf("normalization changed endpoint: %#v", normalized.AdditionalProps)
	}
	driver := &object_storage.OSSORCDriver{}
	dsn := driver.BuildDSN(config)
	if dsn != "oss-orc://analytics/warehouse/events.orc" {
		t.Fatalf("BuildDSN() = %q", dsn)
	}
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
	for _, invalid := range []string{
		"oss-orc://bucket/events.json", "oss-orc://bucket/../events.orc",
		"oss-orc://bucket//events.orc",
	} {
		if err := driver.ValidateDSN(invalid); err == nil {
			t.Errorf("ValidateDSN(%q) unexpectedly succeeded", invalid)
		}
	}
	if err := object_storage.ValidateOSSORCConfig(config); err != nil {
		t.Fatalf("validate access-key config: %v", err)
	}

}

func TestOSSORCConfigRejectsInvalidSettings(t *testing.T) {
	base := func() *model.DataSourceConfig {
		return &model.DataSourceConfig{
			Host: "events.orc", Database: "analytics", Username: "access-key", Password: "access-secret",
			AdditionalProps: map[string]interface{}{"endpoint_url": "https://oss.example.test"},
		}
	}
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{"unsafe key", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "../events.orc" }, "safe relative"},
		{"unsupported extension", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "events.json" }, "safe relative"},
		{"empty bucket", func(c *model.DataSourceConfig) { c.Database = "" }, "bucket"},
		{"missing access key", func(c *model.DataSourceConfig) { c.Username = "" }, "access key ID and secret"},
		{"missing credentials", func(c *model.DataSourceConfig) { c.Password = "" }, "access key ID and secret"},
		{"endpoint query", func(c *model.DataSourceConfig) {
			c.AdditionalProps["endpoint_url"] = "https://oss.example.test/?sig=x"
		}, "query"},
		{"oversize limit", func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = 64<<20 + 1 }, "between 1"},
		{"invalid row limit", func(c *model.DataSourceConfig) { c.AdditionalProps["max_rows"] = 100001 }, "between 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base()
			test.mutate(config)
			err := object_storage.ValidateOSSORCConfig(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateOSSORCConfig() error = %v, want containing %q", err, test.want)
			}
		})
	}
}
