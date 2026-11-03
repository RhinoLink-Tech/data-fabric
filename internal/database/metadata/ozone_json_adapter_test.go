package metadata

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestOzoneJSONAdapterSupportsUnifiedQueryMetadata(t *testing.T) {
	data := []byte("[{\"id\":1,\"name\":\"Ada\"},{\"id\":2,\"name\":\"Lin\"}]")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/analytics/warehouse/events.json" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=access-key/") {
			http.Error(w, "missing SigV4 credentials", http.StatusForbidden)
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

	ctx := context.Background()
	source := &model.DataSource{
		ID: "ozone-json-events", Type: model.DatabaseTypeOzoneJSON,
		Config: model.DataSourceConfig{
			Host: "warehouse/events.json", Database: "analytics", Username: "access-key", Password: "secret-key",
			AdditionalProps: map[string]interface{}{
				"endpoint_url": server.URL, "object_key": "warehouse/events.json", "table_name": "events",
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
	if len(tables) != 1 || tables[0].Name != "events" || tables[0].Type != "TABLE" {
		t.Fatalf("ListTables() = %+v, want the configured events table", tables)
	}
	table, err := extractor.ExtractTableSchema(ctx, source.ID, "events")
	if err != nil {
		t.Fatalf("ExtractTableSchema() error = %v", err)
	}
	if len(table.Columns) != 2 || table.Columns[0].Name != "id" || table.Columns[1].Name != "name" {
		t.Fatalf("ExtractTableSchema() columns = %+v, want id and name", table.Columns)
	}
}
