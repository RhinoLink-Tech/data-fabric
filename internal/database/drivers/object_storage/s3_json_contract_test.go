package object_storage_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestS3JSONContract(t *testing.T) {
	endpoint := os.Getenv("DATA_FABRIC_S3_CONTRACT_ENDPOINT")
	if endpoint == "" {
		t.Skip("set DATA_FABRIC_S3_CONTRACT_ENDPOINT to run the live S3-compatible JSON contract")
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := object_storage.NewS3Client(ctx, &object_storage.S3Config{
		Region: region, Bucket: bucket, AccessKey: accessKey, SecretKey: secretKey,
		EndpointURL: endpoint, ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("create S3 client: %v", err)
	}
	fixtures := []struct {
		name string
		key  string
		data string
		rows int64
	}{
		{name: "object", key: "contract/one.json", data: "{\"id\":3,\"name\":\"Grace\",\"active\":true,\"optional\":null}", rows: 1},
		{name: "array", key: "contract/array.json", data: "[{\"id\":1,\"name\":\"Ada\",\"active\":true,\"optional\":\"x\"},{\"id\":2,\"name\":\"Linus\",\"active\":false},{\"id\":3,\"name\":\"Grace\",\"active\":true,\"optional\":null}]", rows: 3},
		{name: "json-lines", key: "contract/records.ndjson", data: "{\"id\":1,\"name\":\"Ada\",\"active\":true,\"optional\":\"x\"}\n{\"id\":2,\"name\":\"Linus\",\"active\":false}\n{\"id\":3,\"name\":\"Grace\",\"active\":true,\"optional\":null}\n", rows: 3},
	}
	for _, fixture := range fixtures {
		fixture := fixture
		if err := client.PutObject(ctx, fixture.key, []byte(fixture.data), "application/json"); err != nil {
			t.Fatalf("upload %s fixture: %v", fixture.name, err)
		}
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			if err := client.DeleteObject(cleanupCtx, fixture.key); err != nil {
				t.Errorf("delete %s fixture: %v", fixture.name, err)
			}
		})
		ds := &model.DataSource{
			ID: "s3-json-contract-" + fixture.name, Type: model.DatabaseTypeS3JSON,
			Config: model.DataSourceConfig{
				Host: fixture.key, Database: bucket, Username: accessKey, Password: secretKey,
				AdditionalProps: map[string]interface{}{
					"region": region, "object_key": fixture.key, "table_name": "records",
					"endpoint_url": endpoint, "force_path_style": true,
				},
			},
		}
		pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
		t.Cleanup(func() { _ = pool.CloseAll() })
		extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: ds})
		tables, err := extractor.ListTables(ctx, ds.ID)
		if err != nil || len(tables) != 1 || tables[0].Name != "records" {
			t.Fatalf("%s list tables = %+v, error = %v", fixture.name, tables, err)
		}
		table, err := extractor.ExtractTableSchema(ctx, ds.ID, "records")
		if err != nil {
			t.Fatalf("%s describe table: %v", fixture.name, err)
		}
		columns := make(map[string]metadata.ColumnSchema, len(table.Columns))
		for _, column := range table.Columns {
			columns[column.Name] = column
		}
		if len(columns) != 4 || columns["id"].Type != "BIGINT" || columns["name"].Type != "VARCHAR" ||
			columns["active"].Type != "BOOLEAN" || !columns["optional"].Nullable {
			t.Fatalf("%s columns = %+v", fixture.name, table.Columns)
		}

		checker := database.NewHealthChecker(pool)
		if err := checker.ValidateDataSourceConfiguration(&ds.Config, ds.Type); err != nil {
			t.Fatalf("%s validate config: %v", fixture.name, err)
		}
		health, err := checker.CheckDataSourceConnectivity(ctx, &ds.Config, ds.Type)
		if err != nil || health.Status != "healthy" {
			t.Fatalf("%s health = %+v, error = %v", fixture.name, health, err)
		}
		db, err := pool.GetConnection(ctx, ds)
		if err != nil {
			t.Fatalf("%s open SQL adapter: %v", fixture.name, err)
		}
		if err := db.PingContext(ctx); err != nil {
			t.Fatalf("%s ping configured object: %v", fixture.name, err)
		}
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
			t.Fatalf("%s count query: %v", fixture.name, err)
		}
		wantCount := int64(1)
		if fixture.rows > 1 {
			wantCount = 2
		}
		if count != wantCount {
			t.Fatalf("%s active count = %d, want %d", fixture.name, count, wantCount)
		}
		rows, err := db.QueryContext(ctx, "SELECT id, name FROM records WHERE active = true ORDER BY id DESC LIMIT 1")
		if err != nil {
			t.Fatalf("%s SQL query: %v", fixture.name, err)
		}
		if !rows.Next() {
			_ = rows.Close()
			t.Fatalf("%s expected a SQL row, error: %v", fixture.name, rows.Err())
		}
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			_ = rows.Close()
			t.Fatalf("%s scan row: %v", fixture.name, err)
		}
		if id != 3 || name != "Grace" {
			_ = rows.Close()
			t.Fatalf("%s row = (%d, %q), want (3, Grace)", fixture.name, id, name)
		}
		_ = rows.Close()

		zeroRows, err := db.QueryContext(ctx, "SELECT * FROM records LIMIT 0")
		if err != nil {
			t.Fatalf("%s LIMIT 0 schema query: %v", fixture.name, err)
		}
		zeroColumns, err := zeroRows.Columns()
		if err != nil || len(zeroColumns) != 4 || zeroRows.Next() {
			_ = zeroRows.Close()
			t.Fatalf("%s LIMIT 0 columns=%v, error=%v", fixture.name, zeroColumns, err)
		}
		_ = zeroRows.Close()
		if _, err := db.ExecContext(ctx, "DELETE FROM records"); err == nil || !strings.Contains(err.Error(), "read-only SELECT") {
			t.Fatalf("%s write rejection error = %v", fixture.name, err)
		}
	}
	t.Log("S3-compatible JSON object, array, and JSON Lines SQL contracts passed against pinned Garage v2.4.1")
}
