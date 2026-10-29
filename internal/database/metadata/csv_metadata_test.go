package metadata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestCSVObjectMetadataDiscovery(t *testing.T) {
	cases := []struct {
		name, csv, tableName string
		sourceType           model.DatabaseType
		props                map[string]interface{}
		columns              []ColumnSchema
	}{
		{
			name: "minio custom escaped table", csv: "id,name,active\n1,Ada,true\n2,Lin,false\n", tableName: "sales` data",
			sourceType: model.DatabaseTypeMinIOCSV, props: map[string]interface{}{"table_name": "sales` data"},
			columns: []ColumnSchema{{Name: "id", Type: "BIGINT", Nullable: true}, {Name: "name", Type: "VARCHAR", Nullable: true}, {Name: "active", Type: "BOOLEAN", Nullable: true}},
		},
		{
			name: "minio header only", csv: "id,name\n", tableName: "records",
			sourceType: model.DatabaseTypeMinIOCSV, props: map[string]interface{}{},
			columns: []ColumnSchema{{Name: "id", Type: "VARCHAR", Nullable: true}, {Name: "name", Type: "VARCHAR", Nullable: true}},
		},
		{
			name: "s3 without header and custom delimiter", csv: "1;Ada\n2;Lin\n", tableName: "records",
			sourceType: model.DatabaseTypeS3CSV, props: map[string]interface{}{"has_header": false, "delimiter": ";"},
			columns: []ColumnSchema{{Name: "column_1", Type: "BIGINT", Nullable: true}, {Name: "column_2", Type: "VARCHAR", Nullable: true}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gets atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/bucket/path/records.csv" {
					http.NotFound(w, r)
					return
				}
				switch r.Method {
				case http.MethodHead:
					w.Header().Set("Content-Length", fmt.Sprint(len(tc.csv)))
				case http.MethodGet:
					gets.Add(1)
					_, _ = io.WriteString(w, tc.csv)
				default:
					http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
				}
			}))
			defer server.Close()
			props := map[string]interface{}{"endpoint_url": server.URL, "force_path_style": true, "object_key": "path/records.csv"}
			for key, value := range tc.props {
				props[key] = value
			}
			source := &model.DataSource{ID: "csv-source", Type: tc.sourceType, Config: model.DataSourceConfig{
				Database: "bucket", Username: "access", Password: "secret", AdditionalProps: props,
			}}
			pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
			defer pool.CloseAll()
			extractor := &MetadataExtractor{connPool: pool, sources: &dataSourceLookupStub{dataSource: source}, registry: database.NewDriverRegistry()}
			ctx := context.Background()
			tables, err := extractor.ListTables(ctx, source.ID)
			if err != nil {
				t.Fatalf("ListTables: %v", err)
			}
			if !reflect.DeepEqual(tables, []TableIdentity{{Name: tc.tableName, Type: "TABLE"}}) {
				t.Fatalf("tables = %+v", tables)
			}
			if count := gets.Load(); count != 0 {
				t.Fatalf("ListTables downloaded object %d times", count)
			}
			for _, req := range []struct{ schema, name string }{{"", "missing"}, {"other", tc.tableName}} {
				_, err := extractor.ExtractTableSchemaInSchema(ctx, source.ID, req.schema, req.name)
				if !errors.Is(err, ErrTableNotFound) {
					t.Fatalf("unexpected lookup %q/%q error: %v", req.schema, req.name, err)
				}
			}
			if count := gets.Load(); count != 0 {
				t.Fatalf("rejected lookup downloaded object %d times", count)
			}
			table, err := extractor.ExtractTableSchema(ctx, source.ID, tc.tableName)
			if err != nil {
				t.Fatalf("ExtractTableSchema: %v", err)
			}
			if table.Name != tc.tableName || table.Schema != "" || table.Type != "TABLE" || !reflect.DeepEqual(table.Columns, tc.columns) {
				t.Fatalf("table = %+v, columns = %+v, want %+v", table, table.Columns, tc.columns)
			}
			schema, version, err := extractor.ExtractSchema(ctx, source.ID)
			if err != nil {
				t.Fatalf("ExtractSchema: %v", err)
			}
			if version == "" || !reflect.DeepEqual(schema.Tables[tc.tableName], table) {
				t.Fatalf("schema = %+v, version = %q", schema, version)
			}
		})
	}
}
