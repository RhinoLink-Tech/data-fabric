package object_storage_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestMinIODeltaProtocolFixture(t *testing.T) {
	data := minIOParquetContractFixture(t)
	const (
		tablePath = "delta/events"
		logKey    = tablePath + "/_delta_log/00000000000000000000.json"
		dataKey   = tablePath + "/part-00000.parquet"
	)
	logBody := fmt.Sprintf(
		"{\"protocol\":{\"minReaderVersion\":1,\"minWriterVersion\":2}}\n"+
			"{\"metaData\":{\"id\":\"fixture\",\"format\":{\"provider\":\"parquet\",\"options\":{}},\"schemaString\":\"{}\",\"partitionColumns\":[],\"configuration\":{}}}\n"+
			"{\"add\":{\"path\":\"part-00000.parquet\",\"size\":%d,\"partitionValues\":{},\"modificationTime\":1,\"dataChange\":true}}\n",
		len(data),
	)
	var getCalls atomic.Int64
	var holdDataGet atomic.Bool
	blockedDataGets := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if prefix := r.URL.Query().Get("prefix"); prefix != "" {
			if prefix != tablePath+"/_delta_log/" {
				http.Error(w, "unexpected list prefix", http.StatusBadRequest)
				return
			}
			xml := fmt.Sprintf("<ListBucketResult xmlns=\"http://s3.amazonaws.com/doc/2006-03-01/\"><Name>test-bucket</Name><Prefix>%s</Prefix><KeyCount>1</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated><Contents><Key>%s</Key><LastModified>2026-10-29T00:00:00.000Z</LastModified><ETag>\"fixture-etag\"</ETag><Size>%d</Size><StorageClass>STANDARD</StorageClass></Contents></ListBucketResult>", prefix, logKey, len(logBody))
			w.Header().Set("Content-Type", "application/xml")
			w.Header().Set("Content-Length", strconv.Itoa(len(xml)))
			_, _ = w.Write([]byte(xml))
			return
		}
		w.Header().Set("Last-Modified", time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC).Format(http.TimeFormat))
		key := strings.TrimPrefix(r.URL.Path, "/test-bucket/")
		switch key {
		case logKey:
			getCalls.Add(1)
			w.Header().Set("Content-Length", strconv.Itoa(len(logBody)))
			_, _ = w.Write([]byte(logBody))
		case dataKey:
			getCalls.Add(1)
			if holdDataGet.Load() {
				select {
				case blockedDataGets <- struct{}{}:
				default:
				}
				<-r.Context().Done()
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := &model.DataSource{
		ID: "minio-delta-protocol-fixture", Type: model.DatabaseTypeMinIODelta,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: tablePath, Database: "test-bucket", Username: "minio-access-key", Password: "minio-secret-key",
			AdditionalProps: map[string]interface{}{
				"table_path": tablePath, "table_name": "events", "endpoint_url": server.URL,
				"region": "us-east-1", "batch_size": 2,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate MinIO Delta config: %v", err)
	}
	invalid := source.Config
	invalid.Username = ""
	invalid.Password = ""
	if err := checker.ValidateDataSourceConfiguration(&invalid, source.Type); err == nil {
		t.Fatal("MinIO Delta config validation accepted missing credentials")
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("MinIO Delta health = %+v, error = %v", health, err)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(context.Background(), source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "events" {
		t.Fatalf("list MinIO Delta tables = %+v, error = %v", tables, err)
	}
	schema, err := extractor.ExtractTableSchema(context.Background(), source.ID, "events")
	if err != nil {
		t.Fatalf("describe MinIO Delta table: %v", err)
	}
	if len(schema.Columns) != 3 || schema.Columns[0].Name != "id" || schema.Columns[1].Name != "name" || schema.Columns[2].Name != "active" {
		t.Fatalf("MinIO Delta columns = %+v", schema.Columns)
	}

	db, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("open MinIO Delta SQL adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("resolve MinIO Delta snapshot: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT name AS person, id FROM events WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query MinIO Delta table: %v", err)
	}
	if got, _ := rows.Columns(); strings.Join(got, ",") != "person,id" {
		t.Fatalf("MinIO Delta columns = %v, want person,id", got)
	}
	if !rows.Next() {
		t.Fatalf("expected one MinIO Delta row, rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan MinIO Delta row: %v", err)
	}
	if person != "Ada" || id != 1 {
		t.Fatalf("MinIO Delta row = (%q, %d), want (Ada, 1)", person, id)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("MinIO Delta query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("count MinIO Delta rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("MinIO Delta count = %d, want 2", count)
	}

	holdDataGet.Store(true)
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelResult := make(chan error, 1)
	go func() {
		_, err := db.QueryContext(cancelCtx, "SELECT id FROM events")
		cancelResult <- err
	}()
	select {
	case <-blockedDataGets:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("MinIO Delta data read did not reach the protocol fixture")
	}
	cancel()
	select {
	case err := <-cancelResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled MinIO Delta query error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("MinIO Delta query did not stop after cancellation")
	}
	holdDataGet.Store(false)

	beforeWrite := getCalls.Load()
	if _, err := db.ExecContext(context.Background(), "DELETE FROM events"); err == nil {
		t.Fatal("MinIO Delta adapter accepted a write")
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection accessed MinIO objects: before=%d after=%d", beforeWrite, afterWrite)
	}
}
