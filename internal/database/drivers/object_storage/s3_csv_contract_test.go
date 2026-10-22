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

func TestS3CSVContract(t *testing.T) {
	endpoint := os.Getenv("DATA_FABRIC_S3_CONTRACT_ENDPOINT")
	if endpoint == "" {
		t.Skip("set DATA_FABRIC_S3_CONTRACT_ENDPOINT to run the live S3-compatible service contract")
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

	const objectKey = "contract/records.csv"
	client, err := object_storage.NewS3Client(ctx, &object_storage.S3Config{
		Region: region, Bucket: bucket, AccessKey: accessKey, SecretKey: secretKey,
		EndpointURL: endpoint, ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("create S3 client: %v", err)
	}
	if err := client.PutObject(ctx, objectKey, []byte(s3CSVFixture), "text/csv"); err != nil {
		t.Fatalf("upload contract CSV through signed S3 request: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.DeleteObject(cleanupCtx, objectKey); err != nil {
			t.Errorf("delete contract CSV object: %v", err)
		}
	})

	ds := &model.DataSource{
		ID:   "s3-csv-contract",
		Type: model.DatabaseTypeS3CSV,
		Config: model.DataSourceConfig{
			Host: objectKey, Database: bucket, Username: accessKey, Password: secretKey,
			AdditionalProps: map[string]interface{}{
				"region":           region,
				"object_key":       objectKey,
				"table_name":       "records",
				"endpoint_url":     endpoint,
				"force_path_style": true,
				"has_header":       true,
			},
		},
	}

	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&ds.Config, ds.Type); err != nil {
		t.Fatalf("validate S3 CSV source config: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(ctx, &ds.Config, ds.Type)
	if err != nil {
		t.Fatalf("list bucket using S3 connection test: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("connectivity status = %q, want healthy (message: %s)", health.Status, health.Message)
	}

	db, err := pool.GetConnection(ctx, ds)
	if err != nil {
		t.Fatalf("open configured S3 CSV source: %v", err)
	}
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("HEAD configured object through database/sql: %v", err)
	}
	rows, err := db.QueryContext(ctx, "SELECT name AS person, id FROM records WHERE active = true AND id >= 2 ORDER BY id DESC LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("GET and query configured CSV through database/sql: %v", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("read query columns: %v", err)
	}
	if got := strings.Join(columns, ","); got != "person,id" {
		t.Fatalf("columns = %q, want person,id", got)
	}
	if !rows.Next() {
		t.Fatalf("expected the second paginated row, rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan queried values: %v", err)
	}
	if person != "Cara" || id != 3 {
		t.Fatalf("row = (%q, %d), want (Cara, 3)", person, id)
	}
	if rows.Next() {
		t.Fatal("query returned more than one row for LIMIT 1")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate query rows: %v", err)
	}

	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("query CSV count: %v", err)
	}
	if count != 3 {
		t.Fatalf("active row count = %d, want 3", count)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM records"); err == nil || !strings.Contains(err.Error(), "read-only SELECT") {
		t.Fatalf("write rejection error = %v, want read-only SELECT rejection", err)
	}

	t.Log("S3-compatible API operations and SQL adapter contract passed against the pinned Garage v2.4.1 image")
}
