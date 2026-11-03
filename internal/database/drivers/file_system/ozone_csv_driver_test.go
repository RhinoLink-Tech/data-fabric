package file_system

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func ozoneCSVTestConfig(endpoint string) *model.DataSourceConfig {
	return &model.DataSourceConfig{
		Host: "warehouse/events.csv", Database: "analytics", Username: "access-key", Password: "secret-key",
		AdditionalProps: map[string]interface{}{
			"endpoint_url": endpoint, "object_key": "warehouse/events.csv", "table_name": "events",
		},
	}
}

func TestOzoneCSVGatewayRunsBoundedReadOnlySQL(t *testing.T) {
	data := []byte("generated report\nid;name;score\n1;Ada;1.5\n2;Lin;NULL\n3;Mei;3.25\n")
	headLies := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/analytics/warehouse/events.csv" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=access-key/") {
			http.Error(w, "missing SigV4 credentials", http.StatusForbidden)
			return
		}
		contentLength := len(data)
		if headLies {
			contentLength = 1
		}
		w.Header().Set("Content-Length", fmt.Sprint(contentLength))
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

	config := ozoneCSVTestConfig(server.URL)
	config.AdditionalProps["delimiter"] = ";"
	config.AdditionalProps["skip_header_rows"] = 1
	config.AdditionalProps["null_values"] = []string{"NULL"}
	db, err := (&OzoneCSVDriver{}).OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}

	rows, err := db.QueryContext(context.Background(), "SELECT id, name, score FROM events WHERE id >= 2 ORDER BY id DESC LIMIT 2")
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("QueryContext() returned no row; rows.Err() = %v", rows.Err())
	}
	var id int64
	var name string
	var score any
	if err := rows.Scan(&id, &name, &score); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if id != 3 || name != "Mei" || score != float64(3.25) {
		t.Fatalf("first row = (%d, %q, %v), want (3, Mei, 3.25)", id, name, score)
	}
	if !rows.Next() {
		t.Fatalf("second row missing; rows.Err() = %v", rows.Err())
	}
	if err := rows.Scan(&id, &name, &score); err != nil {
		t.Fatalf("second Scan() error = %v", err)
	}
	if id != 2 || name != "Lin" || score != nil {
		t.Fatalf("second row = (%d, %q, %v), want (2, Lin, NULL)", id, name, score)
	}
	if rows.Next() {
		t.Fatal("query returned more than two rows")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() = %v", err)
	}

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events WHERE id > 1").Scan(&count); err != nil {
		t.Fatalf("COUNT(*) error = %v", err)
	}
	if count != 2 {
		t.Fatalf("COUNT(*) = %d, want 2", count)
	}
	if _, err := db.ExecContext(context.Background(), "DELETE FROM events"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("ExecContext() error = %v, want read-only rejection", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	data = []byte(strings.Repeat("1,ada\n", 40))
	headLies = true
	limitedConfig := ozoneCSVTestConfig(server.URL)
	limitedConfig.AdditionalProps["max_file_bytes"] = 64
	limitedDB, err := (&OzoneCSVDriver{}).OpenWithConfig(context.Background(), limitedConfig)
	if err != nil {
		t.Fatalf("OpenWithConfig() with small byte limit error = %v", err)
	}
	defer limitedDB.Close()
	if err := limitedDB.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() with deliberately inaccurate HEAD size error = %v", err)
	}
	if _, err := limitedDB.QueryContext(context.Background(), "SELECT * FROM events"); err == nil {
		t.Fatal("query unexpectedly accepted an oversized GET body")
	}
	if err := limitedDB.Close(); err != nil {
		t.Fatalf("close limited database: %v", err)
	}

	var manyRows strings.Builder
	manyRows.WriteString("id\n")
	for i := 0; i <= ozoneS3MaxRows; i++ {
		fmt.Fprintf(&manyRows, "%d\n", i)
	}
	data = []byte(manyRows.String())
	headLies = false
	rowLimitedDB, err := (&OzoneCSVDriver{}).OpenWithConfig(context.Background(), ozoneCSVTestConfig(server.URL))
	if err != nil {
		t.Fatalf("OpenWithConfig() for row limit error = %v", err)
	}
	defer rowLimitedDB.Close()
	if err := rowLimitedDB.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() for row limit error = %v", err)
	}
	if _, err := rowLimitedDB.QueryContext(context.Background(), "SELECT COUNT(*) FROM events"); err == nil || !strings.Contains(err.Error(), "100000") {
		t.Fatalf("query error = %v, want configured 100,000-row limit", err)
	}
}

func TestNormalizeOzoneCSVConfigAndDSN(t *testing.T) {
	config := ozoneCSVTestConfig("https://ozone.example.test")
	normalized, endpoint, err := NormalizeOzoneCSVConfig(config)
	if err != nil {
		t.Fatalf("NormalizeOzoneCSVConfig() error = %v", err)
	}
	if endpoint != "https://ozone.example.test:9878" {
		t.Fatalf("normalized endpoint = %q, want default port", endpoint)
	}
	if normalized.AdditionalProps["force_path_style"] != true || normalized.AdditionalProps["region"] != "us-east-1" {
		t.Fatalf("gateway defaults = %#v", normalized.AdditionalProps)
	}
	if config.AdditionalProps["force_path_style"] != nil {
		t.Fatal("normalization mutated the caller configuration")
	}
	driver := &OzoneCSVDriver{}
	if got := driver.BuildDSN(config); got != "ozone-s3://ozone.example.test:9878/analytics/warehouse/events.csv" {
		t.Fatalf("BuildDSN() = %q", got)
	}
	if err := driver.ValidateDSN("ozone-s3://ozone.example.test:9878/analytics/warehouse/events.csv"); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
	for _, dsn := range []string{
		"ozone-s3://gateway/analytics/warehouse/events.txt",
		"ozone-s3://gateway/analytics/../events.csv",
		"ozone-s3://gateway/analytics//events.csv",
	} {
		if err := driver.ValidateDSN(dsn); err == nil {
			t.Errorf("ValidateDSN(%q) unexpectedly succeeded", dsn)
		}
	}
}

func TestValidateOzoneCSVConfigRejectsUnsafeSettings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{name: "missing endpoint", mutate: func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "endpoint_url") }, want: "endpoint_url is required"},
		{name: "unsafe key", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "../events.csv" }, want: "safe relative object key"},
		{name: "wrong extension", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "events.txt" }, want: "safe relative object key"},
		{name: "empty bucket", mutate: func(c *model.DataSourceConfig) { c.Database = "" }, want: "bucket is required"},
		{name: "missing credentials", mutate: func(c *model.DataSourceConfig) { c.Password = "" }, want: "access key and secret key are required"},
		{name: "endpoint path", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["endpoint_url"] = "https://ozone.example.test/api" }, want: "without credentials, path"},
		{name: "multi-character delimiter", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["delimiter"] = "||" }, want: "exactly one character"},
		{name: "unsupported quote", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["quote_char"] = "'" }, want: "standard double-quote"},
		{name: "negative header rows", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["skip_header_rows"] = -1 }, want: "non-negative integer"},
		{name: "fractional header rows", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["skip_header_rows"] = 1.5 }, want: "non-negative integer"},
		{name: "invalid header flag", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["has_header"] = "sometimes" }, want: "has_header must be true or false"},
		{name: "oversized maximum", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = maxOzoneS3MaxFileBytes + 1 }, want: "between 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := ozoneCSVTestConfig("http://ozone.example.test")
			tc.mutate(config)
			if err := ValidateOzoneCSVConfig(config); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateOzoneCSVConfig() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}
