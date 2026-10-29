package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"
)

func TestFetchQueryRoutesMinIOCSVThroughGateway(t *testing.T) {
	const csvData = "id,name,active\n1,Ada,true\n2,Lin,false\n3,Grace,true\n"
	getCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo-bucket/data.csv" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodHead:
			w.Header().Set("Content-Length", fmt.Sprint(len(csvData)))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			getCalls++
			_, _ = io.WriteString(w, csvData)
		default:
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	source := &model.DataSource{
		ID: "minio-csv-query-service-test", Type: model.DatabaseTypeMinIOCSV, Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: "data.csv", Database: "demo-bucket", Username: "minio-access-key", Password: "minio-secret-key",
			AdditionalProps: map[string]interface{}{
				"object_key": "data.csv", "table_name": "records", "region": "us-east-1",
				"endpoint_url": server.URL, "force_path_style": true, "has_header": true,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate MinIO CSV source config: %v", err)
	}
	queryService := NewQueryService(federationSourceRepository{source: source}, pool, false, newTestSQLTranslationManager())
	response, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID,
		SQL:          "SELECT id, name FROM records WHERE active = true ORDER BY id DESC",
		Type:         1,
		BatchSize:    10,
	})
	if err != nil {
		t.Fatalf("FetchQuery() error = %v", err)
	}
	if response.TotalCount != 2 || len(response.Entries) != 2 {
		t.Fatalf("response total=%d entries=%d, want two rows", response.TotalCount, len(response.Entries))
	}
	if len(response.Columns) != 2 || response.Columns[0].Name != "id" || response.Columns[0].Type != model.TypeBigInt || response.Columns[1].Name != "name" || response.Columns[1].Type != model.TypeString {
		t.Fatalf("response columns = %+v, want id BIGINT and name STRING", response.Columns)
	}
	assertS3CSVServiceRows(t, response.Entries, []int64{3, 1}, []string{"Grace", "Ada"})

	beforeWrite := getCalls
	if _, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID, SQL: "DELETE FROM records", Type: 1, BatchSize: 10,
	}); err == nil {
		t.Fatal("FetchQuery() accepted a MinIO CSV write")
	}
	if getCalls != beforeWrite {
		t.Fatalf("write rejection accessed the MinIO object: before=%d after=%d", beforeWrite, getCalls)
	}
}

// TestFetchQueryRejectsCSVTableIdentityMismatch pins the execution side of
// table authorization for CSV sources: the gateway serves exactly one table,
// spelled exactly as configured. A policy can grant read on public.records or
// RECORDS as written — serving the configured table through those spellings
// would authorize one identity and execute another, so those queries must
// fail without ever downloading the object. Only the exact identity
// (records, unqualified) may reach the data.
func TestFetchQueryRejectsCSVTableIdentityMismatch(t *testing.T) {
	const csvData = "id,name,active\n1,Ada,true\n2,Lin,false\n3,Grace,true\n"
	getCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo-bucket/data.csv" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodHead:
			w.Header().Set("Content-Length", fmt.Sprint(len(csvData)))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			getCalls++
			_, _ = io.WriteString(w, csvData)
		default:
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	source := &model.DataSource{
		ID: "minio-csv-table-identity-test", Type: model.DatabaseTypeMinIOCSV, OwnerID: "alice", Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: "data.csv", Database: "demo-bucket", Username: "minio-access-key", Password: "minio-secret-key",
			AdditionalProps: map[string]interface{}{
				"object_key": "data.csv", "table_name": "records", "region": "us-east-1",
				"endpoint_url": server.URL, "force_path_style": true, "has_header": true,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer func() { _ = pool.CloseAll() }()

	cases := []struct {
		name     string
		scope    security.TableScope
		tableRef sqlanalysis.TableReference
		sql      string
		wantErr  bool
	}{
		{
			name:     "authorized public.records must not read the configured table",
			scope:    security.TableScope{Schema: "public", Table: "records"},
			tableRef: sqlanalysis.TableReference{Schema: "public", Table: "records"},
			sql:      "SELECT id FROM public.records",
			wantErr:  true,
		},
		{
			name:     "authorized RECORDS must not read the configured table",
			scope:    security.TableScope{Table: "RECORDS"},
			tableRef: sqlanalysis.TableReference{Table: "RECORDS"},
			sql:      "SELECT id FROM RECORDS",
			wantErr:  true,
		},
		{
			name:     "exact configured identity remains readable",
			scope:    security.TableScope{Table: "records"},
			tableRef: sqlanalysis.TableReference{Table: "records"},
			sql:      "SELECT id FROM records",
			wantErr:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The scope below grants exactly what the query asks for, so
			// table authorization itself passes; only execution-side identity
			// can stop the mismatched spellings.
			authorizer := security.NewTableAuthorizer(security.TableAuthorizationConfig{
				Enabled: true,
				DataSources: map[string]security.DataSourceTablePolicy{
					source.ID: {Rules: []security.TableAccessRule{{
						UserID: "alice", Read: []security.TableScope{tc.scope},
					}}},
				},
			})
			service := NewQueryServiceWithTableAuthorizer(
				federationSourceRepository{source: source}, pool, false,
				authorizer, newTestSQLTranslationManager(),
			).(*queryService)
			service.sqlValidator.SetASTAnalyzer(fixedSQLASTAnalyzer{analysis: sqlanalysis.Analysis{
				StatementCount: 1, IsQuery: true,
				Tables: []sqlanalysis.TableReference{tc.tableRef},
			}})

			before := getCalls
			_, err := service.FetchQuery(context.Background(), &security.Caller{UserID: "alice"}, &model.FetchQueryRequest{
				DataSourceID: source.ID, SQL: tc.sql, Type: 1, BatchSize: 10,
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("FetchQuery() error = %v, wantErr %v", err, tc.wantErr)
			}
			downloaded := getCalls - before
			if tc.wantErr && downloaded != 0 {
				t.Fatalf("rejected query downloaded the MinIO object %d times", downloaded)
			}
			if !tc.wantErr && downloaded == 0 {
				t.Fatal("accepted query did not download the MinIO object")
			}
		})
	}
}
