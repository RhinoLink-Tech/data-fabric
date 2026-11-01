package file_system

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func ozoneParquetTestConfig(endpoint string) *model.DataSourceConfig {
	return &model.DataSourceConfig{
		Host:     "warehouse/events.parquet",
		Database: "analytics",
		Username: "access-key",
		Password: "secret-key",
		AdditionalProps: map[string]interface{}{
			"endpoint_url": endpoint,
			"object_key":   "warehouse/events.parquet",
			"table_name":   "events",
		},
	}
}

func TestOzoneParquetGatewayRunsBoundedReadOnlySQL(t *testing.T) {
	data := createHDFSCompressedParquetFixture(t)
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.URL.Path != "/analytics/warehouse/events.parquet" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=access-key/") {
			http.Error(w, "missing SigV4 credentials", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Header().Set("ETag", "fixture-etag")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer server.Close()

	db, err := (&OzoneParquetDriver{}).OpenWithConfig(context.Background(), ozoneParquetTestConfig(server.URL))
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT label, quantity FROM events WHERE quantity >= 2 ORDER BY quantity DESC LIMIT 1")
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	if !rows.Next() {
		t.Fatalf("QueryContext() returned no rows; rows.Err() = %v", rows.Err())
	}
	var label string
	var quantity int64
	if err := rows.Scan(&label, &quantity); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if label != "three" || quantity != 3 {
		t.Fatalf("query row = (%q, %d), want (three, 3)", label, quantity)
	}
	if rows.Next() {
		t.Fatal("query returned more than one row")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() = %v", err)
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events WHERE enabled = true").Scan(&count); err != nil {
		t.Fatalf("COUNT(*) error = %v", err)
	}
	if count != 2 {
		t.Fatalf("COUNT(*) = %d, want 2", count)
	}
	if _, err := db.ExecContext(context.Background(), "DELETE FROM events"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("ExecContext() error = %v, want read-only rejection", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) < 3 {
		t.Fatalf("gateway requests = %v, want metadata and object reads", requests)
	}
	for _, request := range requests {
		if !strings.HasSuffix(request, " /analytics/warehouse/events.parquet") {
			t.Errorf("gateway request %q did not use path-style bucket addressing", request)
		}
	}
}

func TestOzoneParquetRejectsOversizedObjectEvenWhenHeadLies(t *testing.T) {
	data := createHDFSParquetFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/analytics/warehouse/events.parquet" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "1")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer server.Close()

	config := ozoneParquetTestConfig(server.URL)
	config.AdditionalProps["max_file_bytes"] = int64(100)
	db, err := (&OzoneParquetDriver{}).OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
	var count int64
	err = db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events").Scan(&count)
	if err == nil || !strings.Contains(err.Error(), "exceeds configured maximum") {
		t.Fatalf("query error = %v, want bounded-read rejection", err)
	}
}

func TestNormalizeOzoneParquetConfigAppliesGatewayDefaultsWithoutMutation(t *testing.T) {
	config := ozoneParquetTestConfig("http://ozone.example.test")
	normalized, endpoint, err := NormalizeOzoneParquetConfig(config)
	if err != nil {
		t.Fatalf("NormalizeOzoneParquetConfig() error = %v", err)
	}
	if endpoint != "http://ozone.example.test:9878" {
		t.Fatalf("normalized endpoint = %q, want default gateway port", endpoint)
	}
	if config.AdditionalProps["region"] != nil || config.AdditionalProps["force_path_style"] != nil {
		t.Fatal("config normalization mutated the caller's AdditionalProps")
	}
	if normalized.AdditionalProps["region"] != "us-east-1" || normalized.AdditionalProps["force_path_style"] != true {
		t.Fatalf("normalized S3 defaults = %#v", normalized.AdditionalProps)
	}
	if got := (&OzoneParquetDriver{}).BuildDSN(config); got != "ozone-s3://ozone.example.test:9878/analytics/warehouse/events.parquet" {
		t.Fatalf("BuildDSN() = %q", got)
	}
	if err := (&OzoneParquetDriver{}).ValidateDSN("ozone-s3://ozone.example.test:9878/analytics/warehouse/events.parquet"); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
}

func TestValidateOzoneParquetConfigRejectsUnsafeOrUnsupportedConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{name: "missing endpoint", mutate: func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "endpoint_url") }, want: "endpoint_url"},
		{name: "missing access key", mutate: func(c *model.DataSourceConfig) { c.Username = "" }, want: "access key and secret key"},
		{name: "path traversal", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "warehouse/../secret.parquet" }, want: "safe relative"},
		{name: "external gzip", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "warehouse/events.parquet.gz" }, want: "safe relative"},
		{name: "endpoint path", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["endpoint_url"] = "http://ozone.example.test/s3" }, want: "origin"},
		{name: "oversized limit", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = maxOzoneParquetMaxFileBytes + 1 }, want: "between 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := ozoneParquetTestConfig("http://ozone.example.test")
			tc.mutate(config)
			if err := ValidateOzoneParquetConfig(config); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateOzoneParquetConfig() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}
