package object_storage_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/catalog/hadoop"
	icebergio "github.com/apache/iceberg-go/io"
	"github.com/apache/iceberg-go/table"
	"github.com/google/uuid"
)

func TestMinIOIcebergConfiguredReadPath(t *testing.T) {
	ctx := context.Background()
	warehousePath := filepath.ToSlash(t.TempDir())
	localCatalog, err := hadoop.NewCatalog("local-iceberg-fixture", warehousePath, iceberg.Properties{})
	if err != nil {
		t.Fatalf("create local fixture catalog: %v", err)
	}
	defer localCatalog.Close()
	if err := localCatalog.CreateNamespace(ctx, table.Identifier{"sales"}, iceberg.Properties{}); err != nil {
		t.Fatalf("create fixture namespace: %v", err)
	}
	schema := iceberg.NewSchema(0,
		iceberg.NestedField{ID: 1, Name: "id", Type: iceberg.PrimitiveTypes.Int64, Required: true},
		iceberg.NestedField{ID: 2, Name: "name", Type: iceberg.PrimitiveTypes.String, Required: true},
	)
	fixtureTable, err := localCatalog.CreateTable(ctx, table.Identifier{"sales", "orders"}, schema,
		catalog.WithPartitionSpec(iceberg.UnpartitionedSpec), catalog.WithSortOrder(table.UnsortedSortOrder))
	if err != nil {
		t.Fatalf("create fixture table: %v", err)
	}
	arrowSchema, err := table.SchemaToArrowSchema(schema, nil, false, false)
	if err != nil {
		t.Fatalf("convert fixture schema: %v", err)
	}
	builder := array.NewRecordBuilder(memory.DefaultAllocator, arrowSchema)
	builder.Field(0).(*array.Int64Builder).AppendValues([]int64{1, 2, 3}, nil)
	builder.Field(1).(*array.StringBuilder).AppendValues([]string{"alpha", "beta", "alpha"}, nil)
	record := builder.NewRecordBatch()
	builder.Release()
	reader, err := array.NewRecordReader(arrowSchema, []arrow.RecordBatch{record})
	if err != nil {
		record.Release()
		t.Fatalf("create fixture record reader: %v", err)
	}
	fixtureTable, err = fixtureTable.Append(ctx, reader, nil)
	reader.Release()
	record.Release()
	if err != nil {
		t.Fatalf("write fixture snapshot: %v", err)
	}
	metadataBytes, err := json.Marshal(fixtureTable.Metadata())
	if err != nil {
		t.Fatalf("marshal fixture metadata: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/config":
			_, _ = w.Write([]byte("{\"defaults\":{},\"overrides\":{}}"))
		case "/v1/namespaces":
			_, _ = w.Write([]byte("{\"namespaces\":[[\"sales\"]]}"))
		case "/v1/namespaces/sales/tables/orders":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"metadata-location": fixtureTable.MetadataLocation(),
				"metadata":          json.RawMessage(metadataBytes),
				"config":            map[string]string{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := &model.DataSource{
		ID: "minio-iceberg-rest-fixture", Type: model.DatabaseTypeMinIOIceberg,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: server.URL, Database: "warehouse", Schema: "sales",
			Username: "minio-access", Password: "minio-secret",
			AdditionalProps: map[string]interface{}{
				"catalog_uri": server.URL, "warehouse": "warehouse", "namespace": "sales",
				"table_name": "orders", "endpoint_url": "http://127.0.0.1:9000",
				"region": "us-east-1", "force_path_style": true, "max_rows": 100,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate MinIO Iceberg source: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(ctx, &source.Config, source.Type)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("MinIO Iceberg health = %+v, error = %v", health, err)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(ctx, source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "orders" || tables[0].Schema != "sales" {
		t.Fatalf("list configured MinIO Iceberg table = %+v, error = %v", tables, err)
	}
	tableSchema, err := extractor.ExtractTableSchemaInSchema(ctx, source.ID, "sales", "orders")
	if err != nil {
		t.Fatalf("describe configured MinIO Iceberg table: %v", err)
	}
	if len(tableSchema.Columns) != 2 || tableSchema.Columns[0].Name != "id" || tableSchema.Columns[1].Name != "name" {
		t.Fatalf("MinIO Iceberg columns = %+v", tableSchema.Columns)
	}

	db, err := pool.GetConnection(ctx, source)
	if err != nil {
		t.Fatalf("open MinIO Iceberg SQL connection: %v", err)
	}
	rows, err := db.QueryContext(ctx, "SELECT id, name FROM orders WHERE id >= ? LIMIT ? OFFSET ?", int64(2), int64(1), int64(1))
	if err != nil {
		t.Fatalf("query MinIO Iceberg snapshot: %v", err)
	}
	if !rows.Next() {
		t.Fatalf("expected one MinIO Iceberg row; rows error: %v", rows.Err())
	}
	var id int64
	var name string
	if err := rows.Scan(&id, &name); err != nil {
		t.Fatalf("scan MinIO Iceberg result: %v", err)
	}
	if (id != 2 || name != "beta") && (id != 3 || name != "alpha") {
		t.Fatalf("MinIO Iceberg row = (%d, %q), want a row matching id >= 2", id, name)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("MinIO Iceberg query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	if _, err := db.QueryContext(ctx, "SELECT id FROM other.orders LIMIT 1"); err == nil {
		t.Fatal("MinIO Iceberg source allowed access to a table outside its configured namespace")
	}
	if _, err := db.QueryContext(ctx, "SELECT id FROM customers LIMIT 1"); err == nil {
		t.Fatal("MinIO Iceberg source allowed access to a table outside its configured table")
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM orders"); err == nil {
		t.Fatal("MinIO Iceberg source accepted a write")
	}
}

func TestMinIOIcebergContract(t *testing.T) {
	endpoint := os.Getenv("DATA_FABRIC_MINIO_CONTRACT_ENDPOINT")
	if endpoint == "" {
		t.Skip("set DATA_FABRIC_MINIO_CONTRACT_ENDPOINT to run the live MinIO Iceberg contract")
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

	warehouse := "s3://" + bucket + "/data-fabric-contract/iceberg/" + uuid.NewString()
	fileIOProps := iceberg.Properties{
		"allow-unsafe-commits":        "true",
		"s3.region":                   region,
		"s3.access-key-id":            accessKey,
		"s3.secret-access-key":        secretKey,
		"s3.endpoint":                 endpoint,
		"s3.force-virtual-addressing": "false",
	}
	fixtureCatalog, err := hadoop.NewCatalog("minio-contract", warehouse, fileIOProps)
	if err != nil {
		t.Fatalf("create MinIO-backed Iceberg fixture catalog: %v", err)
	}
	defer fixtureCatalog.Close()
	t.Cleanup(func() {
		cleanupFS, err := icebergio.LoadFS(context.Background(), fileIOProps, warehouse)
		if err != nil {
			t.Errorf("open isolated MinIO Iceberg fixture prefix for cleanup: %v", err)
			return
		}
		remover, ok := cleanupFS.(icebergio.RemoveAllIO)
		if !ok {
			t.Errorf("MinIO Iceberg FileIO %T does not support scoped cleanup", cleanupFS)
			return
		}
		if err := remover.RemoveAll(warehouse); err != nil {
			t.Errorf("remove isolated MinIO Iceberg fixture prefix: %v", err)
		}
	})
	if err := fixtureCatalog.CreateNamespace(ctx, table.Identifier{"sales"}, iceberg.Properties{}); err != nil {
		t.Fatalf("create MinIO Iceberg fixture namespace: %v", err)
	}
	schema := iceberg.NewSchema(0,
		iceberg.NestedField{ID: 1, Name: "id", Type: iceberg.PrimitiveTypes.Int64, Required: true},
		iceberg.NestedField{ID: 2, Name: "name", Type: iceberg.PrimitiveTypes.String, Required: true},
	)
	fixtureTable, err := fixtureCatalog.CreateTable(ctx, table.Identifier{"sales", "orders"}, schema,
		catalog.WithPartitionSpec(iceberg.UnpartitionedSpec), catalog.WithSortOrder(table.UnsortedSortOrder))
	if err != nil {
		t.Fatalf("create MinIO Iceberg fixture table: %v", err)
	}
	arrowSchema, err := table.SchemaToArrowSchema(schema, nil, false, false)
	if err != nil {
		t.Fatalf("convert MinIO Iceberg fixture schema: %v", err)
	}
	builder := array.NewRecordBuilder(memory.DefaultAllocator, arrowSchema)
	builder.Field(0).(*array.Int64Builder).AppendValues([]int64{1, 2, 3}, nil)
	builder.Field(1).(*array.StringBuilder).AppendValues([]string{"alpha", "beta", "alpha"}, nil)
	record := builder.NewRecordBatch()
	builder.Release()
	reader, err := array.NewRecordReader(arrowSchema, []arrow.RecordBatch{record})
	if err != nil {
		record.Release()
		t.Fatalf("create MinIO Iceberg fixture record reader: %v", err)
	}
	fixtureTable, err = fixtureTable.Append(ctx, reader, nil)
	reader.Release()
	record.Release()
	if err != nil {
		t.Fatalf("write Iceberg metadata and data files to MinIO: %v", err)
	}
	metadataBytes, err := json.Marshal(fixtureTable.Metadata())
	if err != nil {
		t.Fatalf("marshal MinIO Iceberg fixture metadata: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/config":
			_, _ = w.Write([]byte("{\"defaults\":{},\"overrides\":{}}"))
		case "/v1/namespaces":
			_, _ = w.Write([]byte("{\"namespaces\":[[\"sales\"]]}"))
		case "/v1/namespaces/sales/tables/orders":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"metadata-location": fixtureTable.MetadataLocation(),
				"metadata":          json.RawMessage(metadataBytes),
				"config":            map[string]string{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := &model.DataSource{
		ID: "minio-iceberg-contract", Type: model.DatabaseTypeMinIOIceberg,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: server.URL, Database: "warehouse", Schema: "sales",
			Username: accessKey, Password: secretKey,
			AdditionalProps: map[string]interface{}{
				"catalog_uri": server.URL, "warehouse": "warehouse", "namespace": "sales",
				"table_name": "orders", "endpoint_url": endpoint,
				"region": region, "force_path_style": true, "max_rows": 100,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate MinIO Iceberg source: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(ctx, &source.Config, source.Type)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("MinIO Iceberg health = %+v, error = %v", health, err)
	}

	db, err := pool.GetConnection(ctx, source)
	if err != nil {
		t.Fatalf("open MinIO Iceberg SQL connection: %v", err)
	}
	rows, err := db.QueryContext(ctx, "SELECT id, name FROM orders WHERE id = ? LIMIT ?", int64(2), int64(1))
	if err != nil {
		t.Fatalf("query MinIO-backed Iceberg snapshot: %v", err)
	}
	if !rows.Next() {
		t.Fatalf("expected one MinIO Iceberg row; rows error: %v", rows.Err())
	}
	var id int64
	var name string
	if err := rows.Scan(&id, &name); err != nil {
		t.Fatalf("scan MinIO Iceberg result: %v", err)
	}
	if id != 2 || name != "beta" {
		t.Fatalf("MinIO Iceberg row = (%d, %q), want (2, beta)", id, name)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("MinIO Iceberg query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()
	if _, err := db.QueryContext(ctx, "DELETE FROM orders"); err == nil {
		t.Fatal("MinIO Iceberg source accepted a write")
	}

	cosSource := *source
	cosSource.ID = "cos-iceberg-s3-compatible-contract"
	cosSource.Type = model.DatabaseTypeTencentCOSIceberg
	cosSource.Config = source.Config
	cosSource.Config.AdditionalProps = make(map[string]interface{}, len(source.Config.AdditionalProps))
	for key, value := range source.Config.AdditionalProps {
		cosSource.Config.AdditionalProps[key] = value
	}
	if err := checker.ValidateDataSourceConfiguration(&cosSource.Config, cosSource.Type); err != nil {
		t.Fatalf("validate COS Iceberg adapter against MinIO S3 compatibility endpoint: %v", err)
	}
	cosHealth, err := checker.CheckDataSourceConnectivity(ctx, &cosSource.Config, cosSource.Type)
	if err != nil || cosHealth.Status != "healthy" {
		t.Fatalf("COS Iceberg health against MinIO S3 compatibility endpoint = %+v, error = %v", cosHealth, err)
	}
	cosDB, err := pool.GetConnection(ctx, &cosSource)
	if err != nil {
		t.Fatalf("open COS Iceberg S3-compatible SQL connection: %v", err)
	}
	cosRows, err := cosDB.QueryContext(ctx, "SELECT id, name FROM orders WHERE id = ? LIMIT ?", int64(2), int64(1))
	if err != nil {
		t.Fatalf("query COS Iceberg through MinIO S3 compatibility endpoint: %v", err)
	}
	if !cosRows.Next() {
		t.Fatalf("expected one COS Iceberg row; rows error: %v", cosRows.Err())
	}
	var cosID int64
	var cosName string
	if err := cosRows.Scan(&cosID, &cosName); err != nil {
		t.Fatalf("scan COS Iceberg result: %v", err)
	}
	if cosID != 2 || cosName != "beta" {
		t.Fatalf("COS Iceberg row = (%d, %q), want (2, beta)", cosID, cosName)
	}
	if cosRows.Next() || cosRows.Err() != nil {
		t.Fatalf("COS Iceberg query returned extra rows or error: %v", cosRows.Err())
	}
	_ = cosRows.Close()
	if _, err := cosDB.QueryContext(ctx, "DELETE FROM orders"); err == nil {
		t.Fatal("COS Iceberg adapter accepted a write")
	}
}
