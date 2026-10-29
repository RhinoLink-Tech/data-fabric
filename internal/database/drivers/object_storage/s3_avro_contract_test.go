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
	"github.com/linkedin/goavro/v2"
)

const s3AvroContractSchema = "{\"type\":\"record\",\"name\":\"Record\",\"fields\":[{\"name\":\"id\",\"type\":\"long\"},{\"name\":\"name\",\"type\":[\"null\",\"string\"],\"default\":null},{\"name\":\"active\",\"type\":\"boolean\"}]}"

func s3AvroContractFixture(t *testing.T) []byte {
	t.Helper()
	codec, err := goavro.NewCodec(s3AvroContractSchema)
	if err != nil {
		t.Fatalf("create Avro contract schema: %v", err)
	}
	var output bytes.Buffer
	writer, err := goavro.NewOCFWriter(goavro.OCFConfig{
		W: &output, Codec: codec, CompressionName: "null",
		SyncMarker: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
	})
	if err != nil {
		t.Fatalf("create Avro OCF fixture writer: %v", err)
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

func TestS3AvroContract(t *testing.T) {
	endpoint := os.Getenv("DATA_FABRIC_S3_CONTRACT_ENDPOINT")
	if endpoint == "" {
		t.Skip("set DATA_FABRIC_S3_CONTRACT_ENDPOINT to run the live S3-compatible Avro contract")
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
			t.Fatalf("%s is required when the live S3 contract endpoint is set", name)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	const objectKey = "contract/records.avro"
	client, err := object_storage.NewS3Client(ctx, &object_storage.S3Config{
		Region: region, Bucket: bucket, AccessKey: accessKey, SecretKey: secretKey,
		EndpointURL: endpoint, ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("create S3 client: %v", err)
	}
	if err := client.PutObject(ctx, objectKey, s3AvroContractFixture(t), "avro/avro"); err != nil {
		t.Fatalf("upload contract Avro OCF through signed S3 request: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.DeleteObject(cleanupCtx, objectKey); err != nil {
			t.Errorf("delete contract Avro object: %v", err)
		}
	})

	ds := &model.DataSource{
		ID: "s3-avro-contract", Type: model.DatabaseTypeS3Avro,
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
	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: ds})
	tables, err := extractor.ListTables(ctx, ds.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list S3 Avro tables = %+v, error = %v", tables, err)
	}
	table, err := extractor.ExtractTableSchema(ctx, ds.ID, "records")
	if err != nil {
		t.Fatalf("describe S3 Avro table: %v", err)
	}
	if len(table.Columns) != 3 || table.Columns[0].Name != "id" || table.Columns[0].Type != "BIGINT" ||
		table.Columns[1].Name != "name" || table.Columns[1].Type != "VARCHAR" || !table.Columns[1].Nullable ||
		table.Columns[2].Name != "active" || table.Columns[2].Type != "BOOLEAN" {
		t.Fatalf("S3 Avro columns = %+v", table.Columns)
	}

	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&ds.Config, ds.Type); err != nil {
		t.Fatalf("validate S3 Avro source config: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(ctx, &ds.Config, ds.Type)
	if err != nil {
		t.Fatalf("check S3 Avro connectivity: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("connectivity status = %q, want healthy (message: %s)", health.Status, health.Message)
	}

	db, err := pool.GetConnection(ctx, ds)
	if err != nil {
		t.Fatalf("open configured S3 Avro source: %v", err)
	}
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("HEAD configured Avro object through database/sql: %v", err)
	}
	rows, err := db.QueryContext(ctx, "SELECT id, name FROM records WHERE active = true ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query configured Avro through database/sql: %v", err)
	}
	columns, err := rows.Columns()
	if err != nil {
		_ = rows.Close()
		t.Fatalf("read query columns: %v", err)
	}
	if got := strings.Join(columns, ","); got != "id,name" {
		_ = rows.Close()
		t.Fatalf("columns = %q, want id,name", got)
	}
	if !rows.Next() {
		_ = rows.Close()
		t.Fatalf("expected the second filtered Avro row, rows error: %v", rows.Err())
	}
	var id int64
	var name string
	if err := rows.Scan(&id, &name); err != nil {
		_ = rows.Close()
		t.Fatalf("scan queried values: %v", err)
	}
	if id != 1 || name != "Ada" {
		_ = rows.Close()
		t.Fatalf("row = (%d, %q), want (1, Ada)", id, name)
	}
	if rows.Next() || rows.Err() != nil {
		_ = rows.Close()
		t.Fatalf("unexpected extra row or iteration error: %v", rows.Err())
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("query S3 Avro count: %v", err)
	}
	if count != 2 {
		t.Fatalf("active row count = %d, want 2", count)
	}
	zeroRows, err := db.QueryContext(ctx, "SELECT * FROM records LIMIT 0")
	if err != nil {
		t.Fatalf("discover S3 Avro schema with LIMIT 0: %v", err)
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
	if err := zeroRows.Err(); err != nil {
		_ = zeroRows.Close()
		t.Fatalf("iterate LIMIT 0 rows: %v", err)
	}
	_ = zeroRows.Close()

	if _, err := db.ExecContext(ctx, "DELETE FROM records"); err == nil || !strings.Contains(err.Error(), "read-only SELECT") {
		t.Fatalf("write rejection error = %v, want read-only SELECT rejection", err)
	}
	t.Log("S3-compatible API operations and flat-record Avro OCF SQL contract passed against pinned Garage v2.4.1")
}
