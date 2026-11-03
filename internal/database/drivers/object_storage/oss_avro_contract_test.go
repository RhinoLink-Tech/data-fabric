package object_storage_test

import (
	"bytes"
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
	"github.com/linkedin/goavro/v2"
)

const ossAvroContractSchema = "{\"type\":\"record\",\"name\":\"Record\",\"fields\":[{\"name\":\"id\",\"type\":\"long\"},{\"name\":\"name\",\"type\":[\"null\",\"string\"],\"default\":null},{\"name\":\"active\",\"type\":\"boolean\"}]}"

func ossAvroContractFixture(t *testing.T) []byte {
	t.Helper()
	codec, err := goavro.NewCodec(ossAvroContractSchema)
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

func TestOSSAvroProtocolFixture(t *testing.T) {
	var data atomic.Value
	data.Store(ossAvroContractFixture(t))
	var headLies atomic.Bool
	var holdGET atomic.Bool
	var getCalls atomic.Int64
	blockedGETs := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-bucket/contract/records.avro" {
			http.Error(w, "unexpected OSS object path", http.StatusNotFound)
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
		object := data.Load().([]byte)
		w.Header().Set("ETag", "\"oss-avro-fixture\"")
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.Header().Set("Content-Type", "application/avro")
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
			if holdGET.Load() {
				select {
				case blockedGETs <- struct{}{}:
				default:
				}
				<-r.Context().Done()
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(object)))
			_, _ = w.Write(object)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	const objectKey = "contract/records.avro"
	source := &model.DataSource{
		ID: "oss-avro-protocol-fixture", Type: model.DatabaseTypeAlibabaOSSAvro,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: "test-bucket", Username: "test-access-key", Password: "test-access-secret",
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
		t.Fatalf("validate OSS Avro config: %v", err)
	}
	invalid := source.Config
	invalid.AdditionalProps = map[string]interface{}{"object_key": objectKey}
	if err := checker.ValidateDataSourceConfiguration(&invalid, source.Type); err == nil {
		t.Fatal("OSS Avro config validation accepted a missing endpoint")
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("OSS Avro health = %+v, error = %v", health, err)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(context.Background(), source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list OSS Avro tables = %+v, error = %v", tables, err)
	}
	table, err := extractor.ExtractTableSchema(context.Background(), source.ID, "records")
	if err != nil {
		t.Fatalf("describe OSS Avro table: %v", err)
	}
	if len(table.Columns) != 3 || table.Columns[0].Name != "id" || table.Columns[0].Type != "BIGINT" ||
		table.Columns[1].Name != "name" || table.Columns[1].Type != "VARCHAR" || !table.Columns[1].Nullable ||
		table.Columns[2].Name != "active" || table.Columns[2].Type != "BOOLEAN" {
		t.Fatalf("OSS Avro columns = %+v", table.Columns)
	}

	db, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("open OSS Avro adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD configured OSS Avro object: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT id, name FROM records WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query OSS Avro object: %v", err)
	}
	if !rows.Next() {
		_ = rows.Close()
		t.Fatalf("expected the second filtered Avro row, rows error: %v", rows.Err())
	}
	var id int64
	var name string
	if err := rows.Scan(&id, &name); err != nil {
		_ = rows.Close()
		t.Fatalf("scan OSS Avro row: %v", err)
	}
	if id != 1 || name != "Ada" {
		_ = rows.Close()
		t.Fatalf("OSS Avro row = (%d, %q), want (1, Ada)", id, name)
	}
	if rows.Next() || rows.Err() != nil {
		_ = rows.Close()
		t.Fatalf("unexpected extra row or iteration error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil || count != 2 {
		t.Fatalf("OSS Avro active count = %d, error = %v; want 2", count, err)
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
		t.Fatal("OSS Avro fixture did not receive the cancellable GET")
	}
	cancelQuery()
	select {
	case err := <-queryResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled OSS Avro query error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled OSS Avro query did not return")
	}
	holdGET.Store(false)

	payload := data.Load().([]byte)
	headLies.Store(true)
	limitedConfig := source.Config
	limitedConfig.AdditionalProps = map[string]interface{}{
		"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
		"force_path_style": true, "session_token": "test-session-token", "max_file_bytes": int64(len(payload) - 1),
	}
	limited, err := (&object_storage.OSSAvroDriver{}).OpenWithConfig(context.Background(), &limitedConfig)
	if err != nil {
		t.Fatalf("open size-limited OSS Avro adapter: %v", err)
	}
	defer limited.Close()
	if err := limited.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD with intentionally inaccurate OSS Avro size: %v", err)
	}
	if _, err := limited.QueryContext(context.Background(), "SELECT * FROM records"); err == nil || !strings.Contains(err.Error(), "configured maximum") {
		t.Fatalf("oversized OSS Avro query error = %v, want configured-size limit", err)
	}

	headLies.Store(false)
	rowLimitedConfig := source.Config
	rowLimitedConfig.AdditionalProps = map[string]interface{}{
		"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
		"force_path_style": true, "session_token": "test-session-token", "max_rows": 2,
	}
	rowLimited, err := (&object_storage.OSSAvroDriver{}).OpenWithConfig(context.Background(), &rowLimitedConfig)
	if err != nil {
		t.Fatalf("open row-limited OSS Avro adapter: %v", err)
	}
	defer rowLimited.Close()
	if _, err := rowLimited.QueryContext(context.Background(), "SELECT COUNT(*) FROM records"); err == nil || !strings.Contains(err.Error(), "row limit of 2") {
		t.Fatalf("row-limit query error = %v, want two-row limit", err)
	}
}

