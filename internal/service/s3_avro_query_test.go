package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/linkedin/goavro/v2"
)

const s3AvroServiceSchema = "{\"type\":\"record\",\"name\":\"Record\",\"fields\":[{\"name\":\"id\",\"type\":\"long\"},{\"name\":\"name\",\"type\":[\"null\",\"string\"],\"default\":null},{\"name\":\"active\",\"type\":\"boolean\"}]}"

func s3AvroServiceFixture(t *testing.T) []byte {
	t.Helper()
	codec, err := goavro.NewCodec(s3AvroServiceSchema)
	if err != nil {
		t.Fatalf("create Avro schema: %v", err)
	}
	var output bytes.Buffer
	writer, err := goavro.NewOCFWriter(goavro.OCFConfig{
		W: &output, Codec: codec, CompressionName: "null",
		SyncMarker: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
	})
	if err != nil {
		t.Fatalf("create Avro OCF writer: %v", err)
	}
	records := []interface{}{
		map[string]interface{}{"id": int64(1), "name": goavro.Union("string", "Ada"), "active": true},
		map[string]interface{}{"id": int64(2), "name": nil, "active": false},
		map[string]interface{}{"id": int64(3), "name": goavro.Union("string", "Grace"), "active": true},
	}
	if err := writer.Append(records); err != nil {
		t.Fatalf("write Avro OCF fixture: %v", err)
	}
	return append([]byte(nil), output.Bytes()...)
}

func TestFetchQueryRoutesS3AvroThroughGateway(t *testing.T) {
	data := s3AvroServiceFixture(t)
	var getCalls atomic.Int64
	var etagVersion atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo-bucket/data.avro" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", "\"avro-fixture-"+strconv.Itoa(int(etagVersion.Load()))+"\"")
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

	source := &model.DataSource{
		ID: "s3-avro-query-service-test", Type: model.DatabaseTypeS3Avro, Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: "data.avro", Database: "demo-bucket", Username: "test-access-key", Password: "test-secret-key",
			AdditionalProps: map[string]interface{}{
				"object_key": "data.avro", "table_name": "records", "region": "us-east-1",
				"endpoint_url": server.URL, "force_path_style": true,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate S3 Avro source config: %v", err)
	}

	queryService := NewQueryService(federationSourceRepository{source: source}, pool, false, newTestSQLTranslationManager())
	response, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID,
		SQL:          "SELECT id, name FROM records WHERE active = true ORDER BY id DESC",
		Type:         2,
		BatchSize:    1,
	})
	if err != nil {
		t.Fatalf("FetchQuery() error = %v", err)
	}
	if response.TotalCount != 2 || len(response.Entries) != 1 || response.NextURI == "" {
		t.Fatalf("response total=%d entries=%d next=%q, want first of two Avro rows", response.TotalCount, len(response.Entries), response.NextURI)
	}
	if len(response.Columns) != 2 || response.Columns[0].Name != "id" || response.Columns[1].Name != "name" {
		t.Fatalf("response columns = %+v, want id,name", response.Columns)
	}
	entry := response.Entries[0]
	if entry == nil || len(*entry) != 2 {
		t.Fatalf("response entry = %#v, want two values", entry)
	}
	id, ok := (*entry)[0].(int64)
	if !ok || id != 3 || (*entry)[1] != "Grace" {
		t.Fatalf("response row = %#v, want [3 Grace]", *entry)
	}
	next, err := queryService.FetchNextBatch(context.Background(), nil, response.QueryID, response.Slug, response.Token, 1)
	if err != nil {
		t.Fatalf("FetchNextBatch() error = %v", err)
	}
	if len(next.Entries) != 1 || next.NextURI != "" {
		t.Fatalf("next response entries=%d next=%q, want final Avro row", len(next.Entries), next.NextURI)
	}
	nextID, ok := (*next.Entries[0])[0].(int64)
	if !ok || nextID != 1 || (*next.Entries[0])[1] != "Ada" {
		t.Fatalf("next row = %#v, want [1 Ada]", *next.Entries[0])
	}

	beforeWrite := getCalls.Load()
	if _, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID, SQL: "DELETE FROM records", Type: 1, BatchSize: 10,
	}); err == nil {
		t.Fatal("FetchQuery() accepted an S3 Avro write")
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection read Avro records: GET calls before=%d after=%d", beforeWrite, afterWrite)
	}
	if getCalls.Load() != 1 {
		t.Fatalf("one paginated query downloaded the OCF %d times, want once", getCalls.Load())
	}
	etagVersion.Add(1)
	if _, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID,
		SQL:          "SELECT id FROM records WHERE active = true",
		Type:         1,
		BatchSize:    10,
	}); err != nil {
		t.Fatalf("FetchQuery() after object ETag changes: %v", err)
	}
	if getCalls.Load() != 2 {
		t.Fatalf("changed object ETag caused %d total OCF downloads, want cache refresh to 2", getCalls.Load())
	}
}
