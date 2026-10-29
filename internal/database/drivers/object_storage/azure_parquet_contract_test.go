package object_storage_test

import (
	"context"
	"encoding/base64"
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
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestAzureBlobParquetProtocolFixture(t *testing.T) {
	data := minIOParquetContractFixture(t)
	var getCalls atomic.Int64
	var holdGet atomic.Bool
	blockedGets := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-container/contract/records.parquet" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") == "" && r.URL.Query().Get("sig") != "fixture" {
			http.Error(w, "missing Azure authentication", http.StatusForbidden)
			return
		}
		w.Header().Set("ETag", "\"azure-parquet-fixture\"")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("x-ms-blob-type", "BlockBlob")
		w.Header().Set("x-ms-request-id", "fixture-request")
		w.Header().Set("x-ms-version", "2023-11-03")
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

	const objectKey = "contract/records.parquet"
	accountKey := base64.StdEncoding.EncodeToString([]byte("fixture-account-key"))
	ds := &model.DataSource{
		ID: "azure-blob-parquet-protocol-fixture", Type: model.DatabaseTypeAzureBlobParquet,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: "test-container", Username: "testaccount", Password: accountKey,
			AdditionalProps: map[string]interface{}{
				"object_key": objectKey, "table_name": "records", "endpoint_url": server.URL,
				"batch_size": 2,
			},
		},
	}

	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&ds.Config, ds.Type); err != nil {
		t.Fatalf("validate Azure Blob Parquet config: %v", err)
	}
	invalidConfig := ds.Config
	invalidConfig.Password = ""
	if err := checker.ValidateDataSourceConfiguration(&invalidConfig, ds.Type); err == nil {
		t.Fatal("Azure Blob Parquet validation accepted missing key and SAS credentials")
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &ds.Config, ds.Type)
	if err != nil {
		t.Fatalf("check Azure Blob Parquet connectivity: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("Azure Blob Parquet health status = %q (message: %s)", health.Status, health.Message)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: ds})
	tables, err := extractor.ListTables(context.Background(), ds.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list Azure Blob Parquet tables = %+v, error = %v", tables, err)
	}
	table, err := extractor.ExtractTableSchema(context.Background(), ds.ID, "records")
	if err != nil {
		t.Fatalf("describe Azure Blob Parquet table: %v", err)
	}
	if len(table.Columns) != 3 || table.Columns[0].Name != "id" || table.Columns[0].Type != "BIGINT" ||
		table.Columns[1].Name != "name" || table.Columns[1].Type != "VARCHAR" ||
		table.Columns[2].Name != "active" || table.Columns[2].Type != "BOOLEAN" {
		t.Fatalf("Azure Blob Parquet columns = %+v", table.Columns)
	}

	db, err := pool.GetConnection(context.Background(), ds)
	if err != nil {
		t.Fatalf("open Azure Blob Parquet database/sql adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("HEAD configured Azure blob: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT name AS person, id FROM records WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query Azure Blob Parquet object: %v", err)
	}
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("read Azure Blob query columns: %v", err)
	}
	if got := strings.Join(columns, ","); got != "person,id" {
		t.Fatalf("Azure Blob query columns = %q, want person,id", got)
	}
	if !rows.Next() {
		t.Fatalf("expected one Azure Blob Parquet row, rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan Azure Blob Parquet row: %v", err)
	}
	if person != "Ada" || id != 1 {
		t.Fatalf("Azure Blob Parquet row = (%q, %d), want (Ada, 1)", person, id)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("Azure Blob Parquet query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("count Azure Blob Parquet rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("Azure Blob active row count = %d, want 2", count)
	}

	sasClient, err := object_storage.NewAzureBlobClient(context.Background(), &object_storage.AzureBlobConfig{
		AccountName: "testaccount", ContainerName: "test-container",
		SASToken: "sv=2023-11-03&sig=fixture", Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("create Azure SAS protocol client: %v", err)
	}
	if _, err := sasClient.GetBlob(context.Background(), objectKey); err != nil {
		t.Fatalf("download blob using SAS query token: %v", err)
	}

	sharedKeyClient, err := object_storage.NewAzureBlobClient(context.Background(), &object_storage.AzureBlobConfig{
		AccountName: "testaccount", AccountKey: accountKey,
		ContainerName: "test-container", Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("create Azure shared-key client for cancellation: %v", err)
	}
	holdGet.Store(true)
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelResult := make(chan error, 1)
	go func() {
		_, err := sharedKeyClient.GetBlob(cancelCtx, objectKey)
		cancelResult <- err
	}()
	select {
	case <-blockedGets:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Azure GET did not reach the protocol fixture")
	}
	cancel()
	select {
	case err := <-cancelResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled Azure GET error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Azure GET did not stop after context cancellation")
	}
	holdGet.Store(false)

	beforeWrite := getCalls.Load()
	if _, err := db.ExecContext(context.Background(), "DELETE FROM records"); err == nil {
		t.Fatal("Azure Blob Parquet adapter accepted a write")
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection fetched the blob: before=%d after=%d", beforeWrite, afterWrite)
	}
	if getCalls.Load() == 0 {
		t.Fatal("Azure Blob protocol fixture observed no object reads")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close Azure Blob Parquet adapter: %v", err)
	}
}

func TestAzureParquetTypeAliasSharesBlobSQLPath(t *testing.T) {
	data := minIOParquetContractFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-container/contract/records.parquet" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("sig") != "fixture" {
			http.Error(w, "missing SAS query token", http.StatusForbidden)
			return
		}
		w.Header().Set("ETag", "\"azure-parquet-alias\"")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("x-ms-blob-type", "BlockBlob")
		w.Header().Set("x-ms-request-id", "fixture-request")
		w.Header().Set("x-ms-version", "2023-11-03")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()

	ds := &model.DataSource{
		ID: "azure-parquet-alias-protocol-fixture", Type: model.DatabaseTypeAzureParquet,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: "contract/records.parquet", Database: "test-container",
			Username: "testaccount",
			AdditionalProps: map[string]interface{}{
				"object_key": "contract/records.parquet", "table_name": "records",
				"endpoint_url": server.URL, "sas_token": "sv=2023-11-03&sig=fixture", "batch_size": 2,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	db, err := pool.GetConnection(context.Background(), ds)
	if err != nil {
		t.Fatalf("open Azure Parquet alias adapter: %v", err)
	}
	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records").Scan(&count); err != nil {
		t.Fatalf("query Azure Parquet alias: %v", err)
	}
	if count != 3 {
		t.Fatalf("Azure Parquet alias row count = %d, want 3", count)
	}
}
