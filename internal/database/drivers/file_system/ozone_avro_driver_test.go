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

func ozoneAvroTestConfig(endpoint string) *model.DataSourceConfig {
	return &model.DataSourceConfig{
		Host: "warehouse/events.avro", Database: "analytics", Username: "access-key", Password: "secret-key",
		AdditionalProps: map[string]interface{}{
			"endpoint_url": endpoint, "object_key": "warehouse/events.avro", "table_name": "events",
		},
	}
}

func TestOzoneAvroGatewayRunsBoundedReadOnlySQL(t *testing.T) {
	data := createHDFSAvroFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/analytics/warehouse/events.avro" {
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

	db, err := (&OzoneAvroDriver{}).OpenWithConfig(context.Background(), ozoneAvroTestConfig(server.URL))
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("COUNT(*) error = %v", err)
	}
	if count != 2 {
		t.Fatalf("COUNT(*) = %d, want 2", count)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT id, name FROM events WHERE id >= 2 ORDER BY id DESC LIMIT 1")
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("QueryContext() returned no row; rows.Err() = %v", rows.Err())
	}
	var id int64
	var name string
	if err := rows.Scan(&id, &name); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if id != 3 || name != "Mei" {
		t.Fatalf("query row = (%d, %q), want (3, Mei)", id, name)
	}
	if _, err := db.ExecContext(context.Background(), "DELETE FROM events"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("ExecContext() error = %v, want read-only rejection", err)
	}
}

func TestOzoneAvroRejectsOversizedObjectEvenWhenHeadLies(t *testing.T) {
	data := createHDFSAvroFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	config := ozoneAvroTestConfig(server.URL)
	config.AdditionalProps["max_file_bytes"] = int64(100)
	db, err := (&OzoneAvroDriver{}).OpenWithConfig(context.Background(), config)
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

func TestValidateOzoneAvroConfigRejectsUnsafeOrUnsupportedConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{name: "missing endpoint", mutate: func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "endpoint_url") }, want: "endpoint_url"},
		{name: "missing credentials", mutate: func(c *model.DataSourceConfig) { c.Password = "" }, want: "access key and secret key"},
		{name: "path traversal", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "warehouse/../secret.avro" }, want: "safe relative"},
		{name: "wrong extension", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "warehouse/events.avro.gz" }, want: "safe relative"},
		{name: "endpoint path", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["endpoint_url"] = "http://ozone.example.test/s3" }, want: "origin"},
		{name: "oversized limit", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = maxOzoneAvroMaxFileBytes + 1 }, want: "between 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := ozoneAvroTestConfig("http://ozone.example.test")
			tc.mutate(config)
			if err := ValidateOzoneAvroConfig(config); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateOzoneAvroConfig() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestNormalizeOzoneAvroConfigAppliesGatewayDefaultsWithoutMutation(t *testing.T) {
	config := ozoneAvroTestConfig("http://ozone.example.test")
	normalized, endpoint, err := NormalizeOzoneAvroConfig(config)
	if err != nil {
		t.Fatalf("NormalizeOzoneAvroConfig() error = %v", err)
	}
	if endpoint != "http://ozone.example.test:9878" {
		t.Fatalf("normalized endpoint = %q, want default gateway port", endpoint)
	}
	if config.AdditionalProps["region"] != nil || config.AdditionalProps["force_path_style"] != nil {
		t.Fatal("normalization mutated the caller's AdditionalProps")
	}
	if normalized.AdditionalProps["region"] != "us-east-1" || normalized.AdditionalProps["force_path_style"] != true {
		t.Fatalf("normalized S3 defaults = %#v", normalized.AdditionalProps)
	}
	driver := &OzoneAvroDriver{}
	if got := driver.BuildDSN(config); got != "ozone-s3://ozone.example.test:9878/analytics/warehouse/events.avro" {
		t.Fatalf("BuildDSN() = %q", got)
	}
	if err := driver.ValidateDSN("ozone-s3://ozone.example.test:9878/analytics/warehouse/events.avro"); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
}
