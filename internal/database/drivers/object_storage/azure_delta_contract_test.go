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

func TestAzureDeltaProtocolFixture(t *testing.T) {
	data := minIOParquetContractFixture(t)
	const (
		container = "test-container"
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
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorized := r.Header.Get("Authorization") != "" || r.URL.Query().Get("sig") == "fixture"
		if !authorized {
			http.Error(w, "missing Azure authentication", http.StatusForbidden)
			return
		}
		if r.URL.Query().Get("comp") == "list" {
			if r.URL.Query().Get("prefix") != tablePath+"/_delta_log/" {
				http.Error(w, "unexpected Azure list prefix", http.StatusBadRequest)
				return
			}
			xml := fmt.Sprintf(
				"<?xml version=\"1.0\" encoding=\"utf-8\"?><EnumerationResults ServiceEndpoint=\"%s/\" ContainerName=\"%s\"><Prefix>%s</Prefix><MaxResults>1000</MaxResults><Blobs><Blob><Name>%s</Name><Properties><Creation-Time>2026-10-29T00:00:00Z</Creation-Time><Last-Modified>2026-10-29T00:00:00Z</Last-Modified><Etag>&quot;azure-delta-fixture&quot;</Etag><Content-Length>%d</Content-Length><Content-Type>application/json</Content-Type><BlobType>BlockBlob</BlobType><LeaseStatus>unlocked</LeaseStatus></Properties></Blob></Blobs><NextMarker></NextMarker></EnumerationResults>",
				server.URL, container, tablePath+"/_delta_log/", logKey, len(logBody),
			)
			w.Header().Set("Content-Type", "application/xml")
			w.Header().Set("Content-Length", strconv.Itoa(len(xml)))
			w.Header().Set("x-ms-version", "2023-11-03")
			_, _ = w.Write([]byte(xml))
			return
		}

		key := strings.TrimPrefix(r.URL.Path, "/"+container+"/")
		var content []byte
		switch key {
		case logKey:
			getCalls.Add(1)
			content = []byte(logBody)
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
			content = data
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", "\"azure-delta-fixture\"")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		w.Header().Set("x-ms-blob-type", "BlockBlob")
		w.Header().Set("x-ms-request-id", "fixture-request")
		w.Header().Set("x-ms-version", "2023-11-03")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		start, end := int64(0), int64(len(content))-1
		if value := r.Header.Get("Range"); value != "" {
			rangeValue := strings.TrimPrefix(value, "bytes=")
			startText, endText, found := strings.Cut(rangeValue, "-")
			startValue, startErr := strconv.ParseInt(startText, 10, 64)
			endValue, endErr := strconv.ParseInt(endText, 10, 64)
			if !found || startErr != nil || endErr != nil || startValue < 0 || endValue < startValue || startValue >= int64(len(content)) {
				http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
				return
			}
			start, end = startValue, endValue
			if end >= int64(len(content)) {
				end = int64(len(content)) - 1
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(content)))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.WriteHeader(http.StatusPartialContent)
		}
		_, _ = w.Write(content[start : end+1])
	}))
	defer server.Close()

	accountKey := base64.StdEncoding.EncodeToString([]byte("fixture-account-key"))
	source := &model.DataSource{
		ID: "azure-delta-protocol-fixture", Type: model.DatabaseTypeAzureDelta,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: tablePath, Database: container, Username: "testaccount", Password: accountKey,
			AdditionalProps: map[string]interface{}{
				"table_path": tablePath, "table_name": "events", "endpoint_url": server.URL,
				"batch_size": 2,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate Azure Delta config: %v", err)
	}
	invalid := source.Config
	invalid.Password = ""
	if err := checker.ValidateDataSourceConfiguration(&invalid, source.Type); err == nil {
		t.Fatal("Azure Delta config validation accepted missing account key and SAS credentials")
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("Azure Delta health = %+v, error = %v", health, err)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(context.Background(), source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "events" {
		t.Fatalf("list Azure Delta tables = %+v, error = %v", tables, err)
	}
	schema, err := extractor.ExtractTableSchema(context.Background(), source.ID, "events")
	if err != nil {
		t.Fatalf("describe Azure Delta table: %v", err)
	}
	if len(schema.Columns) != 3 || schema.Columns[0].Name != "id" || schema.Columns[1].Name != "name" || schema.Columns[2].Name != "active" {
		t.Fatalf("Azure Delta columns = %+v", schema.Columns)
	}

	db, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("open Azure Delta SQL adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("resolve Azure Delta snapshot: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT name AS person, id FROM events WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query Azure Delta table: %v", err)
	}
	if got, _ := rows.Columns(); strings.Join(got, ",") != "person,id" {
		t.Fatalf("Azure Delta columns = %v, want person,id", got)
	}
	if !rows.Next() {
		t.Fatalf("expected one Azure Delta row, rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan Azure Delta row: %v", err)
	}
	if person != "Ada" || id != 1 {
		t.Fatalf("Azure Delta row = (%q, %d), want (Ada, 1)", person, id)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("Azure Delta query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("count Azure Delta rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("Azure Delta count = %d, want 2", count)
	}

	sasClient, err := object_storage.NewAzureBlobClient(context.Background(), &object_storage.AzureBlobConfig{
		AccountName: "testaccount", ContainerName: container, SASToken: "sv=2023-11-03&sig=fixture", Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("create Azure SAS protocol client: %v", err)
	}
	if _, err := sasClient.GetBlob(context.Background(), logKey); err != nil {
		t.Fatalf("download Delta log using SAS query token: %v", err)
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
		t.Fatal("Azure Delta data read did not reach the protocol fixture")
	}
	cancel()
	select {
	case err := <-cancelResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled Azure Delta query error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Azure Delta query did not stop after context cancellation")
	}
	holdDataGet.Store(false)

	beforeWrite := getCalls.Load()
	if _, err := db.ExecContext(context.Background(), "DELETE FROM events"); err == nil {
		t.Fatal("Azure Delta adapter accepted a write")
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection accessed Azure blobs: before=%d after=%d", beforeWrite, afterWrite)
	}
}
