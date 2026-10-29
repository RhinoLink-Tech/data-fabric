package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestFetchQueryRoutesS3ORCThroughGateway(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "database", "drivers", "object_storage", "testdata", "orc_split_elim.orc"))
	if err != nil {
		t.Fatalf("read ORC fixture: %v", err)
	}
	var getCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo-bucket/data.orc" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", "\"orc-fixture\"")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			getCalls.Add(1)
			start, end := int64(0), int64(len(data))-1
			if value := r.Header.Get("Range"); value != "" {
				if _, err := fmt.Sscanf(value, "bytes=%d-%d", &start, &end); err != nil || start < 0 || end < start || start >= int64(len(data)) {
					http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
					return
				}
				if end >= int64(len(data)) {
					end = int64(len(data)) - 1
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
				w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
				w.WriteHeader(http.StatusPartialContent)
			}
			_, _ = w.Write(data[start : end+1])
		default:
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	source := &model.DataSource{
		ID: "s3-orc-query-service-test", Type: model.DatabaseTypeS3ORC, Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: "data.orc", Database: "demo-bucket", Username: "test-access-key", Password: "test-secret-key",
			AdditionalProps: map[string]interface{}{
				"object_key": "data.orc", "table_name": "records", "region": "us-east-1",
				"endpoint_url": server.URL, "force_path_style": true,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate S3 ORC source config: %v", err)
	}

	queryService := NewQueryService(federationSourceRepository{source: source}, pool, false, newTestSQLTranslationManager())
	response, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID,
		SQL:          "SELECT userid, string1 FROM records WHERE userid = 2 ORDER BY string1 LIMIT 1",
		Type:         2,
		BatchSize:    10,
	})
	if err != nil {
		t.Fatalf("FetchQuery() error = %v", err)
	}
	if response.TotalCount != 1 || len(response.Entries) != 1 {
		t.Fatalf("response total=%d entries=%d, want one matching ORC row", response.TotalCount, len(response.Entries))
	}
	if len(response.Columns) != 2 || response.Columns[0].Name != "userid" || response.Columns[1].Name != "string1" {
		t.Fatalf("response columns = %+v, want userid,string1", response.Columns)
	}
	entry := response.Entries[0]
	if entry == nil || len(*entry) != 2 {
		t.Fatalf("response entry = %#v, want two values", entry)
	}
	userID, ok := (*entry)[0].(int64)
	if !ok || userID != 2 || (*entry)[1] != "foo" {
		t.Fatalf("response row = %#v, want [2 foo]", *entry)
	}

	beforeWrite := getCalls.Load()
	if _, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID, SQL: "DELETE FROM records", Type: 1, BatchSize: 10,
	}); err == nil {
		t.Fatal("FetchQuery() accepted an S3 ORC write")
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection read object rows: GET calls before=%d after=%d", beforeWrite, afterWrite)
	}
	if getCalls.Load() == 0 {
		t.Fatal("expected the read-only query to retrieve S3 object ranges")
	}
}
