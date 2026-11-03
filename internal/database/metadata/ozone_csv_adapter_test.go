package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestOzoneCSVAdapterSupportsUnifiedMetadataDiscovery(t *testing.T) {
	ctx := context.Background()
	data := []byte("id,name\n1,Ada\n2,Lin\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/analytics/warehouse/events.csv" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer server.Close()

	source := &model.DataSource{
		ID: "ozone-csv-events", Type: model.DatabaseTypeOzoneCSV,
		Config: model.DataSourceConfig{
			Host: "warehouse/events.csv", Database: "analytics", Username: "access-key", Password: "secret-key",
			AdditionalProps: map[string]interface{}{
				"endpoint_url": server.URL, "object_key": "warehouse/events.csv", "table_name": "events",
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
