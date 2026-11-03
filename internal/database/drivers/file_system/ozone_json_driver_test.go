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

func ozoneJSONTestConfig(endpoint string) *model.DataSourceConfig {
	return &model.DataSourceConfig{
		Host: "warehouse/events.json", Database: "analytics", Username: "access-key", Password: "secret-key",
		AdditionalProps: map[string]interface{}{
			"endpoint_url": endpoint, "object_key": "warehouse/events.json", "table_name": "events",
		},
	}
}

func TestOzoneJSONGatewayRunsReadOnlySQLForSupportedDocumentForms(t *testing.T) {
	for _, tc := range []struct {
		name       string
		key        string
		data       string
		wantID     int64
		wantName   string
		wantActive int64
	}{
		{name: "object", key: "warehouse/events.json", data: `{"id":1,"name":"Ada","active":true}`, wantID: 1, wantName: "Ada", wantActive: 1},
		{name: "array", key: "warehouse/events.json", data: `[{"id":1,"name":"Ada","active":true},{"id":2,"name":"Lin","active":false},{"id":3,"name":"Mei","active":true}]`, wantID: 3, wantName: "Mei", wantActive: 2},
		{name: "JSON Lines", key: "warehouse/events.ndjson", data: "{\"id\":1,\"name\":\"Ada\",\"active\":true}\n{\"id\":2,\"name\":\"Lin\",\"active\":false}\n{\"id\":3,\"name\":\"Mei\",\"active\":true}\n", wantID: 3, wantName: "Mei", wantActive: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.data)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/analytics/"+tc.key {
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

			config := ozoneJSONTestConfig(server.URL)
			config.AdditionalProps["object_key"] = tc.key
			db, err := (&OzoneJSONDriver{}).OpenWithConfig(context.Background(), config)
			if err != nil {
				t.Fatalf("OpenWithConfig() error = %v", err)
			}
			defer db.Close()
			if err := db.PingContext(context.Background()); err != nil {
				t.Fatalf("PingContext() error = %v", err)
			}

			rows, err := db.QueryContext(context.Background(), "SELECT id, name FROM events ORDER BY id DESC LIMIT 1")
			if err != nil {
				t.Fatalf("QueryContext() error = %v", err)
			}
			if !rows.Next() {
				t.Fatalf("QueryContext() returned no row; rows.Err() = %v", rows.Err())
			}
			var id int64
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			if id != tc.wantID || name != tc.wantName {
				t.Fatalf("query row = (%d, %q), want (%d, %q)", id, name, tc.wantID, tc.wantName)
			}
			if rows.Next() {
				t.Fatal("query returned more than one row")
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("rows.Err() = %v", err)
			}
			_ = rows.Close()

			var count int64
			if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events WHERE active = true").Scan(&count); err != nil {
				t.Fatalf("COUNT(*) error = %v", err)
			}
			if count != tc.wantActive {
				t.Fatalf("COUNT(*) = %d, want %d", count, tc.wantActive)
			}
			if _, err := db.ExecContext(context.Background(), "DELETE FROM events"); err == nil || !strings.Contains(err.Error(), "read-only") {
				t.Fatalf("ExecContext() error = %v, want read-only rejection", err)
			}
		})
	}
}

func TestOzoneJSONRejectsNestedFieldsAndOversizedBody(t *testing.T) {
	for _, tc := range []struct {
		name  string
		data  string
		limit int64
		want  string
	}{
		{name: "nested values", data: `{"id":1,"details":{"city":"Paris"}}`, limit: 1024, want: "top-level scalar JSON fields"},
		{name: "body exceeds configured bound even when HEAD lies", data: `{"id":1,"padding":"` + strings.Repeat("x", 256) + `"}`, limit: 64, want: "configured maximum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.data)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/analytics/warehouse/events.json" {
					http.Error(w, "unexpected path", http.StatusNotFound)
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

			config := ozoneJSONTestConfig(server.URL)
			config.AdditionalProps["max_file_bytes"] = tc.limit
			db, err := (&OzoneJSONDriver{}).OpenWithConfig(context.Background(), config)
			if err != nil {
				t.Fatalf("OpenWithConfig() error = %v", err)
			}
			defer db.Close()
			var count int64
			err = db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events").Scan(&count)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("query error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestValidateOzoneJSONConfigAndDSN(t *testing.T) {
	config := ozoneJSONTestConfig("http://ozone.example.test")
	normalized, endpoint, err := NormalizeOzoneJSONConfig(config)
	if err != nil {
		t.Fatalf("NormalizeOzoneJSONConfig() error = %v", err)
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
	driver := &OzoneJSONDriver{}
	wantDSN := "ozone-s3://ozone.example.test:9878/analytics/warehouse/events.json"
	if got := driver.BuildDSN(config); got != wantDSN {
		t.Fatalf("BuildDSN() = %q, want %q", got, wantDSN)
	}
	if err := driver.ValidateDSN(wantDSN); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
	withoutTable := ozoneJSONTestConfig("http://ozone.example.test")
	delete(withoutTable.AdditionalProps, "table_name")
	if got, err := OzoneJSONTableName(withoutTable); err != nil || got != "events" {
		t.Fatalf("OzoneJSONTableName() = %q, %v; want events", got, err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{name: "missing endpoint", mutate: func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "endpoint_url") }, want: "endpoint_url"},
		{name: "missing credentials", mutate: func(c *model.DataSourceConfig) { c.Password = "" }, want: "access key and secret key"},
		{name: "path traversal", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "warehouse/../secret.json" }, want: "safe relative"},
		{name: "unsupported extension", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "warehouse/events.csv" }, want: "safe relative"},
		{name: "endpoint path", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["endpoint_url"] = "http://ozone.example.test/s3" }, want: "origin"},
		{name: "oversized limit", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = maxOzoneS3MaxFileBytes + 1 }, want: "between 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := ozoneJSONTestConfig("http://ozone.example.test")
			tc.mutate(invalid)
			if err := ValidateOzoneJSONConfig(invalid); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateOzoneJSONConfig() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}
