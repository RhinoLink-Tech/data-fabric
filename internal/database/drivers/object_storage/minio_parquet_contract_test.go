package object_storage_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/writer"
)

type minIOParquetContractRow struct {
	ID     int64  `parquet:"name=id, type=INT64"`
	Name   string `parquet:"name=name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Active bool   `parquet:"name=active, type=BOOLEAN"`
}

func minIOParquetContractFixture(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	parquetWriter, err := writer.NewParquetWriterFromWriter(&output, new(minIOParquetContractRow), 1)
	if err != nil {
		t.Fatalf("create contract Parquet writer: %v", err)
	}
	parquetWriter.CompressionType = parquet.CompressionCodec_UNCOMPRESSED
	for _, row := range []minIOParquetContractRow{
		{ID: 1, Name: "Ada", Active: true},
		{ID: 2, Name: "Lin", Active: false},
		{ID: 3, Name: "Grace", Active: true},
	} {
		if err := parquetWriter.Write(row); err != nil {
			t.Fatalf("write contract Parquet row: %v", err)
		}
	}
	if err := parquetWriter.WriteStop(); err != nil {
		t.Fatalf("finish contract Parquet writer: %v", err)
	}
	return append([]byte(nil), output.Bytes()...)
}

func TestMinIOParquetContract(t *testing.T) {
	endpoint := os.Getenv("DATA_FABRIC_MINIO_CONTRACT_ENDPOINT")
	if endpoint == "" {
		t.Skip("set DATA_FABRIC_MINIO_CONTRACT_ENDPOINT to run the live MinIO Parquet contract")
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

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	const objectKey = "contract/records.parquet"
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
	if err := client.PutObject(ctx, objectKey, minIOParquetContractFixture(t), "application/vnd.apache.parquet"); err != nil {
		t.Fatalf("upload contract Parquet through MinIO: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.DeleteObject(cleanupCtx, objectKey); err != nil {
			t.Errorf("delete MinIO contract Parquet object: %v", err)
		}
	})

	ds := &model.DataSource{
		ID: "minio-parquet-contract", Type: model.DatabaseTypeMinIOParquet,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: bucket, Username: accessKey, Password: secretKey,
			AdditionalProps: map[string]interface{}{
				"region": region, "object_key": objectKey, "table_name": "records", "endpoint_url": endpoint,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: ds})
	tables, err := extractor.ListTables(ctx, ds.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list MinIO Parquet tables = %+v, error = %v", tables, err)
	}
	table, err := extractor.ExtractTableSchema(ctx, ds.ID, "records")
	if err != nil {
		t.Fatalf("describe MinIO Parquet table: %v", err)
	}
	if len(table.Columns) != 3 || table.Columns[0].Name != "id" || table.Columns[0].Type != "BIGINT" ||
		table.Columns[1].Name != "name" || table.Columns[1].Type != "VARCHAR" ||
		table.Columns[2].Name != "active" || table.Columns[2].Type != "BOOLEAN" {
		t.Fatalf("MinIO Parquet columns = %+v", table.Columns)
	}
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&ds.Config, ds.Type); err != nil {
		t.Fatalf("validate MinIO Parquet source config: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(ctx, &ds.Config, ds.Type)
	if err != nil {
		t.Fatalf("check MinIO Parquet connectivity: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("connectivity status = %q, want healthy (message: %s)", health.Status, health.Message)
	}

	db, err := pool.GetConnection(ctx, ds)
	if err != nil {
		t.Fatalf("open configured MinIO Parquet source: %v", err)
	}
	rows, err := db.QueryContext(ctx, "SELECT name AS person, id FROM records WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query configured Parquet through database/sql: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("expected one paginated result, rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan queried values: %v", err)
	}
	if person != "Ada" || id != 1 {
		t.Fatalf("row = (%q, %d), want (Ada, 1)", person, id)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra row or iteration error: %v", rows.Err())
	}
	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("query MinIO Parquet count: %v", err)
	}
	if count != 2 {
		t.Fatalf("active row count = %d, want 2", count)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM records"); err == nil || !strings.Contains(err.Error(), "read-only SELECT") {
		t.Fatalf("write rejection error = %v, want read-only SELECT rejection", err)
	}

	t.Logf("MinIO %s (%s) Parquet contract passed", minioContractVersion, minioContractCommit)
}
