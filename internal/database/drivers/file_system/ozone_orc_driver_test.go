package file_system

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func ozoneORCTestConfig(endpoint string) *model.DataSourceConfig {
	return &model.DataSourceConfig{
		Host: "warehouse/events.orc", Database: "analytics", Username: "access-key", Password: "secret-key",
		AdditionalProps: map[string]interface{}{
			"endpoint_url": endpoint, "object_key": "warehouse/events.orc", "table_name": "events",
		},
	}
}

func createOzoneORCFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "object_storage", "testdata", "orc_split_elim.orc"))
	if err != nil {
		t.Fatalf("read ORC fixture: %v", err)
	}
	return data
}

func TestOzoneORCGatewayRunsBoundedReadOnlySQL(t *testing.T) {
	data := createOzoneORCFixture(t)
	var mu sync.Mutex
	var requests []string
	var holdGET atomic.Bool
	blockedGET := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.URL.Path != "/analytics/warehouse/events.orc" {
			http.Error(w, "unexpected Ozone S3 Gateway object path", http.StatusNotFound)
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
		if holdGET.Load() {
			select {
			case blockedGET <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()

	db, err := (&OzoneORCDriver{}).OpenWithConfig(context.Background(), ozoneORCTestConfig(server.URL))
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT userid, string1 FROM events WHERE userid = 2 ORDER BY string1 LIMIT 1")
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	if !rows.Next() {
		t.Fatalf("QueryContext() returned no row; rows.Err() = %v", rows.Err())
	}
	var userID int64
	var value string
	if err := rows.Scan(&userID, &value); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if userID != 2 || value != "foo" {
		t.Fatalf("query row = (%d, %q), want (2, foo)", userID, value)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("query returned an extra row or iteration error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events").Scan(&count); err != nil {
		t.Fatalf("COUNT(*) error = %v", err)
	}
	if count != 25000 {
		t.Fatalf("COUNT(*) = %d, want 25000", count)
	}
	if _, err := db.ExecContext(context.Background(), "DELETE FROM events"); err == nil || !strings.Contains(err.Error(), "read-only SELECT") {
		t.Fatalf("ExecContext() error = %v, want read-only rejection", err)
	}

	holdGET.Store(true)
	queryCtx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, queryErr := db.QueryContext(queryCtx, "SELECT * FROM events")
		result <- queryErr
	}()
	select {
	case <-blockedGET:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Ozone S3 Gateway fixture did not receive the cancellable GET")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled query error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled query did not return")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) < 4 {
		t.Fatalf("gateway requests = %v, want metadata and object reads", requests)
	}
	for _, request := range requests {
		if !strings.HasSuffix(request, " /analytics/warehouse/events.orc") {
			t.Errorf("gateway request %q did not use path-style bucket addressing", request)
		}
	}
}

func TestOzoneORCRejectsOversizedObjectEvenWhenHeadLies(t *testing.T) {
	data := createOzoneORCFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/analytics/warehouse/events.orc" {
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

	config := ozoneORCTestConfig(server.URL)
	config.AdditionalProps["max_file_bytes"] = int64(len(data) - 1)
	db, err := (&OzoneORCDriver{}).OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
	var count int64
	err = db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events").Scan(&count)
	if err == nil || !strings.Contains(err.Error(), "configured maximum") {
		t.Fatalf("query error = %v, want bounded-read rejection", err)
	}
}

func TestORCSQLDatabaseEnforcesDecodedRowLimit(t *testing.T) {
	data := createOzoneORCFixture(t)
	db, err := object_storage.NewORCSQLDatabase(object_storage.ORCSQLDatabaseConfig{
		DriverName: "ozone-orc-row-limit", ObjectKey: "events.orc", TableName: "events",
		BatchSize: 1, MaxFileBytes: int64(len(data)), MaxRows: 1,
	}, func(context.Context, string) error { return nil }, func(context.Context, string) ([]byte, error) {
		return append([]byte(nil), data...), nil
	}, nil)
	if err != nil {
		t.Fatalf("NewORCSQLDatabase() error = %v", err)
	}
	defer db.Close()
	if _, err := db.QueryContext(context.Background(), "SELECT * FROM events"); err == nil || !strings.Contains(err.Error(), "row limit") {
		t.Fatalf("QueryContext() error = %v, want row-limit rejection", err)
	}
}

func TestNormalizeOzoneORCConfigAppliesGatewayDefaultsWithoutMutation(t *testing.T) {
	config := ozoneORCTestConfig("http://ozone.example.test")
	normalized, endpoint, err := NormalizeOzoneORCConfig(config)
	if err != nil {
		t.Fatalf("NormalizeOzoneORCConfig() error = %v", err)
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
	driver := &OzoneORCDriver{}
	if got := driver.BuildDSN(config); got != "ozone-s3://ozone.example.test:9878/analytics/warehouse/events.orc" {
		t.Fatalf("BuildDSN() = %q", got)
	}
	if err := driver.ValidateDSN("ozone-s3://ozone.example.test:9878/analytics/warehouse/events.orc"); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
}

func TestValidateOzoneORCConfigRejectsUnsafeOrUnsupportedConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*model.DataSourceConfig)
		want   string
	}{
		{name: "missing endpoint", mutate: func(c *model.DataSourceConfig) { delete(c.AdditionalProps, "endpoint_url") }, want: "endpoint_url"},
		{name: "missing access key", mutate: func(c *model.DataSourceConfig) { c.Username = "" }, want: "access key and secret key"},
		{name: "path traversal", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "warehouse/../secret.orc" }, want: "safe relative"},
		{name: "external gzip", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["object_key"] = "warehouse/events.orc.gz" }, want: "safe relative"},
		{name: "endpoint path", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["endpoint_url"] = "http://ozone.example.test/s3" }, want: "origin"},
		{name: "oversized limit", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = maxOzoneORCMaxFileBytes + 1 }, want: "between 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := ozoneORCTestConfig("http://ozone.example.test")
			tc.mutate(config)
			if err := ValidateOzoneORCConfig(config); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateOzoneORCConfig() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}
