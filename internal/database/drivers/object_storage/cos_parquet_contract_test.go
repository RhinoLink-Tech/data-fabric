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

func TestCOSParquetProtocolFixture(t *testing.T) {
	data := minIOParquetContractFixture(t)
	var getCalls atomic.Int64
	var holdGet atomic.Bool
	blockedGets := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/contract/records.parquet" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "missing COS authorization header", http.StatusForbidden)
			return
		}
		w.Header().Set("ETag", "\"cos-parquet-fixture\"")
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
		ID: "cos-parquet-protocol-fixture", Type: model.DatabaseTypeTencentCOSParquet,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: "test-bucket-1250000000", Username: "test-secret-id", Password: "test-secret-key",
			AdditionalProps: map[string]interface{}{
				"object_key": objectKey, "table_name": "records", "region": "ap-guangzhou",
				"endpoint_url": server.URL, "batch_size": 2,
			},
		},
	}

	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&ds.Config, ds.Type); err != nil {
		t.Fatalf("validate COS Parquet config: %v", err)
	}
	invalidConfig := ds.Config
	invalidConfig.AdditionalProps = map[string]interface{}{"object_key": objectKey, "endpoint_url": server.URL}
	if err := checker.ValidateDataSourceConfiguration(&invalidConfig, ds.Type); err == nil {
		t.Fatal("COS Parquet config validation accepted a missing region")
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &ds.Config, ds.Type)
	if err != nil {
		t.Fatalf("check COS Parquet connectivity: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("COS Parquet health status = %q (message: %s)", health.Status, health.Message)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: ds})
	tables, err := extractor.ListTables(context.Background(), ds.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list COS Parquet tables = %+v, error = %v", tables, err)
	}
	table, err := extractor.ExtractTableSchema(context.Background(), ds.ID, "records")
	if err != nil {
		t.Fatalf("describe COS Parquet table: %v", err)
	}
	if len(table.Columns) != 3 || table.Columns[0].Name != "id" || table.Columns[0].Type != "BIGINT" ||
		table.Columns[1].Name != "name" || table.Columns[1].Type != "VARCHAR" ||
		table.Columns[2].Name != "active" || table.Columns[2].Type != "BOOLEAN" {
		t.Fatalf("COS Parquet columns = %+v", table.Columns)
	}

	db, err := pool.GetConnection(context.Background(), ds)
	if err != nil {
		t.Fatalf("open COS Parquet database/sql adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD configured COS object: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT name AS person, id FROM records WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query COS Parquet object: %v", err)
	}
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("read COS query columns: %v", err)
	}
	if got := strings.Join(columns, ","); got != "person,id" {
		t.Fatalf("COS query columns = %q, want person,id", got)
	}
	if !rows.Next() {
		t.Fatalf("expected one COS Parquet row, rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan COS Parquet row: %v", err)
	}
	if person != "Ada" || id != 1 {
		t.Fatalf("COS Parquet row = (%q, %d), want (Ada, 1)", person, id)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("COS Parquet query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("count COS Parquet rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("COS active row count = %d, want 2", count)
	}

	cosClient, err := object_storage.NewCOSClient(context.Background(), &object_storage.COSConfig{
		SecretID: "test-secret-id", SecretKey: "test-secret-key", Region: "ap-guangzhou",
		BucketName: "test-bucket-1250000000", EndpointURL: server.URL, HTTPS: false,
	})
	if err != nil {
		t.Fatalf("create COS protocol client for cancellation: %v", err)
	}
	holdGet.Store(true)
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelResult := make(chan error, 1)
	go func() {
		_, err := cosClient.GetObject(cancelCtx, objectKey)
		cancelResult <- err
	}()
	select {
	case <-blockedGets:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("COS GET did not reach the protocol fixture")
	}
	cancel()
	select {
	case err := <-cancelResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled COS GET error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("COS GET did not stop after context cancellation")
	}
	holdGet.Store(false)

	beforeWrite := getCalls.Load()
	if _, err := db.ExecContext(context.Background(), "DELETE FROM records"); err == nil {
		t.Fatal("COS Parquet adapter accepted a write")
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection fetched the object: before=%d after=%d", beforeWrite, afterWrite)
	}
	if getCalls.Load() == 0 {
		t.Fatal("COS Parquet protocol fixture observed no object reads")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close COS Parquet adapter: %v", err)
	}
}
