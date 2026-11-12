package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	queryservice "github.com/RhinoLink-Tech/data-fabric/internal/service"
	unifiedservice "github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// This optional contract test is only enabled with an explicitly supplied
// PostgreSQL test database. Every created identifier includes a random suffix.
func TestPostgresListAndDescribeConfiguredSchemaContract(t *testing.T) {
	dsn := os.Getenv("DATA_FABRIC_POSTGRES_SCHEMA_TEST_DSN")
	if dsn == "" {
		t.Skip("set DATA_FABRIC_POSTGRES_SCHEMA_TEST_DSN to an isolated PostgreSQL test database")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "postgres" || u.Hostname() == "" || u.Port() == "" || u.User == nil {
		t.Fatal("test DSN must be a postgres URL with host, port, and user")
	}
	pass, ok := u.User.Password()
	if !ok {
		t.Fatal("test DSN must include a password")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal("invalid test database port")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("test database is unavailable: %v", err)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	schema := "df_sales_" + suffix
	shared := "df_orders_" + suffix
	salesOnly := "df_sales_only_" + suffix
	publicOnly := "df_public_only_" + suffix
	quote := pq.QuoteIdentifier
	statements := []string{
		"CREATE SCHEMA " + quote(schema),
		"CREATE TABLE " + quote(schema) + "." + quote(shared) + " (sales_marker integer)",
		"CREATE TABLE " + quote(schema) + "." + quote(salesOnly) + " (id integer)",
		"CREATE TABLE public." + quote(shared) + " (public_marker integer)",
		"CREATE TABLE public." + quote(publicOnly) + " (id integer)",
	}
	t.Cleanup(func() {
		for _, statement := range []string{
			"DROP TABLE IF EXISTS public." + quote(shared),
			"DROP TABLE IF EXISTS public." + quote(publicOnly),
			"DROP SCHEMA IF EXISTS " + quote(schema) + " CASCADE",
		} {
			if _, err := db.ExecContext(context.Background(), statement); err != nil {
				t.Errorf("clean up isolated fixture: %v", err)
			}
		}
	})
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("create isolated fixture: %v", err)
		}
	}

	source := activeOwnedSource()
	source.ID = uuid.NewString()
	source.Config = model.DataSourceConfig{Host: u.Hostname(), Port: port, Database: strings.TrimPrefix(u.Path, "/"), Username: u.User.Username(), Password: pass, SSLMode: u.Query().Get("sslmode"), Schema: schema}
	repo := describeTableRepo{source: source}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer pool.CloseAll()
	cfg := &config.Config{Query: config.QueryConfig{ExecutionMode: "pagination"}}
	queries := unifiedservice.NewUnifiedQueryService(queryservice.NewQueryService(repo, pool, false), nil, cfg)
	extractor := metadata.NewMetadataExtractor(pool, repo)
	server := NewMCPServer(queries, repo, extractor)
	caller := &security.Caller{UserID: "alice"}

	list := func() map[string]bool {
		t.Helper()
		callCtx, request := tablePolicyRequest(caller, "list_tables", map[string]any{"datasource_id": source.ID})
		result, err := server.handleListTables(callCtx, request)
		if err != nil || result.IsError {
			t.Fatalf("list tables result = %+v, err = %v", result, err)
		}
		var response model.FetchQueryResponse
		if err := json.Unmarshal([]byte(describeTableText(t, result)), &response); err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, entry := range response.Entries {
			if len(*entry) == 1 {
				if name, ok := (*entry)[0].(string); ok {
					found[name] = true
				}
			}
		}
		return found
	}
	found := list()
	if !found[shared] || !found[salesOnly] || found[publicOnly] {
		t.Fatalf("configured schema discovery = %v", found)
	}
	callCtx, request := describeTableRequest(t, caller, source.ID, shared, schema)
	result, err := server.handleDescribeTable(callCtx, request)
	if err != nil || result.IsError {
		t.Fatalf("describe result = %+v, err = %v", result, err)
	}
	text := describeTableText(t, result)
	if !strings.Contains(text, "sales_marker") || strings.Contains(text, "public_marker") {
		t.Fatalf("wrong schema described: %s", text)
	}
	source.Config.Schema = ""
	found = list()
	if !found[shared] || !found[publicOnly] || found[salesOnly] {
		t.Fatalf("default schema discovery = %v", found)
	}
}
