package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestFetchQueryRoutesDruidSQLThroughUnifiedGateway(t *testing.T) {
	var receivedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			_, _ = fmt.Fprint(w, `{"version":"31.0.0"}`)
		case "/druid/v2/sql":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var payload struct {
				Query          string `json:"query"`
				ResultFormat   string `json:"resultFormat"`
				Header         bool   `json:"header"`
				SQLTypesHeader bool   `json:"sqlTypesHeader"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if payload.ResultFormat != "array" || !payload.Header || !payload.SQLTypesHeader {
				http.Error(w, "gateway did not request stable typed rows", http.StatusBadRequest)
				return
			}
			receivedQuery = payload.Query
			_, _ = fmt.Fprint(w, `[["id","name"],["BIGINT","VARCHAR"],[1,"Ada"],[2,"Lin"]]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil {
		t.Fatal(err)
	}
	source := &model.DataSource{
		ID: "a5ee7f77-7c18-4e6b-87d9-e6df930bab4c", Name: "analytics",
		Type: model.DatabaseTypeApacheDruid, Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{Host: endpoint.Hostname(), Port: port, Database: "druid"},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	queryService := NewQueryService(federationSourceRepository{source: source}, pool, false, newTestSQLTranslationManager()).(*queryService)

	response, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID,
		SQL:          "SELECT id, name FROM events",
		BatchSize:    10,
		Type:         1,
	})
	if err != nil {
		t.Fatalf("FetchQuery() error = %v", err)
	}
	if response.TotalCount != 2 || len(response.Entries) != 2 {
		t.Fatalf("unexpected result size: total=%d entries=%d", response.TotalCount, len(response.Entries))
	}
	if len(response.Columns) != 2 || response.Columns[0].Type != model.TypeBigInt || response.Columns[1].Type != model.TypeString {
		t.Fatalf("unexpected normalized columns: %+v", response.Columns)
	}
	if row := *response.Entries[0]; len(row) != 2 || row[0] != int64(1) || row[1] != "Ada" {
		t.Fatalf("unexpected first row: %#v", row)
	}
	if !strings.Contains(receivedQuery, "SELECT id, name FROM events") || !strings.Contains(receivedQuery, "LIMIT 10 OFFSET 0") {
		t.Fatalf("Druid query = %q, want paginated gateway query", receivedQuery)
	}

	if _, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID,
		SQL:          "DELETE FROM events",
		BatchSize:    10,
		Type:         1,
	}); err == nil {
		t.Fatal("FetchQuery() accepted a write query")
	}
	stats, err := queryService.GetQueryStats(context.Background())
	if err != nil {
		t.Fatalf("GetQueryStats() error = %v", err)
	}
	if stats.TotalQueries != 2 || stats.SuccessfulQueries != 1 || stats.FailedQueries != 1 {
		t.Fatalf("unexpected query counters: %+v", stats)
	}
	if stats.QueriesByType[string(model.DatabaseTypeApacheDruid)] != 2 {
		t.Fatalf("database-type aggregation = %#v, want two Druid queries", stats.QueriesByType)
	}
	if stats.QueriesByDataSource[source.ID] != 2 {
		t.Fatalf("data-source aggregation = %#v, want two source queries", stats.QueriesByDataSource)
	}
}

func TestFetchQueryUsesNativeDruidExecutorForBoundedContinuation(t *testing.T) {
	var queryRequests, countRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/druid/v2/sql" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		query := payload["query"].(string)
		if strings.Contains(strings.ToUpper(query), "COUNT(") {
			countRequests++
			_ = json.NewEncoder(w).Encode([][]any{{"count"}, {"BIGINT"}, {2}})
			return
		}
		queryRequests++
		parameters := payload["parameters"].([]interface{})
		parameter := parameters[0].(map[string]interface{})
		if parameter["type"] != "BIGINT" || parameter["value"] != float64(2) {
			http.Error(w, "expected a bound BIGINT parameter", http.StatusBadRequest)
			return
		}
		switch {
		case strings.Contains(query, "LIMIT 1 OFFSET 0"):
			_ = json.NewEncoder(w).Encode([][]any{{"id"}, {"BIGINT"}, {2}})
		case strings.Contains(query, "LIMIT 1 OFFSET 1"):
			_ = json.NewEncoder(w).Encode([][]any{{"id"}, {"BIGINT"}, {3}})
		default:
			http.Error(w, "unexpected paginated query: "+query, http.StatusBadRequest)
		}
	}))
	defer server.Close()

	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil {
		t.Fatal(err)
	}
	source := &model.DataSource{
		ID: "b5ee7f77-7c18-4e6b-87d9-e6df930bab4c", Name: "analytics",
		Type: model.DatabaseTypeApacheDruid, Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{Host: endpoint.Hostname(), Port: port},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	queryService := NewQueryService(federationSourceRepository{source: source}, pool, false, newTestSQLTranslationManager()).(*queryService)

	first, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID, SQL: "SELECT id FROM events WHERE id = ?",
		Parameters: []interface{}{int64(2)}, BatchSize: 1, Type: 2,
	})
	if err != nil {
		t.Fatalf("FetchQuery() error = %v", err)
	}
	if first.TotalCount != 2 || len(first.Entries) != 1 || (*first.Entries[0])[0] != int64(2) || first.NextURI == "" {
		t.Fatalf("unexpected first native batch: %+v", first)
	}
	second, err := queryService.FetchNextBatch(context.Background(), nil, first.QueryID, first.Slug, first.Token, 1)
	if err != nil {
		t.Fatalf("FetchNextBatch() error = %v", err)
	}
	if second.TotalCount != 2 || len(second.Entries) != 1 || (*second.Entries[0])[0] != int64(3) || second.NextURI != "" {
		t.Fatalf("unexpected second native batch: %+v", second)
	}
	if queryRequests != 2 || countRequests != 1 {
		t.Fatalf("native calls: page=%d count=%d, want page=2 count=1", queryRequests, countRequests)
	}
}
