package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/writer"
)

type minIOParquetQueryTestRow struct {
	ID     int64  `parquet:"name=id, type=INT64"`
	Name   string `parquet:"name=name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Active bool   `parquet:"name=active, type=BOOLEAN"`
}

func minIOParquetQueryTestData(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	parquetWriter, err := writer.NewParquetWriterFromWriter(&output, new(minIOParquetQueryTestRow), 1)
	if err != nil {
		t.Fatalf("create Parquet writer: %v", err)
	}
	parquetWriter.CompressionType = parquet.CompressionCodec_UNCOMPRESSED
	for _, row := range []minIOParquetQueryTestRow{
		{ID: 1, Name: "Ada", Active: true},
		{ID: 2, Name: "Lin", Active: false},
		{ID: 3, Name: "Grace", Active: true},
	} {
		if err := parquetWriter.Write(row); err != nil {
			t.Fatalf("write Parquet row: %v", err)
		}
	}
	if err := parquetWriter.WriteStop(); err != nil {
		t.Fatalf("finish Parquet writer: %v", err)
	}
	return append([]byte(nil), output.Bytes()...)
}

func TestFetchQueryRoutesMinIOParquetThroughGateway(t *testing.T) {
	data := minIOParquetQueryTestData(t)
	var getCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo-bucket/data.parquet" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", `"minio-parquet-fixture"`)
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
		ID: "minio-parquet-query-service-test", Type: model.DatabaseTypeMinIOParquet, Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: "data.parquet", Database: "demo-bucket", Username: "minio-access-key", Password: "minio-secret-key",
			AdditionalProps: map[string]interface{}{
				"object_key": "data.parquet", "table_name": "records", "region": "us-east-1", "endpoint_url": server.URL,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate MinIO Parquet source config: %v", err)
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
		t.Fatalf("response total=%d entries=%d next=%q, want first of two rows", response.TotalCount, len(response.Entries), response.NextURI)
	}
	if len(response.Columns) != 2 || response.Columns[0].Name != "id" || response.Columns[1].Name != "name" {
		t.Fatalf("response columns = %+v, want id,name", response.Columns)
	}
	assertS3CSVServiceRows(t, response.Entries, []int64{3}, []string{"Grace"})
	next, err := queryService.FetchNextBatch(context.Background(), nil, response.QueryID, response.Slug, response.Token, 1)
	if err != nil {
		t.Fatalf("FetchNextBatch() error = %v", err)
	}
	if next.TotalCount != 2 || len(next.Entries) != 1 || next.NextURI != "" {
		t.Fatalf("next response total=%d entries=%d next=%q, want final row", next.TotalCount, len(next.Entries), next.NextURI)
	}
	assertS3CSVServiceRows(t, next.Entries, []int64{1}, []string{"Ada"})

	beforeWrite := getCalls.Load()
	if _, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID, SQL: "DELETE FROM records", Type: 1, BatchSize: 10,
	}); err == nil {
		t.Fatal("FetchQuery() accepted a MinIO Parquet write")
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection accessed the MinIO object: before=%d after=%d", beforeWrite, afterWrite)
	}
	if getCalls.Load() == 0 || strings.TrimSpace(response.QueryID) == "" {
		t.Fatalf("expected the query path to read the object and return a query id; get calls=%d, response=%+v", getCalls.Load(), response)
	}
}
