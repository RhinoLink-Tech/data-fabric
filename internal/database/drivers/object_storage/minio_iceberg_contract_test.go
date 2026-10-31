package object_storage_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/catalog/hadoop"
	"github.com/apache/iceberg-go/table"
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
