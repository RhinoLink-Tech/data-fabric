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

func TestOSSJSONProtocolFixture(t *testing.T) {
	data := []byte(`[{"id":1,"name":"Ada","profile":{"city":"Paris"}},{"id":2,"name":"Lin","profile":{"city":"Beijing"}}]`)
	var getCalls atomic.Int64
	var holdGET atomic.Bool
	blockedGETs := make(chan struct{}, 1)
	headLies := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-bucket/contract/records.jsonl" {
			http.Error(w, "unexpected object path", http.StatusNotFound)
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
		contentLength := len(data)
		if headLies && r.Method == http.MethodHead {
			contentLength = 1
		}
		w.Header().Set("Content-Length", strconv.Itoa(contentLength))
		w.Header().Set("ETag", "\"oss-json-fixture\"")
		switch r.Method {
		case http.MethodHead:
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
			_, _ = w.Write(data)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	const objectKey = "contract/records.jsonl"
	source := &model.DataSource{
		ID: "oss-json-protocol-fixture", Type: model.DatabaseTypeAlibabaOSSJSON,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: "test-bucket", Username: "test-access-key", Password: "test-secret",
			AdditionalProps: map[string]interface{}{
				"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
				"force_path_style": true, "session_token": "test-session-token", "flatten_nested": true,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate OSS JSON config: %v", err)
	}
	invalid := source.Config
	invalid.AdditionalProps = map[string]interface{}{"object_key": objectKey}
	if err := checker.ValidateDataSourceConfiguration(&invalid, source.Type); err == nil {
		t.Fatal("OSS JSON config validation accepted a missing endpoint")
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
	if err != nil {
		t.Fatalf("check OSS JSON connectivity: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("OSS JSON health status = %q (message: %s)", health.Status, health.Message)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(context.Background(), source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list OSS JSON tables = %+v, error = %v", tables, err)
	}
	table, err := extractor.ExtractTableSchema(context.Background(), source.ID, "records")
	if err != nil {
		t.Fatalf("describe OSS JSON table: %v", err)
	}
	if len(table.Columns) != 3 || table.Columns[0].Name != "id" || table.Columns[1].Name != "name" || table.Columns[2].Name != "profile_city" {
		t.Fatalf("OSS JSON columns = %+v", table.Columns)
	}

	db, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("open OSS JSON adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD configured OSS object: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT name, profile_city FROM records WHERE id > 1 ORDER BY id LIMIT 1")
	if err != nil {
		t.Fatalf("query OSS JSON object: %v", err)
	}
	if !rows.Next() {
		t.Fatalf("expected one OSS JSON row, rows error: %v", rows.Err())
	}
	var name, city string
	if err := rows.Scan(&name, &city); err != nil {
		t.Fatalf("scan OSS JSON row: %v", err)
	}
	if name != "Lin" || city != "Beijing" {
		t.Fatalf("OSS JSON row = (%q, %q), want (Lin, Beijing)", name, city)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("OSS JSON query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()
	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records").Scan(&count); err != nil {
		t.Fatalf("count OSS JSON rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("OSS JSON row count = %d, want 2", count)
	}
	beforeWrite := getCalls.Load()
	if _, err := db.ExecContext(context.Background(), "DELETE FROM records"); err == nil {
		t.Fatal("OSS JSON adapter accepted a write")
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection fetched the object: before=%d after=%d", beforeWrite, afterWrite)
	}

	holdGET.Store(true)
	queryCtx, cancelQuery := context.WithCancel(context.Background())
	queryResult := make(chan error, 1)
	go func() {
		_, queryErr := db.QueryContext(queryCtx, "SELECT * FROM records")
		queryResult <- queryErr
	}()
	select {
	case <-blockedGETs:
	case <-time.After(5 * time.Second):
		cancelQuery()
		t.Fatal("OSS JSON fixture did not receive the cancellable GET")
	}
	cancelQuery()
	select {
	case err := <-queryResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled OSS JSON query error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled OSS JSON query did not return")
	}
	holdGET.Store(false)

	for _, fixture := range []struct {
		name string
		data string
		want int64
	}{
		{name: "single object", data: `{"id":7,"name":"solo"}`, want: 1},
		{name: "JSON array", data: `[{"id":1},{"id":2}]`, want: 2},
		{name: "JSON Lines", data: "{\"id\":1}\n{\"id\":2}\n", want: 2},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			data = []byte(fixture.data)
			var got int64
			if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records").Scan(&got); err != nil {
				t.Fatalf("count fixture records: %v", err)
			}
			if got != fixture.want {
				t.Fatalf("fixture record count = %d, want %d", got, fixture.want)
			}
		})
	}

	data = []byte("not-json")
	if _, err := db.QueryContext(context.Background(), "SELECT * FROM records"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "json") {
		t.Fatalf("malformed JSON query error = %v, want JSON parse failure", err)
	}

	data = []byte(strings.Repeat("x", 65))
	headLies = true
	limitedConfig := source.Config
	limitedConfig.AdditionalProps = map[string]interface{}{
		"object_key": objectKey, "endpoint_url": server.URL, "force_path_style": true, "max_file_bytes": 64,
	}
	limited, err := (&object_storage.OSSJSONDriver{}).OpenWithConfig(context.Background(), &limitedConfig)
	if err != nil {
		t.Fatalf("open size-limited OSS JSON adapter: %v", err)
	}
	defer limited.Close()
	if err := limited.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD with intentionally inaccurate size: %v", err)
	}
	if _, err := limited.QueryContext(context.Background(), "SELECT * FROM records"); err == nil || !strings.Contains(err.Error(), "64 bytes") {
		t.Fatalf("oversized OSS JSON query error = %v, want 64-byte limit", err)
	}

	data = []byte(strings.Repeat("{\"id\":1}\n", 100001))
	headLies = false
	rowLimitedConfig := source.Config
	rowLimitedConfig.AdditionalProps = map[string]interface{}{
		"object_key": objectKey, "endpoint_url": server.URL, "force_path_style": true,
	}
	rowLimited, err := (&object_storage.OSSJSONDriver{}).OpenWithConfig(context.Background(), &rowLimitedConfig)
	if err != nil {
		t.Fatalf("open record-limited OSS JSON adapter: %v", err)
	}
	defer rowLimited.Close()
	if _, err := rowLimited.QueryContext(context.Background(), "SELECT COUNT(*) FROM records"); err == nil || !strings.Contains(err.Error(), "100000") {
		t.Fatalf("record-limit error = %v, want 100,000-record limit", err)
	}
}

func TestOSSJSONDriverConfigurationAndDSN(t *testing.T) {
	config := &model.DataSourceConfig{
		Host: "warehouse/events.jsonl", Database: "analytics", Username: "access-key", Password: "access-secret",
		AdditionalProps: map[string]interface{}{"endpoint_url": "oss-cn-hangzhou.aliyuncs.com", "flatten_nested": true, "security_token": "sts-token"},
	}
	normalized, err := object_storage.NormalizeOSSJSONConfig(config)
	if err != nil {
		t.Fatalf("normalize OSS JSON config: %v", err)
	}
	if normalized.AdditionalProps["endpoint_url"] != "https://oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("normalized endpoint = %#v", normalized.AdditionalProps["endpoint_url"])
	}
	endpointAlias := *config
	endpointAlias.AdditionalProps = map[string]interface{}{"endpoint": "oss-cn-hangzhou.aliyuncs.com"}
	aliased, err := object_storage.NormalizeOSSJSONConfig(&endpointAlias)
	if err != nil || aliased.AdditionalProps["endpoint_url"] != "https://oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("normalize OSS endpoint alias = %#v, error = %v", aliased, err)
	}
	driver := &object_storage.OSSJSONDriver{}
	dsn := driver.BuildDSN(config)
	if dsn != "oss-json://analytics/warehouse/events.jsonl" {
		t.Fatalf("BuildDSN() = %q", dsn)
	}
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
	for _, invalid := range []string{
		"oss-json://bucket/events.txt", "oss-json://bucket/../events.json", "oss-json://bucket//events.json",
	} {
		if err := driver.ValidateDSN(invalid); err == nil {
			t.Errorf("ValidateDSN(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestOSSJSONConfigRejectsInvalidSettings(t *testing.T) {
	base := func() *model.DataSourceConfig {
		return &model.DataSourceConfig{
			Host: "events.json", Database: "analytics", Username: "access-key", Password: "access-secret",
			AdditionalProps: map[string]interface{}{"endpoint_url": "oss-cn-hangzhou.aliyuncs.com"},
		}
	}
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{"missing endpoint", func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "endpoint_url") }, "endpoint_url is required"},
		{"unsafe key", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "../events.json" }, "safe relative"},
		{"wrong extension", func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "events.txt" }, "safe relative"},
		{"empty bucket", func(c *model.DataSourceConfig) { c.Database = "" }, "bucket"},
		{"missing credentials", func(c *model.DataSourceConfig) { c.Password = "" }, "access key ID and secret"},
		{"endpoint path", func(c *model.DataSourceConfig) { c.AdditionalProps["endpoint_url"] = "https://oss.example.test/api" }, "without credentials, path"},
		{"invalid path style flag", func(c *model.DataSourceConfig) { c.AdditionalProps["force_path_style"] = "sometimes" }, "force_path_style must be true or false"},
		{"invalid flatten flag", func(c *model.DataSourceConfig) { c.AdditionalProps["flatten_nested"] = "sometimes" }, "flatten_nested must be true or false"},
		{"oversize limit", func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = 64<<20 + 1 }, "between 1"},
		{"oversize records", func(c *model.DataSourceConfig) { c.AdditionalProps["max_records"] = 100001 }, "between 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base()
			test.mutate(config)
			err := object_storage.ValidateOSSJSONConfig(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateOSSJSONConfig() error = %v, want containing %q", err, test.want)
			}
		})
	}
}
