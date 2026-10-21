package table_formats

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog"
	"github.com/apache/iceberg-go/catalog/hadoop"
	"github.com/apache/iceberg-go/table"
)

func TestIcebergSQLQueryUsesCurrentSnapshotThroughREST(t *testing.T) {
	ctx := context.Background()
	warehouse := filepath.ToSlash(t.TempDir())
	localCatalog, err := hadoop.NewCatalog("fixture", warehouse, iceberg.Properties{})
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
		t.Fatalf("write Iceberg fixture snapshot: %v", err)
	}
	if fixtureTable.CurrentSnapshot() == nil {
		t.Fatal("fixture table did not resolve a current snapshot")
	}
	metadata, err := json.Marshal(fixtureTable.Metadata())
	if err != nil {
		t.Fatalf("marshal fixture metadata: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/config":
			_, _ = w.Write([]byte(`{"defaults":{},"overrides":{}}`))
		case "/v1/namespaces":
			_, _ = w.Write([]byte(`{"namespaces":[["sales"]]}`))
		case "/v1/namespaces/sales/tables/orders":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"metadata-location": fixtureTable.MetadataLocation(),
				"metadata":          json.RawMessage(metadata),
				"config":            map[string]string{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	config := &model.DataSourceConfig{
		Host: "127.0.0.1", Port: 8181, Database: "test-warehouse", Schema: "sales",
		AdditionalProps: map[string]interface{}{"catalog_uri": server.URL},
	}
	icebergDriver := &IcebergDriver{}
	queryDB, err := icebergDriver.OpenWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open configured Iceberg adapter: %v", err)
	}
	defer queryDB.Close()
	if err := queryDB.PingContext(ctx); err != nil {
		t.Fatalf("ping configured Iceberg catalog: %v", err)
	}

	rows, err := queryDB.QueryContext(ctx,
		"SELECT id AS order_id, name FROM orders WHERE id >= ? AND name = ? LIMIT ? OFFSET ?",
		int64(2), "alpha", int64(1), int64(0),
	)
	if err != nil {
		t.Fatalf("query current Iceberg snapshot: %v", err)
	}
	defer rows.Close()
	gotColumns, err := rows.Columns()
	if err != nil {
		t.Fatalf("read result columns: %v", err)
	}
	if want := []string{"order_id", "name"}; !reflect.DeepEqual(gotColumns, want) {
		t.Fatalf("columns = %#v, want %#v", gotColumns, want)
	}
	if !rows.Next() {
		t.Fatalf("expected one filtered row; rows error: %v", rows.Err())
	}
	var id int64
	var name string
	if err := rows.Scan(&id, &name); err != nil {
		t.Fatalf("scan result row: %v", err)
	}
	if id != 3 || name != "alpha" {
		t.Fatalf("row = (%d, %q), want (3, alpha)", id, name)
	}
	if hasNext := rows.Next(); hasNext || rows.Err() != nil {
		t.Fatalf("expected exactly one row; next=%t error=%v", hasNext, rows.Err())
	}

	var count int64
	countQuery := "SELECT COUNT(*) AS total FROM (SELECT id FROM sales.orders WHERE name = 'alpha') AS t"
	if err := queryDB.QueryRowContext(ctx, countQuery).Scan(&count); err != nil {
		t.Fatalf("count current Iceberg snapshot: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	var pagedCount int64
	if err := queryDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM (SELECT id FROM sales.orders LIMIT 3 OFFSET 2) AS page").Scan(&pagedCount); err != nil {
		t.Fatalf("count bounded inner page: %v", err)
	}
	if pagedCount != 1 {
		t.Fatalf("count of inner page = %d, want 1", pagedCount)
	}

	zeroCountRows, err := queryDB.QueryContext(ctx, "SELECT COUNT(*) FROM sales.orders LIMIT 0")
	if err != nil {
		t.Fatalf("query zero-row aggregate page: %v", err)
	}
	if zeroCountRows.Next() || zeroCountRows.Err() != nil {
		t.Fatalf("COUNT(*) LIMIT 0 should return no rows; error=%v", zeroCountRows.Err())
	}
	_ = zeroCountRows.Close()
	directRows, err := icebergDriver.QueryTable(ctx, "SELECT id FROM sales.orders LIMIT 1")
	if err != nil {
		t.Fatalf("query through QueryTable: %v", err)
	}
	defer directRows.Close()
	if !directRows.Next() {
		t.Fatalf("QueryTable returned no rows: %v", directRows.Err())
	}
	var firstID int64
	if err := directRows.Scan(&firstID); err != nil {
		t.Fatalf("scan QueryTable row: %v", err)
	}
	if firstID != 1 {
		t.Fatalf("QueryTable id = %d, want 1", firstID)
	}
}

func TestIcebergConfigFromDataSourceSupportsIPv6AndOAuth(t *testing.T) {
	settings, err := icebergConfigFromDataSource(&model.DataSourceConfig{
		Host: "::1", Port: 8181, Database: "warehouse", Schema: "sales",
		Username: "catalog-client", Password: "secret",
		AdditionalProps: map[string]interface{}{"max_rows": float64(256), "s3.region": "us-east-1"},
	})
	if err != nil {
		t.Fatalf("build Iceberg config: %v", err)
	}
	if settings.uri != "http://[::1]:8181" || settings.credential != "catalog-client:secret" || settings.maxRows != 256 {
		t.Fatalf("unexpected Iceberg config: %#v", settings)
	}
	if settings.props["s3.region"] != "us-east-1" {
		t.Fatalf("S3 property was not forwarded: %#v", settings.props)
	}
}

func TestIcebergCountOffsetOnlyCountsInnerResultRows(t *testing.T) {
	plan, err := parseIcebergSQL("SELECT COUNT(*) FROM (SELECT id FROM sales.orders LIMIT 3 OFFSET 2) AS t", nil, "", 1000, false)
	if err != nil {
		t.Fatalf("parse count query: %v", err)
	}
	if !plan.count || plan.offset != 2 || plan.limit != 3 || !plan.hasLimit {
		t.Fatalf("unexpected count page plan: %#v", plan)
	}
}

func TestIcebergRESTLoadTableReadsStandardMetadataEnvelope(t *testing.T) {
	response := map[string]any{
		"metadata-location": "s3://warehouse/sales/orders/metadata/v2.metadata.json",
		"metadata": map[string]any{
			"format-version":    2,
			"current-schema-id": 3,
			"schemas": []any{
				map[string]any{"schema-id": 1, "fields": []any{map[string]any{"id": 1, "name": "old_id", "type": "long"}}},
				map[string]any{"schema-id": 3, "fields": []any{map[string]any{"id": 2, "name": "order_id", "type": "long"}}},
			},
			"default-spec-id": 7,
			"partition-specs": []any{map[string]any{"spec-id": 7, "fields": []any{
				map[string]any{"source-id": 2, "field-id": 1000, "name": "order_bucket", "transform": "bucket[16]"},
			}}},
			"properties":          map[string]string{"owner": "tests"},
			"current-snapshot-id": 91,
			"snapshots": []any{map[string]any{
				"sequence-number": 4, "snapshot-id": 91, "timestamp-ms": 1234,
				"manifest-list": "s3://warehouse/sales/orders/metadata/snap-91.avro",
				"summary":       map[string]string{"operation": "append"}, "schema-id": 3,
			}},
			"snapshot-log": []any{map[string]any{"snapshot-id": 91, "timestamp-ms": 1234}},
			"metadata-log": []any{map[string]any{"metadata-file": "v2.metadata.json", "timestamp-ms": 1234}},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/namespaces/sales/tables/orders" {
			t.Errorf("unexpected REST request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client, err := NewIcebergRESTClient(&IcebergConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("create REST client: %v", err)
	}
	metadata, err := client.LoadTable(context.Background(), "sales", "orders")
	if err != nil {
		t.Fatalf("load standard REST metadata envelope: %v", err)
	}
	if metadata.FormatVersion != 2 || metadata.Format != "iceberg-v2" || metadata.Schema.SchemaID != 3 || len(metadata.Schema.Fields) != 1 || metadata.Schema.Fields[0].Name != "order_id" {
		t.Fatalf("unexpected current metadata schema: %#v", metadata)
	}
	if metadata.SpecID != 7 || len(metadata.PartitionSpec) != 1 || metadata.PartitionSpec[0].FieldID != 1000 {
		t.Fatalf("unexpected default partition spec: %#v", metadata.PartitionSpec)
	}
	if len(metadata.Snapshots) != 1 || metadata.Snapshots[0].SequenceNumber != 4 || metadata.Snapshots[0].ManifestList == "" {
		t.Fatalf("unexpected standard snapshot metadata: %#v", metadata.Snapshots)
	}
	snapshot, err := client.GetTableSnapshot(context.Background(), "sales", "orders", "91")
	if err != nil || snapshot.SnapshotID != 91 {
		t.Fatalf("find snapshot from standard metadata (snapshot=%#v, error=%v)", snapshot, err)
	}
}
