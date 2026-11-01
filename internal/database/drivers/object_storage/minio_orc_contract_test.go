package object_storage_test

import (
	"context"
	"fmt"
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

func TestMinIOORCReadOnlySQLContract(t *testing.T) {
	orcData := s3ORCContractFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=minio-access/") {
			http.Error(w, "missing S3 request signature", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/analytics/contract/records.orc" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Type", "application/vnd.apache.orc")
		w.Header().Set("ETag", "\"orc-fixture\"")
		w.Header().Set("Last-Modified", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(http.TimeFormat))
		switch r.Method {
		case http.MethodHead:
			w.Header().Set("Content-Length", strconv.Itoa(len(orcData)))
		case http.MethodGet:
			start, end := int64(0), int64(len(orcData)-1)
			if rawRange := r.Header.Get("Range"); rawRange != "" {
				value, ok := strings.CutPrefix(rawRange, "bytes=")
				parts := strings.SplitN(value, "-", 2)
				if !ok || len(parts) != 2 {
					http.Error(w, "invalid byte range", http.StatusRequestedRangeNotSatisfiable)
					return
				}
				var err error
				start, err = strconv.ParseInt(parts[0], 10, 64)
				if err != nil {
					http.Error(w, "invalid range start", http.StatusRequestedRangeNotSatisfiable)
					return
				}
				end, err = strconv.ParseInt(parts[1], 10, 64)
				if err != nil || start < 0 || start >= int64(len(orcData)) || end < start {
					http.Error(w, "invalid byte range", http.StatusRequestedRangeNotSatisfiable)
					return
				}
				if end >= int64(len(orcData)) {
					end = int64(len(orcData)) - 1
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(orcData)))
				w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(orcData[start : end+1])
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(orcData)))
			_, _ = w.Write(orcData)
		default:
			http.Error(w, "unsupported S3 method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	source := &model.DataSource{
		ID: "minio-orc-local-fixture", Type: model.DatabaseTypeMinIOORC,
		Config: model.DataSourceConfig{
			Host: "contract/records.orc", Database: "analytics",
			Username: "minio-access", Password: "minio-secret",
			AdditionalProps: map[string]interface{}{
				"object_key": "contract/records.orc", "table_name": "records",
				"endpoint_url": server.URL, "region": "us-east-1",
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	t.Cleanup(func() { _ = pool.CloseAll() })
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate MinIO ORC source: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &source.Config, source.Type)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("MinIO ORC health = %+v, error = %v", health, err)
	}

	extractor := metadata.NewMetadataExtractor(pool, minioContractSourceRepo{source: source})
	tables, err := extractor.ListTables(context.Background(), source.ID)
	if err != nil || len(tables) != 1 || tables[0].Name != "records" {
		t.Fatalf("list configured MinIO ORC table = %+v, error = %v", tables, err)
	}
	schema, err := extractor.ExtractTableSchema(context.Background(), source.ID, "records")
	if err != nil {
		t.Fatalf("describe configured MinIO ORC table: %v", err)
	}
	if len(schema.Columns) != 5 || schema.Columns[0].Name != "userid" || schema.Columns[0].Type != "BIGINT" {
		t.Fatalf("MinIO ORC columns = %+v", schema.Columns)
	}

	db, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("open MinIO ORC SQL connection: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT userid, string1 FROM records WHERE userid = 2 ORDER BY string1 LIMIT 1")
	if err != nil {
		t.Fatalf("query configured MinIO ORC object: %v", err)
	}
	if !rows.Next() {
		t.Fatalf("expected filtered MinIO ORC row; rows error: %v", rows.Err())
	}
	var id int64
	var value string
	if err := rows.Scan(&id, &value); err != nil {
		t.Fatalf("scan MinIO ORC result: %v", err)
	}
	if id != 2 || value != "foo" {
		t.Fatalf("MinIO ORC row = (%d, %q), want (2, foo)", id, value)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("MinIO ORC query returned extra rows or error: %v", rows.Err())
	}
	_ = rows.Close()

	pagedRows, err := db.QueryContext(context.Background(), "SELECT userid FROM records ORDER BY userid LIMIT 1 OFFSET 1")
	if err != nil {
		t.Fatalf("query MinIO ORC page: %v", err)
	}
	if !pagedRows.Next() {
		_ = pagedRows.Close()
		t.Fatalf("expected a MinIO ORC row after offset; rows error: %v", pagedRows.Err())
	}
	var pageID int64
	if err := pagedRows.Scan(&pageID); err != nil {
		_ = pagedRows.Close()
		t.Fatalf("scan MinIO ORC page: %v", err)
	}
	if pagedRows.Next() || pagedRows.Err() != nil {
		_ = pagedRows.Close()
		t.Fatalf("MinIO ORC page returned extra rows or error: %v", pagedRows.Err())
	}
	_ = pagedRows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM records").Scan(&count); err != nil {
		t.Fatalf("count MinIO ORC rows: %v", err)
	}
	if count != 25000 {
		t.Fatalf("MinIO ORC count = %d, want 25000", count)
	}
	if _, err := db.ExecContext(context.Background(), "DELETE FROM records"); err == nil {
		t.Fatal("MinIO ORC source accepted a write")
	}
}