func TestOSSAvroDriverConfigurationAndDSN(t *testing.T) {
	config := &model.DataSourceConfig{
		Host: "warehouse/events.avro", Database: "analytics", Username: "access-key", Password: "access-secret",
		AdditionalProps: map[string]interface{}{"endpoint_url": "oss-cn-hangzhou.aliyuncs.com", "session_token": "sts-token"},
	}
	normalized, err := object_storage.NormalizeOSSAvroConfig(config)
	if err != nil {
		t.Fatalf("normalize OSS Avro config: %v", err)
	}
	if normalized.AdditionalProps["endpoint_url"] != "https://oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("normalized OSS endpoint = %#v", normalized.AdditionalProps["endpoint_url"])
	}
	if normalized.AdditionalProps["session_token"] != "sts-token" {
		t.Fatalf("normalization lost the STS token: %#v", normalized.AdditionalProps)
	}
	aliasConfig := *config
	aliasConfig.AdditionalProps = map[string]interface{}{"endpoint": "oss-cn-hangzhou.aliyuncs.com"}
	if _, err := object_storage.NormalizeOSSAvroConfig(&aliasConfig); err != nil {
		t.Fatalf("normalize endpoint alias: %v", err)
	}
	driver := &object_storage.OSSAvroDriver{}
	dsn := driver.BuildDSN(config)
	if dsn != "oss-avro://analytics/warehouse/events.avro" {
		t.Fatalf("BuildDSN() = %q", dsn)
	}
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
	for _, invalid := range []string{
		"oss-avro://bucket/events.json", "oss-avro://bucket/../events.avro", "oss-avro://bucket//events.avro",
	} {
		if err := driver.ValidateDSN(invalid); err == nil {
			t.Errorf("ValidateDSN(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestOSSAvroConfigRejectsInvalidSettings(t *testing.T) {
	base := func() *model.DataSourceConfig {
		return &model.DataSourceConfig{
			Host: "events.avro", Database: "analytics", Username: "access-key", Password: "access-secret",
			AdditionalProps: map[string]interface{}{"endpoint_url": "https://oss.example.test"},
		}
	}
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{"unsafe key", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "../events.avro" }, "safe relative"},
		{"unsupported extension", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "events.json" }, "safe relative"},
		{"empty bucket", func(c *model.DataSourceConfig) { c.Database = "" }, "bucket"},
		{"missing access key", func(c *model.DataSourceConfig) { c.Username = "" }, "access key ID and secret"},
		{"missing access secret", func(c *model.DataSourceConfig) { c.Password = "" }, "access key ID and secret"},
		{"missing endpoint", func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "endpoint_url") }, "endpoint_url is required"},
		{"endpoint query", func(c *model.DataSourceConfig) { c.AdditionalProps["endpoint_url"] = "https://oss.example.test/?sig=x" }, "without credentials, path, query, or fragment"},
		{"invalid path-style setting", func(c *model.DataSourceConfig) { c.AdditionalProps["force_path_style"] = "yes" }, "force_path_style must be true or false"},
		{"invalid size limit", func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = 64<<20 + 1 }, "between 1"},
		{"invalid row limit", func(c *model.DataSourceConfig) { c.AdditionalProps["max_rows"] = 100001 }, "between 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base()
			test.mutate(config)
			if err := object_storage.ValidateOSSAvroConfig(config); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateOSSAvroConfig() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestOSSAvroProtocolFixtureRejectsMalformedOCF(t *testing.T) {
	data := []byte("not-avro")
	var requestCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if r.URL.Path != "/bucket/events.avro" {
			http.Error(w, "unexpected OSS object path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "" || !strings.Contains(r.Header.Get("Authorization"), "access-key") {
			http.Error(w, "missing OSS authorization", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	config := &model.DataSourceConfig{
		Host: "events.avro", Database: "bucket", Username: "access-key", Password: "secret",
		AdditionalProps: map[string]interface{}{"endpoint_url": server.URL, "force_path_style": true},
	}
	db, err := (&object_storage.OSSAvroDriver{}).OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open malformed OSS Avro adapter: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD malformed OSS Avro object: %v", err)
	}
	if _, err := db.QueryContext(context.Background(), "SELECT * FROM events"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "avro") {
		t.Fatalf("malformed OCF query error = %v, want Avro parse error", err)
	}
	if requestCount.Load() < 2 {
		t.Fatalf("malformed OCF fixture received %d requests, want metadata and object read", requestCount.Load())
	}
}
