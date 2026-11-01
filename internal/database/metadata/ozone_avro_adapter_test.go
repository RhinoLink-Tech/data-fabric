package metadata

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/linkedin/goavro/v2"
)

const ozoneAvroMetadataSchema = "{\"type\":\"record\",\"name\":\"Event\",\"fields\":[{\"name\":\"id\",\"type\":\"long\"},{\"name\":\"name\",\"type\":[\"null\",\"string\"],\"default\":null}]}"

func TestOzoneAvroAdapterSupportsUnifiedMetadataDiscovery(t *testing.T) {
	ctx := context.Background()
	codec, err := goavro.NewCodec(ozoneAvroMetadataSchema)
	if err != nil {
		t.Fatalf("create Avro codec: %v", err)
	}
	var output bytes.Buffer
	writer, err := goavro.NewOCFWriter(goavro.OCFConfig{W: &output, Codec: codec, CompressionName: "null"})
	if err != nil {
		t.Fatalf("create Avro OCF writer: %v", err)
	}
	if err := writer.Append([]interface{}{map[string]interface{}{"id": int64(1), "name": goavro.Union("string", "Ada")}}); err != nil {
		t.Fatalf("write Avro fixture: %v", err)
	}
	data := append([]byte(nil), output.Bytes()...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/analytics/warehouse/events.avro" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "missing SigV4 authorization", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer server.Close()

	source := &model.DataSource{
		ID: "ozone-avro-events", Type: model.DatabaseTypeOzoneAvro,
		Config: model.DataSourceConfig{
			Host: "warehouse/events.avro", Database: "analytics", Username: "access-key", Password: "secret-key",
			AdditionalProps: map[string]interface{}{
				"endpoint_url": server.URL, "object_key": "warehouse/events.avro", "table_name": "events",
			},
		},
	}
	registry := database.NewDriverRegistry()
	pool := database.NewConnectionPool(registry, nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	extractor := &MetadataExtractor{
		connPool: pool,
		sources:  &dataSourceLookupStub{dataSource: source},
		registry: registry,
	}

	tables, err := extractor.ListTables(ctx, source.ID)
	if err != nil {
		t.Fatalf("ListTables() error = %v", err)
	}
	if len(tables) != 1 || tables[0].Name != "events" {
		t.Fatalf("ListTables() = %+v, want events", tables)
	}
	table, err := extractor.ExtractTableSchema(ctx, source.ID, "events")
	if err != nil {
		t.Fatalf("ExtractTableSchema() error = %v", err)
	}
	if len(table.Columns) != 2 || table.Columns[0].Name != "id" || table.Columns[1].Name != "name" {
		t.Fatalf("ExtractTableSchema() columns = %+v, want id and name", table.Columns)
	}
	schema, _, err := extractor.ExtractSchema(ctx, source.ID)
	if err != nil {
		t.Fatalf("ExtractSchema() error = %v", err)
	}
	if len(schema.Tables) != 1 || schema.Tables["events"] == nil {
		t.Fatalf("ExtractSchema() tables = %+v, want events", schema.Tables)
	}
}
