package table_formats

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// GetSchemaAtSnapshot must return the schema the snapshot actually used, not
// the table's current schema. The metadata below has two schema versions: the
// current one (schema 3, "order_id") and an older one (schema 1, "old_id").
// Snapshot 90 was written with schema 1, so this must resolve to schema 1.
func TestGetSchemaAtSnapshotReturnsSnapshotsSchemaNotCurrent(t *testing.T) {
	response := map[string]any{
		"metadata-location": "s3://warehouse/sales/orders/metadata/v2.metadata.json",
		"metadata": map[string]any{
			"format-version":    2,
			"current-schema-id": 3,
			"schemas": []any{
				map[string]any{"schema-id": 1, "fields": []any{map[string]any{"id": 1, "name": "old_id", "type": "long"}}},
				map[string]any{"schema-id": 3, "fields": []any{map[string]any{"id": 2, "name": "order_id", "type": "long"}}},
			},
			"current-snapshot-id": 90,
			"snapshots": []any{map[string]any{
				"snapshot-id": 90, "schema-id": 1, "timestamp-ms": 1000,
				"manifest-list": "s3://warehouse/sales/orders/metadata/snap-90.avro",
				"summary":       map[string]string{"operation": "append"},
			}},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	driver, err := NewIcebergDriver(&IcebergConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("create iceberg driver: %v", err)
	}
	tt := NewIcebergTimeTravel(driver)

	schema, err := tt.GetSchemaAtSnapshot(context.Background(), "sales", "orders", 90)
	if err != nil {
		t.Fatalf("GetSchemaAtSnapshot() error = %v", err)
	}
	if schema.SchemaID != 1 {
		t.Fatalf("GetSchemaAtSnapshot() SchemaID = %d, want 1 (the snapshot's schema, not current schema 3)", schema.SchemaID)
	}
	if len(schema.Fields) != 1 || schema.Fields[0].Name != "old_id" {
		t.Fatalf("GetSchemaAtSnapshot() returned %+v, want the schema{1:[old_id]} that snapshot 90 used", schema.Fields)
	}
}
