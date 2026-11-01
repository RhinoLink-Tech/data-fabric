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

func ozoneTextTestConfig(endpoint string) *model.DataSourceConfig {
	return &model.DataSourceConfig{
		Host: "warehouse/events.log", Database: "analytics", Username: "access-key", Password: "secret-key",
		AdditionalProps: map[string]interface{}{
			"endpoint_url": endpoint,
			"object_key":   "warehouse/events.log",
			"table_name":   "events",
		},
	}
}

func TestOzoneTextGatewayRunsBoundedReadOnlySQL(t *testing.T) {
	data := []byte("first\r\n\r\nthird")
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.URL.Path != "/analytics/warehouse/events.log" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=access-key/") {
			http.Error(w, "missing SigV4 credentials", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
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

	db, err := (&OzoneTextDriver{}).OpenWithConfig(context.Background(), ozoneTextTestConfig(server.URL))
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT line_number, line FROM events ORDER BY line_number DESC LIMIT 2")
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	defer rows.Close()
	want := []struct {
		number int64
		line   string
	}{{number: 3, line: "third"}, {number: 2, line: ""}}
	for _, expected := range want {
		if !rows.Next() {
			t.Fatalf("QueryContext() ended early; rows.Err() = %v", rows.Err())
		}
		var number int64
		var line string
		if err := rows.Scan(&number, &line); err != nil {
			t.Fatalf("Scan() error = %v", err)
		}
		if number != expected.number || line != expected.line {
			t.Errorf("row = (%d, %q), want (%d, %q)", number, line, expected.number, expected.line)
		}
	}
	if rows.Next() {
		t.Fatal("query returned more than two rows")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() = %v", err)
	}

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events").Scan(&count); err != nil {
		t.Fatalf("COUNT(*) error = %v", err)
	}
	if count != 3 {
		t.Fatalf("COUNT(*) = %d, want 3", count)
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
		if !strings.HasSuffix(request, " /analytics/warehouse/events.log") {
			t.Errorf("gateway request %q did not use path-style bucket addressing", request)
		}
	}
}

func TestOzoneTextRejectsOversizedObjectEvenWhenHeadLies(t *testing.T) {
	data := []byte(strings.Repeat("line\n", 40))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/analytics/warehouse/events.log" {
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

	config := ozoneTextTestConfig(server.URL)
	config.AdditionalProps["max_file_bytes"] = int64(100)
	db, err := (&OzoneTextDriver{}).OpenWithConfig(context.Background(), config)
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

func TestValidateOzoneTextConfigRejectsUnsafeOrUnsupportedConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{name: "missing endpoint", mutate: func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "endpoint_url") }, want: "endpoint_url"},
		{name: "missing credentials", mutate: func(c *model.DataSourceConfig) { c.Username = "" }, want: "access key and secret key"},
		{name: "path traversal", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "warehouse/../secret.log" }, want: "safe relative"},
		{name: "parquet object", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "warehouse/events.parquet" }, want: "safe relative"},
		{name: "endpoint path", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["endpoint_url"] = "http://ozone.example.test/s3" }, want: "origin"},
		{name: "oversized limit", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = maxOzoneTextMaxFileBytes + 1 }, want: "between 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := ozoneTextTestConfig("http://ozone.example.test")
			tc.mutate(config)
			if err := ValidateOzoneTextConfig(config); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateOzoneTextConfig() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestNormalizeOzoneTextConfigUsesGatewayDefaults(t *testing.T) {
	config := ozoneTextTestConfig("http://ozone.example.test")
	normalized, endpoint, err := NormalizeOzoneTextConfig(config)
	if err != nil {
		t.Fatalf("NormalizeOzoneTextConfig() error = %v", err)
	}
	if endpoint != "http://ozone.example.test:9878" {
		t.Fatalf("normalized endpoint = %q, want default gateway port", endpoint)
	}
	if normalized.AdditionalProps["region"] != "us-east-1" || normalized.AdditionalProps["force_path_style"] != true {
		t.Fatalf("normalized S3 defaults = %#v", normalized.AdditionalProps)
	}
	if config.AdditionalProps["region"] != nil || config.AdditionalProps["force_path_style"] != nil {
		t.Fatal("config normalization mutated the caller's AdditionalProps")
	}
	if got := (&OzoneTextDriver{}).BuildDSN(config); got != "ozone-s3://ozone.example.test:9878/analytics/warehouse/events.log" {
		t.Fatalf("BuildDSN() = %q", got)
	}
	if err := (&OzoneTextDriver{}).ValidateDSN("ozone-s3://ozone.example.test:9878/analytics/warehouse/events.log"); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
}
