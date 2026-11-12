package object_storage_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/google/uuid"
)

func TestS3DeltaProtocolFixture(t *testing.T) {
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
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=s3-access-key/") {
			http.Error(w, "missing S3 SigV4 authorization", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/test-bucket" && !strings.HasPrefix(r.URL.Path, "/test-bucket/") {
			http.Error(w, "expected path-style bucket addressing", http.StatusBadRequest)
			return
		}
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
		ID: "s3-delta-protocol-fixture", Type: model.DatabaseTypeS3Delta,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: tablePath, Database: "test-bucket", Username: "s3-access-key", Password: "s3-secret-key",
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
		t.Fatalf("validate S3 Delta config: %v", err)
	}
	invalid := source.Config
	invalid.Username = ""
	if err := checker.ValidateDataSourceConfiguration(&invalid, source.Type); err == nil {
		t.Fatal("S3 Delta config validation accepted a partial credential pair")
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("S3 Delta health = %+v, error = %v", health, err)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(context.Background(), source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "events" {
		t.Fatalf("list S3 Delta tables = %+v, error = %v", tables, err)
	}
	schema, err := extractor.ExtractTableSchema(context.Background(), source.ID, "events")
	if err != nil {
		t.Fatalf("describe S3 Delta table: %v", err)
	}
	if len(schema.Columns) != 3 || schema.Columns[0].Name != "id" || schema.Columns[1].Name != "name" || schema.Columns[2].Name != "active" {
		t.Fatalf("S3 Delta columns = %+v", schema.Columns)
	}

	db, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("open S3 Delta SQL adapter: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("resolve S3 Delta snapshot: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT name AS person, id FROM events WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query S3 Delta table: %v", err)
	}
	if got, _ := rows.Columns(); strings.Join(got, ",") != "person,id" {
		t.Fatalf("S3 Delta columns = %v, want person,id", got)
	}
	if !rows.Next() {
		t.Fatalf("expected one S3 Delta row, rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan S3 Delta row: %v", err)
	}
	if person != "Ada" || id != 1 {
		t.Fatalf("S3 Delta row = (%q, %d), want (Ada, 1)", person, id)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("S3 Delta query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("count S3 Delta rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("S3 Delta count = %d, want 2", count)
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
		t.Fatal("S3 Delta data read did not reach the protocol fixture")
	}
	cancel()
	select {
	case err := <-cancelResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled S3 Delta query error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("S3 Delta query did not stop after cancellation")
	}
	holdDataGet.Store(false)

	beforeWrite := getCalls.Load()
	if _, err := db.ExecContext(context.Background(), "DELETE FROM events"); err == nil {
		t.Fatal("S3 Delta adapter accepted a write")
	}
	if afterWrite := getCalls.Load(); afterWrite != beforeWrite {
		t.Fatalf("write rejection accessed S3 objects: before=%d after=%d", beforeWrite, afterWrite)
	}
}

func TestS3DeltaContract(t *testing.T) {
	endpoint := os.Getenv("DATA_FABRIC_S3_CONTRACT_ENDPOINT")
	if endpoint == "" {
		t.Skip("set DATA_FABRIC_S3_CONTRACT_ENDPOINT to run the live S3 Delta contract")
	}
	region := os.Getenv("DATA_FABRIC_S3_CONTRACT_REGION")
	accessKey := os.Getenv("DATA_FABRIC_S3_CONTRACT_ACCESS_KEY")
	secretKey := os.Getenv("DATA_FABRIC_S3_CONTRACT_SECRET_KEY")
	bucket := os.Getenv("DATA_FABRIC_S3_CONTRACT_BUCKET")
	for name, value := range map[string]string{
		"DATA_FABRIC_S3_CONTRACT_REGION":     region,
		"DATA_FABRIC_S3_CONTRACT_ACCESS_KEY": accessKey,
		"DATA_FABRIC_S3_CONTRACT_SECRET_KEY": secretKey,
		"DATA_FABRIC_S3_CONTRACT_BUCKET":     bucket,
	} {
		if value == "" {
			t.Fatalf("%s is required when the live S3 Delta contract endpoint is set", name)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := object_storage.NewS3Client(ctx, &object_storage.S3Config{
		Region: region, Bucket: bucket, AccessKey: accessKey, SecretKey: secretKey,
		EndpointURL: endpoint, ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("create S3 Delta contract client: %v", err)
	}
	tablePath := "contract/delta/" + uuid.NewString()
	logKey := tablePath + "/_delta_log/00000000000000000000.json"
	dataKey := tablePath + "/part-00000.parquet"
	data := minIOParquetContractFixture(t)
	logBody := fmt.Sprintf(
		"{\"protocol\":{\"minReaderVersion\":1,\"minWriterVersion\":2}}\n"+
			"{\"metaData\":{\"id\":\"garage-contract\",\"format\":{\"provider\":\"parquet\",\"options\":{}},\"schemaString\":\"{}\",\"partitionColumns\":[],\"configuration\":{}}}\n"+
			"{\"add\":{\"path\":\"part-00000.parquet\",\"size\":%d,\"partitionValues\":{},\"modificationTime\":1,\"dataChange\":true}}\n",
		len(data),
	)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, key := range []string{logKey, dataKey} {
			if err := client.DeleteObject(cleanupCtx, key); err != nil {
				t.Errorf("delete S3 Delta contract object %q: %v", key, err)
			}
		}
	})
	if err := client.PutObject(ctx, dataKey, data, "application/vnd.apache.parquet"); err != nil {
		t.Fatalf("upload S3 Delta Parquet file: %v", err)
	}
	if err := client.PutObject(ctx, logKey, []byte(logBody), "application/json"); err != nil {
		t.Fatalf("upload S3 Delta transaction log: %v", err)
	}

	source := &model.DataSource{
		ID: "s3-delta-contract", Type: model.DatabaseTypeS3Delta,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: tablePath, Database: bucket, Username: accessKey, Password: secretKey,
			AdditionalProps: map[string]interface{}{
				"table_path": tablePath, "table_name": "events", "endpoint_url": endpoint,
				"region": region, "batch_size": 2, "force_path_style": true,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate S3 Delta source: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(ctx, &source.Config, source.Type)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("S3 Delta health = %+v, error = %v", health, err)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(ctx, source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "events" {
		t.Fatalf("list S3 Delta tables = %+v, error = %v", tables, err)
	}
	schema, err := extractor.ExtractTableSchema(ctx, source.ID, "events")
	if err != nil {
		t.Fatalf("describe S3 Delta table: %v", err)
	}
	if len(schema.Columns) != 3 || schema.Columns[0].Name != "id" || schema.Columns[1].Name != "name" || schema.Columns[2].Name != "active" {
		t.Fatalf("S3 Delta columns = %+v", schema.Columns)
	}

	db, err := pool.GetConnection(ctx, source)
	if err != nil {
		t.Fatalf("open S3 Delta source: %v", err)
	}
	rows, err := db.QueryContext(ctx, "SELECT name AS person, id FROM events WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query S3 Delta table: %v", err)
	}
	if !rows.Next() {
		t.Fatalf("expected one S3 Delta row; rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan S3 Delta result: %v", err)
	}
	if person != "Ada" || id != 1 {
		t.Fatalf("S3 Delta row = (%q, %d), want (Ada, 1)", person, id)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("S3 Delta query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("count S3 Delta rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("S3 Delta count = %d, want 2", count)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM events"); err == nil {
		t.Fatal("S3 Delta adapter accepted a write")
	}
	t.Log("S3 Delta snapshot and read-only SQL contract passed against pinned Garage v2.4.1")
}
