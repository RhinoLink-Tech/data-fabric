package object_storage_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestOSSParquetProtocolFixture(t *testing.T) {
	data := minIOParquetContractFixture(t)
	var getCalls atomic.Int64
	var holdGet atomic.Bool
	blockedGets := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-bucket/contract/records.parquet" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", "\"oss-parquet-fixture\"")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			getCalls.Add(1)
			if holdGet.Load() {
				select {
				case blockedGets <- struct{}{}:
				default:
				}
				<-r.Context().Done()
				return
			}
			_, _ = w.Write(data)
		default:
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	const objectKey = "contract/records.parquet"
	ds := &model.DataSource{
		ID: "oss-parquet-protocol-fixture", Type: model.DatabaseTypeAlibabaOSSParquet,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: "test-bucket", Username: "test-access-key", Password: "test-secret",
			AdditionalProps: map[string]interface{}{
				"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
				"force_path_style": true, "batch_size": 2,
			},
		},
	}

	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&ds.Config, ds.Type); err != nil {
		t.Fatalf("validate OSS Parquet config: %v", err)
	}
	invalidConfig := ds.Config
	invalidConfig.AdditionalProps = map[string]interface{}{"object_key": objectKey}
	if err := checker.ValidateDataSourceConfiguration(&invalidConfig, ds.Type); err == nil {
		t.Fatal("OSS Parquet config validation accepted a missing endpoint")
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &ds.Config, ds.Type)
	if err != nil {
		t.Fatalf("check OSS Parquet connectivity: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("OSS Parquet health status = %q (message: %s)", health.Status, health.Message)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: ds})
	tables, err := extractor.ListTables(context.Background(), ds.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list OSS Parquet tables = %+v, error = %v", tables, err)
	}
	table, err := extractor.ExtractTableSchema(context.Background(), ds.ID, "records")
	if err != nil {
		t.Fatalf("describe OSS Parquet table: %v", err)
	}
	if len(table.Columns) != 3 || table.Columns[0].Name != "id" || table.Columns[0].Type != "BIGINT" ||
		table.Columns[1].Name != "name" || table.Columns[1].Type != "VARCHAR" ||
		table.Columns[2].Name != "active" || table.Columns[2].Type != "BOOLEAN" {
		t.Fatalf("OSS Parquet columns = %+v", table.Columns)
	}

	db, err := pool.GetConnection(context.Background(), ds)
	if err != nil {
		t.Fatalf("open OSS Parquet database/sql adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD configured OSS object: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT name AS person, id FROM records WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query OSS Parquet object: %v", err)
	}
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("read OSS query columns: %v", err)
	}
	if got := strings.Join(columns, ","); got != "person,id" {
		t.Fatalf("OSS query columns = %q, want person,id", got)
	}
	if !rows.Next() {
		t.Fatalf("expected one OSS Parquet row, rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan OSS Parquet row: %v", err)
	}
	if person != "Ada" || id != 1 {
		t.Fatalf("OSS Parquet row = (%q, %d), want (Ada, 1)", person, id)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("OSS Parquet query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("count OSS Parquet rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("OSS active row count = %d, want 2", count)
	}

	ossClient, err := object_storage.NewOSSClient(context.Background(), &object_storage.OSSConfig{
		Endpoint: server.URL, AccessKeyID: "test-access-key", AccessKeySecret: "test-secret",
		BucketName: "test-bucket", ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("create OSS protocol client for cancellation: %v", err)
	}
	holdGet.Store(true)
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelResult := make(chan error, 1)
	go func() {
		_, err := ossClient.GetObject(cancelCtx, objectKey)
		cancelResult <- err
	}()
	select {
	case <-blockedGets:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("OSS GET did not reach the protocol fixture")
	}
	cancel()
	select {
	case err := <-cancelResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled OSS GET error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OSS GET did not stop after context cancellation")
	}
	holdGet.Store(false)

	beforeWrite := getCalls.Load()
	if _, err := db.ExecContext(context.Background(), "DELETE FROM records"); err == nil {
		t.Fatal("OSS Parquet adapter accepted a write")
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection fetched the object: before=%d after=%d", beforeWrite, afterWrite)
	}
	if getCalls.Load() == 0 {
		t.Fatal("OSS Parquet protocol fixture observed no object reads")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close OSS Parquet adapter: %v", err)
	}
}
