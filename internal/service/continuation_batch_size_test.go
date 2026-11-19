package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestContinuationRetainsEffectivePageSizeForBothModes(t *testing.T) {
	const data = "id\n1\n2\n3\n4\n5\n6\n7\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bucket/data.csv" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, data)
			return
		}
		http.Error(w, "unsupported", 405)
	}))
	defer server.Close()
	source := &model.DataSource{ID: "bounded-size-fixture", Type: model.DatabaseTypeS3CSV, Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{Host: "data.csv", Port: 443, Database: "bucket", Username: "synthetic", Password: "synthetic", AdditionalProps: map[string]interface{}{"object_key": "data.csv", "table_name": "records", "region": "us-east-1", "endpoint_url": server.URL, "force_path_style": true}}}
	for _, mode := range []string{"pagination", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
			defer pool.CloseAll()
			repo := federationSourceRepository{source: source}
			ctx := context.Background()
			req := &model.FetchQueryRequest{DataSourceID: source.ID, SQL: "SELECT id FROM records ORDER BY id", Type: 1, BatchSize: 2}
			var page *model.FetchQueryResponse
			var err error
			var next func(*model.FetchQueryResponse, int) (*model.FetchQueryResponse, error)
			if mode == "pagination" {
				q := NewQueryService(repo, pool, false, newTestSQLTranslationManager())
				page, err = q.FetchQuery(ctx, nil, req)
				next = func(p *model.FetchQueryResponse, n int) (*model.FetchQueryResponse, error) {
					return q.FetchNextBatch(ctx, nil, p.QueryID, p.Slug, p.Token, n)
				}
			} else {
				q := NewStreamingService(repo, pool, newTestSQLTranslationManager())
				defer q.Shutdown()
				page, err = q.FetchWithStreaming(ctx, nil, req)
				next = func(p *model.FetchQueryResponse, n int) (*model.FetchQueryResponse, error) {
					return q.FetchNextBatchWithStreaming(ctx, nil, p.QueryID, p.Slug, p.Token, n)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			wants := []int{2, 2, 1, 1, 1}
			requested := []int{0, 1, 0, 0}
			ids := []int64{}
			for i, want := range wants {
				if len(page.Entries) != want {
					t.Fatalf("page %d has %d rows, want %d", i, len(page.Entries), want)
				}
				for _, entry := range page.Entries {
					ids = append(ids, (*entry)[0].(int64))
				}
				if i < len(requested) {
					if page.NextURI == "" {
						t.Fatalf("page %d lacks continuation", i)
					}
					expectedSize := 2
					if i >= 2 {
						expectedSize = 1
					}
					if !strings.Contains(page.NextURI, fmt.Sprintf("batch_size=%d", expectedSize)) {
						t.Fatalf("page %d URI %q has wrong effective size", i, page.NextURI)
					}
					page, err = next(page, requested[i])
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if fmt.Sprint(ids) != "[1 2 3 4 5 6 7]" || page.NextURI != "" {
				t.Fatalf("rows %v, continuation %q", ids, page.NextURI)
			}
		})
	}
}
