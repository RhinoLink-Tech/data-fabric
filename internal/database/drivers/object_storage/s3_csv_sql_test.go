package object_storage_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const s3CSVFixture = "id,name,active\n1,Alice,true\n2,Bob,false\n3,Cara,true\n4,Dana,true\n"

func TestS3CSVQueryThroughConnectionPool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo-bucket/data.csv" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprint(len(s3CSVFixture)))
			w.Header().Set("ETag", "\"fixture\"")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
			return
		}
		_, _ = io.WriteString(w, s3CSVFixture)
	}))
	defer server.Close()

	ds := &model.DataSource{
		ID:   "s3-csv-test",
		Type: model.DatabaseTypeS3CSV,
		Config: model.DataSourceConfig{
			Host:     "data.csv",
			Port:     443,
			Database: "demo-bucket",
			Username: "test-access-key",
			Password: "test-secret-key",
			AdditionalProps: map[string]interface{}{
				"region":           "us-east-1",
				"object_key":       "data.csv",
				"table_name":       "records",
				"endpoint_url":     server.URL,
				"force_path_style": true,
				"has_header":       true,
			},
		},
	}

	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer pool.CloseAll()
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&ds.Config, ds.Type); err != nil {
		t.Fatalf("validate S3 CSV source config: %v", err)
	}
	health, err := checker.CheckDataSourceConnectivity(context.Background(), &ds.Config, ds.Type)
	if err != nil {
		t.Fatalf("check S3 CSV connectivity: %v", err)
	}
	if health.Status != "healthy" {
		t.Fatalf("connectivity status = %q, want healthy (message: %s)", health.Status, health.Message)
	}
	db, err := pool.GetConnection(context.Background(), ds)
	if err != nil {
		t.Fatalf("open configured S3 CSV source: %v", err)
	}

	query := "SELECT name AS person, id FROM records WHERE active = true AND id >= 2 ORDER BY id DESC"
	pageSQL, err := pool.ApplyBatchPagination(model.DatabaseTypeS3CSV, query, 1, 1)
	if err != nil {
		t.Fatalf("apply batch pagination: %v", err)
	}
	rows, err := db.QueryContext(context.Background(), pageSQL)
	if err != nil {
		t.Fatalf("execute paged SQL: %v", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("read result columns: %v", err)
	}
	if got := strings.Join(columns, ","); got != "person,id" {
		t.Fatalf("columns = %q, want person,id", got)
	}
	if !rows.Next() {
		t.Fatalf("expected one paged row, rows error: %v", rows.Err())
	}
	var person string
	var id int64
	if err := rows.Scan(&person, &id); err != nil {
		t.Fatalf("scan paged row: %v", err)
	}
	if person != "Cara" || id != 3 {
		t.Fatalf("row = (%q, %d), want (Cara, 3)", person, id)
	}
	if rows.Next() {
		t.Fatal("received more than one row for a one-row page")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate paged rows: %v", err)
	}

	countRows, err := db.QueryContext(context.Background(), "SELECT COUNT(*) FROM (SELECT id FROM records WHERE active = true) AS t")
	if err != nil {
		t.Fatalf("execute count wrapper: %v", err)
	}
	defer countRows.Close()
	if !countRows.Next() {
		t.Fatalf("expected a count row, rows error: %v", countRows.Err())
	}
	var count int64
	if err := countRows.Scan(&count); err != nil {
		t.Fatalf("scan count row: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}

	filteredRows, err := db.QueryContext(context.Background(), "SELECT id FROM records WHERE id IN (1, 3) AND name LIKE 'C%' ORDER BY id")
	if err != nil {
		t.Fatalf("execute IN and LIKE filters: %v", err)
	}
	defer filteredRows.Close()
	if !filteredRows.Next() {
		t.Fatalf("expected one row matching IN and LIKE, rows error: %v", filteredRows.Err())
	}
	var filteredID int64
	if err := filteredRows.Scan(&filteredID); err != nil {
		t.Fatalf("scan filtered row: %v", err)
	}
	if filteredID != 3 || filteredRows.Next() {
		t.Fatalf("IN and LIKE filters returned unexpected rows; first id=%d, rows error=%v", filteredID, filteredRows.Err())
	}
}

func TestS3CSVQueryHonorsCancellation(t *testing.T) {
	getStarted := make(chan struct{})
	var getStartedOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		getStartedOnce.Do(func() { close(getStarted) })
		<-r.Context().Done()
	}))
	defer server.Close()

	ds := &model.DataSource{
		ID:   "s3-csv-cancel-test",
		Type: model.DatabaseTypeS3CSV,
		Config: model.DataSourceConfig{
			Host: "data.csv", Port: 443, Database: "demo-bucket", Username: "test", Password: "secret",
			AdditionalProps: map[string]interface{}{"endpoint_url": server.URL, "force_path_style": true, "object_key": "data.csv", "table_name": "records"},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer pool.CloseAll()
	db, err := pool.GetConnection(context.Background(), ds)
	if err != nil {
		t.Fatalf("open configured S3 CSV source: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-getStarted
		cancel()
	}()
	if _, err := db.QueryContext(ctx, "SELECT * FROM records"); err == nil {
		t.Fatal("expected in-flight object download to stop after cancellation")
	}
}
