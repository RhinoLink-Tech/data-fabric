package object_storage_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestMinIOJSONReadOnlySQLContract(t *testing.T) {
	fixtures := []struct {
		name       string
		key        string
		body       string
		activeRows int64
	}{
		{name: "object", key: "contract/one.json", body: "{\"id\":3,\"name\":\"Grace\",\"active\":true,\"optional\":null}", activeRows: 1},
		{name: "array", key: "contract/array.json", body: "[{\"id\":1,\"name\":\"Ada\",\"active\":true,\"optional\":\"x\"},{\"id\":2,\"name\":\"Linus\",\"active\":false},{\"id\":3,\"name\":\"Grace\",\"active\":true,\"optional\":null}]", activeRows: 2},
		{name: "json-lines", key: "contract/records.ndjson", body: "{\"id\":1,\"name\":\"Ada\",\"active\":true,\"optional\":\"x\"}\n{\"id\":2,\"name\":\"Linus\",\"active\":false}\n{\"id\":3,\"name\":\"Grace\",\"active\":true,\"optional\":null}\n", activeRows: 2},
	}
	objects := make(map[string][]byte, len(fixtures))
	for _, fixture := range fixtures {
		objects["/analytics/"+fixture.key] = []byte(fixture.body)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=minio-access/") {
			http.Error(w, "missing S3 request signature", http.StatusForbidden)
			return
		}
		body, ok := objects[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("ETag", "\"json-fixture\"")
		w.Header().Set("Last-Modified", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(http.TimeFormat))
		switch r.Method {
		case http.MethodHead:
		case http.MethodGet:
			_, _ = w.Write(body)
		default:
			http.Error(w, "unsupported S3 method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			source := &model.DataSource{
				ID: "minio-json-local-" + fixture.name, Type: model.DatabaseTypeMinIOJSON,
				Config: model.DataSourceConfig{
					Host: fixture.key, Database: "analytics", Username: "minio-access", Password: "minio-secret",
					AdditionalProps: map[string]interface{}{
						"object_key": fixture.key, "table_name": "records", "endpoint_url": server.URL, "region": "us-east-1",
					},
				},
			}
			pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
			t.Cleanup(func() { _ = pool.CloseAll() })
			checker := database.NewHealthChecker(pool)
			if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
				t.Fatalf("validate MinIO JSON source: %v", err)
			}
			health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
			if err != nil || health.Status != "healthy" {
				t.Fatalf("MinIO JSON health = %+v, error = %v", health, err)
			}
			extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
			tables, err := extractor.ListTables(context.Background(), source.ID)
			if err != nil || len(tables) != 1 || tables[0].Name != "records" {
				t.Fatalf("list configured MinIO JSON table = %+v, error = %v", tables, err)
			}
			schema, err := extractor.ExtractTableSchema(context.Background(), source.ID, "records")
			if err != nil {
				t.Fatalf("describe configured MinIO JSON table: %v", err)
			}
			columns := make(map[string]metadata.ColumnSchema, len(schema.Columns))
			for _, column := range schema.Columns {
				columns[column.Name] = column
			}
			if len(columns) != 4 || columns["id"].Type != "BIGINT" || columns["name"].Type != "VARCHAR" ||
				columns["active"].Type != "BOOLEAN" || !columns["optional"].Nullable {
				t.Fatalf("MinIO JSON columns = %+v", schema.Columns)
			}
			db, err := pool.GetConnection(context.Background(), source)
			if err != nil {
				t.Fatalf("open MinIO JSON SQL adapter: %v", err)
			}
			var count int64
			if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records WHERE active = true").Scan(&count); err != nil {
				t.Fatalf("count MinIO JSON rows: %v", err)
			}
			if count != fixture.activeRows {
				t.Fatalf("MinIO JSON active count = %d, want %d", count, fixture.activeRows)
			}
			rows, err := db.QueryContext(context.Background(), "SELECT id, name FROM records WHERE active = true ORDER BY id DESC LIMIT 1")
			if err != nil {
				t.Fatalf("query MinIO JSON object: %v", err)
			}
			if !rows.Next() {
				_ = rows.Close()
				t.Fatalf("expected a MinIO JSON row; rows error: %v", rows.Err())
			}
			var id int64
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				_ = rows.Close()
				t.Fatalf("scan MinIO JSON row: %v", err)
			}
			if id != 3 || name != "Grace" {
				_ = rows.Close()
				t.Fatalf("MinIO JSON row = (%d, %q), want (3, Grace)", id, name)
			}
			_ = rows.Close()
			zeroRows, err := db.QueryContext(context.Background(), "SELECT * FROM records LIMIT 0")
			if err != nil {
				t.Fatalf("discover MinIO JSON schema: %v", err)
			}
			zeroColumns, err := zeroRows.Columns()
			if err != nil || len(zeroColumns) != 4 || zeroRows.Next() {
				_ = zeroRows.Close()
				t.Fatalf("MinIO JSON LIMIT 0 columns=%v, error=%v", zeroColumns, err)
			}
			_ = zeroRows.Close()
			if _, err := db.ExecContext(context.Background(), "DELETE FROM records"); err == nil {
				t.Fatal("MinIO JSON source accepted a write")
			}
		})
	}
}
