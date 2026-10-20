package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
)

type federationSourceRepository struct {
	repository.DataSourceRepository
	source *model.DataSource
}

func (r federationSourceRepository) GetByID(_ context.Context, id string) (*model.DataSource, error) {
	if id != r.source.ID {
		return nil, repository.ErrDataSourceNotFound
	}
	return r.source, nil
}

func TestFetchQueryRoutesFederatedRequestAndNormalizesResult(t *testing.T) {
	queries := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/statement" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("X-Trino-User"); got != "data-fabric" {
			t.Errorf("X-Trino-User = %q", got)
		}
		if got := r.Header.Get("X-Trino-Catalog"); got != "lakehouse" {
			t.Errorf("X-Trino-Catalog = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		query := string(body)
		queries <- query

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(strings.ToUpper(query), "COUNT(*)"):
			_, _ = fmt.Fprint(w, "{\"id\":\"count-query\",\"columns\":[{\"name\":\"count\",\"type\":\"bigint\",\"typeSignature\":{\"rawType\":\"bigint\"}}],\"data\":[[2]],\"stats\":{\"state\":\"FINISHED\"}}")
		case strings.Contains(query, "postgresql.crm.customers"):
			_, _ = fmt.Fprint(w, "{\"id\":\"federated-query\",\"columns\":[{\"name\":\"order_id\",\"type\":\"bigint\",\"typeSignature\":{\"rawType\":\"bigint\"}},{\"name\":\"customer_name\",\"type\":\"varchar\",\"typeSignature\":{\"rawType\":\"varchar\"}}],\"data\":[[1,\"Ada\"],[2,\"Lin\"]],\"stats\":{\"state\":\"FINISHED\"}}")
		default:
			_, _ = fmt.Fprint(w, "{\"id\":\"ping-query\",\"columns\":[{\"name\":\"result\",\"type\":\"bigint\",\"typeSignature\":{\"rawType\":\"bigint\"}}],\"data\":[[1]],\"stats\":{\"state\":\"FINISHED\"}}")
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
		ID: "d377b04b-78b7-4f8f-9fd6-6cf29e5581f1", Name: "federation",
		Type: model.DatabaseTypeTrino, Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{Host: endpoint.Hostname(), Port: port, Database: "lakehouse", Schema: "sales", Username: "data-fabric"},
	}
	database.GetDriverRegistry()
	pool := database.NewConnectionPool(nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	queryService := NewQueryService(federationSourceRepository{source: source}, pool, false, newTestSQLTranslationManager())

	req := &model.FetchQueryRequest{
		DataSourceID: source.ID,
		SQL: "SELECT o.order_id, c.customer_name FROM lakehouse.sales.orders o " +
			"JOIN postgresql.crm.customers c ON o.customer_id = c.customer_id",
		SourceDialect: "trino",
		BatchSize:     10,
		Type:          1,
	}
	response, err := queryService.FetchQuery(context.Background(), nil, req)
	if err != nil {
		t.Fatalf("FetchQuery() error = %v", err)
	}
	if response.TotalCount != 2 || len(response.Entries) != 2 {
		t.Fatalf("unexpected result size: total=%d entries=%d", response.TotalCount, len(response.Entries))
	}
	if len(response.Columns) != 2 || response.Columns[0].Type != model.TypeBigInt || response.Columns[1].Type != model.TypeString {
		t.Fatalf("unexpected normalized columns: %+v", response.Columns)
	}
	if got := *response.Entries[0]; len(got) != 2 || got[0] != int64(1) || got[1] != "Ada" {
		t.Fatalf("unexpected first row: %#v", got)
	}
	if response.QueryID == "" || response.Slug == "" || response.Token == "" {
		t.Fatalf("query response omitted existing session fields: %+v", response)
	}

	foundFederatedQuery, foundCountQuery := false, false
	for len(queries) > 0 {
		query := <-queries
		if strings.Contains(query, "postgresql.crm.customers") {
			foundFederatedQuery = true
		}
		if strings.Contains(strings.ToUpper(query), "COUNT(*)") {
			foundCountQuery = true
		}
	}
	if !foundFederatedQuery || foundCountQuery {
		t.Fatalf("partial page should return its exact count without a count query: federated=%v count=%v", foundFederatedQuery, foundCountQuery)
	}

	// A full page is ambiguous: there may be more rows, so retain the exact
	// count query and its existing pagination behavior.
	req.BatchSize = len(response.Entries)
	fullPageResponse, err := queryService.FetchQuery(context.Background(), nil, req)
	if err != nil {
		t.Fatalf("FetchQuery() for a full page error = %v", err)
	}
	if fullPageResponse.TotalCount != 2 {
		t.Fatalf("full-page total count = %d, want 2", fullPageResponse.TotalCount)
	}

	foundCountQuery = false
	for len(queries) > 0 {
		if strings.Contains(strings.ToUpper(<-queries), "COUNT(*)") {
			foundCountQuery = true
		}
	}
	if !foundCountQuery {
		t.Fatal("full page did not request an exact count")
	}
}
