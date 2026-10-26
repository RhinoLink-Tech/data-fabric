package object_storage_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const minioContractVersion = "RELEASE.2025-04-22T22-12-26Z"
const minioContractCommit = "0d7408fc9969caf07de6a8c3a84f9fbb10a6739e"

func TestMinIOCSVContract(t *testing.T) {
	endpoint := os.Getenv("DATA_FABRIC_MINIO_CONTRACT_ENDPOINT")
	if endpoint == "" {
		t.Skip("set DATA_FABRIC_MINIO_CONTRACT_ENDPOINT to run the live MinIO CSV contract")
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
	const objectKey = "contract/records.csv"
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
	if err := client.PutObject(ctx, objectKey, []byte(s3CSVFixture), "text/csv"); err != nil {
		t.Fatalf("upload contract CSV through MinIO: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.DeleteObject(cleanupCtx, objectKey); err != nil {
			t.Errorf("delete MinIO contract CSV object: %v", err)
		}
	})

	ds := &model.DataSource{
		ID: "minio-csv-contract", Type: model.DatabaseTypeMinIOCSV,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: bucket, Username: accessKey, Password: secretKey,
			AdditionalProps: map[string]interface{}{
				"region": region, "object_key": objectKey, "table_name": "records",
				"endpoint_url": endpoint, "force_path_style": true, "has_header": true,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&ds.Config, ds.Type); err != nil {
		t.Fatalf("validate MinIO CSV source config: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(ctx, &ds.Config, ds.Type)
	if err != nil {
		t.Fatalf("check MinIO CSV connectivity: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("connectivity status = %q, want healthy (message: %s)", health.Status, health.Message)
	}

	db, err := pool.GetConnection(ctx, ds)
	if err != nil {
		t.Fatalf("open configured MinIO CSV source: %v", err)
	}
	rows, err := db.QueryContext(ctx, "SELECT name AS person, id FROM records WHERE active = true AND id >= 2 ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query configured CSV through database/sql: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("expected a paginated result, rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan queried values: %v", err)
	}
	if person != "Cara" || id != 3 {
		t.Fatalf("row = (%q, %d), want (Cara, 3)", person, id)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra row or iteration error: %v", rows.Err())
	}

	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("query MinIO CSV count: %v", err)
	}
	if count != 3 {
		t.Fatalf("active row count = %d, want 3", count)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM records"); err == nil || !strings.Contains(err.Error(), "read-only SELECT") {
		t.Fatalf("write rejection error = %v, want read-only SELECT rejection", err)
	}

	t.Logf("MinIO %s (%s) CSV contract passed", minioContractVersion, minioContractCommit)
}
