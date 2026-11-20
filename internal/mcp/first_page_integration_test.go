package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	service "github.com/RhinoLink-Tech/data-fabric/internal/service"
	unified "github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
)

// Calls the actual MCP handlers through unified query and both service paths.
// The S3-compatible HTTP fixture serves only synthetic CSV and runs locally.
func TestMCPFirstPageAndContinuations(t *testing.T) {
	var csv strings.Builder
	csv.WriteString("id\n")
	for i := 1; i <= 205; i++ {
		fmt.Fprintf(&csv, "%d\n", i)
	}
	payload := csv.String()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bucket/data.csv" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			return
		}
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, payload)
			return
		}
		http.Error(w, "unsupported", http.StatusMethodNotAllowed)
	}))
	defer fixture.Close()
	source := &model.DataSource{ID: "preview-fixture", Type: model.DatabaseTypeS3CSV, Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{Host: "data.csv", Port: 443, Database: "bucket", Username: "synthetic", Password: "synthetic", AdditionalProps: map[string]interface{}{"object_key": "data.csv", "table_name": "records", "region": "us-east-1", "endpoint_url": fixture.URL, "force_path_style": true}}}
	for _, mode := range []string{"pagination", "streaming"} {
		for _, initialSize := range []int{0, 10000} {
			t.Run(fmt.Sprintf("%s/%d", mode, initialSize), func(t *testing.T) {
				pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
				defer pool.CloseAll()
				repo := describeTableRepo{source: source}
				translator := sql_translator.NewSQLTranslationManager(false)
				pagination := service.NewQueryService(repo, pool, false, translator)
				streaming := service.NewStreamingService(repo, pool, translator)
				defer streaming.Shutdown()
				queries := unified.NewUnifiedQueryService(pagination, streaming, &config.Config{Query: config.QueryConfig{ExecutionMode: mode}})
				server := NewMCPServer(queries, repo, nil, translator)
				args := map[string]any{"datasource_id": source.ID, "sql": "SELECT id FROM records ORDER BY id", "execution_mode": mode}
				if initialSize != 0 {
					args["batch_size"] = initialSize
				}
				result, err := server.handleExecuteSQLQuery(context.Background(), callToolRequest("execute_sql_query", args))
				if err != nil || result.IsError {
					t.Fatalf("initial tool result=%+v err=%v", result, err)
				}
				var page model.FetchQueryResponse
				if err := json.Unmarshal([]byte(describeTableText(t, result)), &page); err != nil {
					t.Fatal(err)
				}
				wantSize := 100
				wantPages := []int{100, 100, 5}
				if initialSize != 0 {
					wantSize = initialSize
					wantPages = []int{205}
				}
				if page.AppliedBatchSize != wantSize {
					t.Fatalf("applied size %d, want %d", page.AppliedBatchSize, wantSize)
				}
				seen := 0
				for index, want := range wantPages {
					if len(page.Entries) != want {
						t.Fatalf("page %d has %d rows, want %d", index, len(page.Entries), want)
					}
					for _, row := range page.Entries {
						seen++
						if got, ok := (*row)[0].(float64); !ok || got != float64(seen) {
							t.Fatalf("row %d: %v", seen, *row)
						}
					}
					if index+1 < len(wantPages) {
						if page.NextURI == "" {
							t.Fatalf("page %d lacks continuation", index)
						}
						request := callToolRequest("fetch_next_batch", map[string]any{"query_id": page.QueryID, "slug": page.Slug, "token": page.Token})
						result, err = server.handleFetchNextBatch(context.Background(), request)
						if err != nil || result.IsError {
							t.Fatalf("continuation result=%+v err=%v", result, err)
						}
						page = model.FetchQueryResponse{}
						if err := json.Unmarshal([]byte(describeTableText(t, result)), &page); err != nil {
							t.Fatal(err)
						}
					}
				}
				if seen != 205 || page.NextURI != "" {
					t.Fatalf("seen=%d next=%q", seen, page.NextURI)
				}
			})
		}
	}
}
