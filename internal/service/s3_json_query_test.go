package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestFetchQueryRoutesS3JSONDocumentFormsThroughGateway(t *testing.T) {
	fixtures := map[string][]byte{
		"object.json":    []byte("{\"id\":3,\"name\":\"Grace\",\"active\":true,\"optional\":null}"),
		"array.json":     []byte("[{\"id\":1,\"name\":\"Ada\",\"active\":true,\"optional\":\"x\"},{\"id\":2,\"name\":\"Linus\",\"active\":false},{\"id\":3,\"name\":\"Grace\",\"active\":true,\"optional\":null}]"),
		"records.ndjson": []byte("{\"id\":1,\"name\":\"Ada\",\"active\":true,\"optional\":\"x\"}\n{\"id\":2,\"name\":\"Linus\",\"active\":false}\n{\"id\":3,\"name\":\"Grace\",\"active\":true,\"optional\":null}\n"),
	}
	var getCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		data, exists := fixtures[key[len("/demo-bucket/"):]]
		if !exists {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", "\"json-fixture\"")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			getCalls.Add(1)
			_, _ = w.Write(data)
		default:
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	for key := range fixtures {
		source := &model.DataSource{
			ID: "s3-json-query-service-" + key, Type: model.DatabaseTypeS3JSON, Status: model.DataSourceStatusActive,
			Config: model.DataSourceConfig{
				Host: key, Database: "demo-bucket", Username: "test-access-key", Password: "test-secret-key",
				AdditionalProps: map[string]interface{}{
					"object_key": key, "table_name": "records", "region": "us-east-1",
					"endpoint_url": server.URL, "force_path_style": true,
				},
			},
		}
		queryService := NewQueryService(federationSourceRepository{source: source}, pool, false, newTestSQLTranslationManager())
		response, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
			DataSourceID: source.ID,
			SQL:          "SELECT id, name FROM records WHERE active = true ORDER BY id DESC",
			Type:         2,
			BatchSize:    10,
		})
		if err != nil {
			t.Fatalf("%s FetchQuery() error = %v", key, err)
		}
		wantRows := 1
		if key != "object.json" {
			wantRows = 2
		}
		if response.TotalCount != int64(wantRows) || len(response.Entries) != wantRows {
			t.Fatalf("%s response count=%d entries=%d, want %d", key, response.TotalCount, len(response.Entries), wantRows)
		}
		entry := response.Entries[0]
		if entry == nil || len(*entry) != 2 || (*entry)[0] != int64(3) || (*entry)[1] != "Grace" {
			t.Fatalf("%s first entry = %#v, want [3 Grace]", key, entry)
		}
	}
	source := &model.DataSource{
		ID: "s3-json-query-service-object.json", Type: model.DatabaseTypeS3JSON, Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: "object.json", Database: "demo-bucket", Username: "test-access-key", Password: "test-secret-key",
			AdditionalProps: map[string]interface{}{
				"object_key": "object.json", "table_name": "records", "region": "us-east-1",
				"endpoint_url": server.URL, "force_path_style": true,
			},
		},
	}
	queryService := NewQueryService(federationSourceRepository{source: source}, pool, false, newTestSQLTranslationManager())
	beforeWrite := getCalls.Load()
	if _, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID, SQL: "DELETE FROM records", Type: 1, BatchSize: 10,
	}); err == nil {
		t.Fatal("FetchQuery() accepted an S3 JSON write")
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection fetched the S3 JSON object: GET calls before=%d after=%d", beforeWrite, afterWrite)
	}
	if got := getCalls.Load(); got != 3 {
		t.Fatalf("queries downloaded JSON objects %d times, want one GET for each form", got)
	}
}
