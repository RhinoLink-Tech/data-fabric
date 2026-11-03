package object_storage_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestMinIOAvroReadOnlySQLContract(t *testing.T) {
	avroData := s3AvroContractFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=minio-access/") {
			http.Error(w, "missing S3 request signature", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/analytics/contract/records.avro" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Type", "avro/avro")
		w.Header().Set("ETag", "\"avro-fixture\"")
		w.Header().Set("Last-Modified", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(http.TimeFormat))
		switch r.Method {
		case http.MethodHead:
			w.Header().Set("Content-Length", strconv.Itoa(len(avroData)))
		case http.MethodGet:
			start, end := int64(0), int64(len(avroData)-1)
			if rawRange := r.Header.Get("Range"); rawRange != "" {
				value, ok := strings.CutPrefix(rawRange, "bytes=")
				parts := strings.SplitN(value, "-", 2)
				if !ok || len(parts) != 2 {
					http.Error(w, "invalid byte range", http.StatusRequestedRangeNotSatisfiable)
					return
				}
				var err error
				start, err = strconv.ParseInt(parts[0], 10, 64)
				if err != nil {
					http.Error(w, "invalid range start", http.StatusRequestedRangeNotSatisfiable)
					return
				}
				end, err = strconv.ParseInt(parts[1], 10, 64)
				if err != nil || start < 0 || start >= int64(len(avroData)) || end < start {
					http.Error(w, "invalid byte range", http.StatusRequestedRangeNotSatisfiable)
					return
				}
				if end >= int64(len(avroData)) {
					end = int64(len(avroData)) - 1
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(avroData)))
				w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(avroData[start : end+1])
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(avroData)))
			_, _ = w.Write(avroData)
		default:
			http.Error(w, "unsupported S3 method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	source := &model.DataSource{
		ID: "minio-avro-local-fixture", Type: model.DatabaseTypeMinIOAvro,
		Config: model.DataSourceConfig{
			Host: "contract/records.avro", Database: "analytics",
			Username: "minio-access", Password: "minio-secret",
			AdditionalProps: map[string]interface{}{
				"object_key": "contract/records.avro", "table_name": "records",
				"endpoint_url": server.URL, "region": "us-east-1",
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate MinIO Avro source: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("MinIO Avro health = %+v, error = %v", health, err)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(context.Background(), source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list configured MinIO Avro table = %+v, error = %v", tables, err)
	}
	schema, err := extractor.ExtractTableSchema(context.Background(), source.ID, "records")
	if err != nil {
		t.Fatalf("describe configured MinIO Avro table: %v", err)
	}
	if len(schema.Columns) != 3 || schema.Columns[0].Name != "id" || schema.Columns[0].Type != "BIGINT" ||
		schema.Columns[1].Name != "name" || !schema.Columns[1].Nullable {
		t.Fatalf("MinIO Avro columns = %+v", schema.Columns)
	}

	db, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("open MinIO Avro SQL connection: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT id, name FROM records WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query configured MinIO Avro object: %v", err)
	}
	if !rows.Next() {
		t.Fatalf("expected filtered MinIO Avro row; rows error: %v", rows.Err())
	}
	var id int64
	var name string
	if err := rows.Scan(&id, &name); err != nil {
		t.Fatalf("scan MinIO Avro result: %v", err)
	}
	if id != 1 || name != "Ada" {
		t.Fatalf("MinIO Avro row = (%d, %q), want (1, Ada)", id, name)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("MinIO Avro query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("count MinIO Avro rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("MinIO Avro count = %d, want 2", count)
	}
	zeroRows, err := db.QueryContext(context.Background(), "SELECT * FROM records LIMIT 0")
	if err != nil {
		t.Fatalf("discover MinIO Avro schema with LIMIT 0: %v", err)
	}
	zeroColumns, err := zeroRows.Columns()
	if err != nil {
		_ = zeroRows.Close()
		t.Fatalf("read LIMIT 0 columns: %v", err)
	}
	if len(zeroColumns) != 3 || zeroRows.Next() {
		_ = zeroRows.Close()
		t.Fatalf("LIMIT 0 columns=%v; expected three columns and no rows", zeroColumns)
	}
	_ = zeroRows.Close()
	if _, err := db.ExecContext(context.Background(), "DELETE FROM records"); err == nil {
		t.Fatal("MinIO Avro source accepted a write")
	}
}

func TestMinIOAvroContract(t *testing.T) {
	endpoint := os.Getenv("DATA_FABRIC_MINIO_CONTRACT_ENDPOINT")
	if endpoint == "" {
		t.Skip("set DATA_FABRIC_MINIO_CONTRACT_ENDPOINT to run the live MinIO Avro contract")
	}
	region := os.Getenv("DATA_FABRIC_MINIO_CONTRACT_REGION")
	accessKey := os.Getenv("DATA_FABRIC_MINIO_CONTRACT_ACCESS_KEY")
	secretKey := os.Getenv("DATA_FABRIC_MINIO_CONTRACT_SECRET_KEY")
	bucket := os.Getenv("DATA_FABRIC_MINIO_CONTRACT_BUCKET")
	for name, value := range map[string]string{
		"DATA_FABRIC_MINIO_CONTRACT_REGION":     region,
		"DATA_FABRIC_MINIO_CONTRACT_ACCESS_KEY": accessKey,
		"DATA_FABRIC_MINIO_CONTRACT_SECRET_KEY": secretKey,
		"DATA_FABRIC_MINIO_CONTRACT_BUCKET":     bucket,
	} {
		if value == "" {
			t.Fatalf("%s is required when the live MinIO contract endpoint is set", name)
		}
	}
	if version := os.Getenv("DATA_FABRIC_MINIO_CONTRACT_VERSION"); version != minioContractVersion {
		t.Fatalf("MinIO contract version = %q, want %q", version, minioContractVersion)
	}
	if commit := os.Getenv("DATA_FABRIC_MINIO_CONTRACT_COMMIT"); commit != minioContractCommit {
		t.Fatalf("MinIO contract commit = %q, want %q", commit, minioContractCommit)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	const objectKey = "contract/records.avro"
	client, err := object_storage.NewS3Client(ctx, &object_storage.S3Config{
		Region: region, Bucket: bucket, AccessKey: accessKey, SecretKey: secretKey,
		EndpointURL: endpoint, ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("create MinIO-compatible client: %v", err)
	}
	if err := client.EnsureBucket(ctx); err != nil {
		t.Fatalf("create MinIO contract bucket: %v", err)
	}
	if err := client.PutObject(ctx, objectKey, s3AvroContractFixture(t), "avro/avro"); err != nil {
		t.Fatalf("upload contract Avro OCF through MinIO: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if err := client.DeleteObject(cleanupCtx, objectKey); err != nil {
			t.Errorf("delete MinIO Avro contract object: %v", err)
		}
	})

	ds := &model.DataSource{
		ID: "minio-avro-contract", Type: model.DatabaseTypeMinIOAvro,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: bucket, Username: accessKey, Password: secretKey,
			AdditionalProps: map[string]interface{}{
				"region": region, "object_key": objectKey, "table_name": "records",
				"endpoint_url": endpoint, "force_path_style": true,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&ds.Config, ds.Type); err != nil {
		t.Fatalf("validate MinIO Avro source: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(ctx, &ds.Config, ds.Type)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("MinIO Avro health = %+v, error = %v", health, err)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: ds})
	tables, err := extractor.ListTables(ctx, ds.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list MinIO Avro table = %+v, error = %v", tables, err)
	}
	schema, err := extractor.ExtractTableSchema(ctx, ds.ID, "records")
	if err != nil {
		t.Fatalf("describe MinIO Avro table: %v", err)
	}
	if len(schema.Columns) != 3 || schema.Columns[0].Name != "id" || schema.Columns[0].Type != "BIGINT" ||
		schema.Columns[1].Name != "name" || !schema.Columns[1].Nullable ||
		schema.Columns[2].Name != "active" || schema.Columns[2].Type != "BOOLEAN" {
		t.Fatalf("MinIO Avro columns = %+v", schema.Columns)
	}

	db, err := pool.GetConnection(ctx, ds)
	if err != nil {
		t.Fatalf("open MinIO Avro SQL adapter: %v", err)
	}
	rows, err := db.QueryContext(ctx, "SELECT id, name FROM records WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query MinIO Avro object: %v", err)
	}
	if !rows.Next() {
		_ = rows.Close()
		t.Fatalf("expected filtered MinIO Avro row; rows error: %v", rows.Err())
	}
	var id int64
	var name string
	if err := rows.Scan(&id, &name); err != nil {
		_ = rows.Close()
		t.Fatalf("scan MinIO Avro row: %v", err)
	}
	if id != 1 || name != "Ada" {
		_ = rows.Close()
		t.Fatalf("MinIO Avro row = (%d, %q), want (1, Ada)", id, name)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("MinIO Avro query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("count MinIO Avro rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("MinIO Avro count = %d, want 2", count)
	}
	zeroRows, err := db.QueryContext(ctx, "SELECT * FROM records LIMIT 0")
	if err != nil {
		t.Fatalf("discover MinIO Avro schema with LIMIT 0: %v", err)
	}
	zeroColumns, err := zeroRows.Columns()
	if err != nil || len(zeroColumns) != 3 || zeroRows.Next() {
		_ = zeroRows.Close()
		t.Fatalf("MinIO Avro LIMIT 0 columns=%v, error=%v", zeroColumns, err)
	}
	_ = zeroRows.Close()
	if _, err := db.ExecContext(ctx, "DELETE FROM records"); err == nil {
		t.Fatal("MinIO Avro source accepted a write")
	}
	t.Logf("MinIO %s (%s) Avro OCF contract passed", minioContractVersion, minioContractCommit)
}
