package object_storage_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func s3ORCContractFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "orc_split_elim.orc"))
	if err != nil {
		t.Fatalf("read ORC contract fixture: %v", err)
	}
	return data
}

func TestS3ORCContract(t *testing.T) {
	endpoint := os.Getenv("DATA_FABRIC_S3_CONTRACT_ENDPOINT")
	if endpoint == "" {
		t.Skip("set DATA_FABRIC_S3_CONTRACT_ENDPOINT to run the live S3-compatible ORC contract")
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

	const objectKey = "contract/records.orc"
	client, err := object_storage.NewS3Client(ctx, &object_storage.S3Config{
		Region: region, Bucket: bucket, AccessKey: accessKey, SecretKey: secretKey,
		EndpointURL: endpoint, ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("create S3 client: %v", err)
	}
	if err := client.PutObject(ctx, objectKey, s3ORCContractFixture(t), "application/vnd.apache.orc"); err != nil {
		t.Fatalf("upload contract ORC through signed S3 request: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.DeleteObject(cleanupCtx, objectKey); err != nil {
			t.Errorf("delete contract ORC object: %v", err)
		}
	})

	ds := &model.DataSource{
		ID: "s3-orc-contract", Type: model.DatabaseTypeS3ORC,
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
		t.Fatalf("list S3 ORC tables = %+v, error = %v", tables, err)
	}
	table, err := extractor.ExtractTableSchema(ctx, ds.ID, "records")
	if err != nil {
		t.Fatalf("describe S3 ORC table: %v", err)
	}
	if len(table.Columns) != 5 || table.Columns[0].Name != "userid" || table.Columns[0].Type != "BIGINT" ||
		table.Columns[1].Name != "string1" || table.Columns[1].Type != "VARCHAR" {
		t.Fatalf("S3 ORC columns = %+v", table.Columns)
	}

	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&ds.Config, ds.Type); err != nil {
		t.Fatalf("validate S3 ORC source config: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(ctx, &ds.Config, ds.Type)
	if err != nil {
		t.Fatalf("check S3 ORC connectivity: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("connectivity status = %q, want healthy (message: %s)", health.Status, health.Message)
	}

	db, err := pool.GetConnection(ctx, ds)
	if err != nil {
		t.Fatalf("open configured S3 ORC source: %v", err)
	}
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("HEAD configured ORC object through database/sql: %v", err)
	}
	rows, err := db.QueryContext(ctx, "SELECT userid, string1 FROM records WHERE userid = 2 ORDER BY string1 LIMIT 1")
	if err != nil {
		t.Fatalf("query configured ORC through database/sql: %v", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("read query columns: %v", err)
	}
	if got := strings.Join(columns, ","); got != "userid,string1" {
		t.Fatalf("columns = %q, want userid,string1", got)
	}
	if !rows.Next() {
		t.Fatalf("expected the filtered ORC row, rows error: %v", rows.Err())
	}
	var userID int64
	var value string
	if err := rows.Scan(&userID, &value); err != nil {
		t.Fatalf("scan queried values: %v", err)
	}
	if userID != 2 || value != "foo" {
		t.Fatalf("row = (%d, %q), want (2, foo)", userID, value)
	}
	if rows.Next() {
		t.Fatal("query returned more than one row for LIMIT 1")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate query rows: %v", err)
	}

	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM records").Scan(&count); err != nil {
		t.Fatalf("query S3 ORC count: %v", err)
	}
	if count != 25000 {
		t.Fatalf("row count = %d, want 25000", count)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM records"); err == nil || !strings.Contains(err.Error(), "read-only SELECT") {
		t.Fatalf("write rejection error = %v, want read-only SELECT rejection", err)
	}

	zeroRows, err := db.QueryContext(ctx, "SELECT * FROM records LIMIT 0")
	if err != nil {
		t.Fatalf("discover S3 ORC schema with LIMIT 0: %v", err)
	}
	zeroColumns, err := zeroRows.Columns()
	if err != nil {
		_ = zeroRows.Close()
		t.Fatalf("read LIMIT 0 columns: %v", err)
	}
	if len(zeroColumns) != 5 || zeroRows.Next() {
		_ = zeroRows.Close()
		t.Fatalf("LIMIT 0 columns=%v; expected five columns and no rows", zeroColumns)
	}
	if err := zeroRows.Err(); err != nil {
		_ = zeroRows.Close()
		t.Fatalf("iterate LIMIT 0 rows: %v", err)
	}
	_ = zeroRows.Close()

	t.Log("S3-compatible API operations and flat-schema ORC SQL contract passed against the pinned Garage v2.4.1 image")
}
