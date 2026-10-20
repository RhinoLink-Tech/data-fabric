package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	service "github.com/RhinoLink-Tech/data-fabric/internal/service"
)

func TestStreamableHTTPAuthenticationAndDataSourceScope(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	jwtManager := security.NewJWTManager(secret, time.Hour)
	source := &model.DataSource{ID: "source-1", OwnerID: "alice", TenantID: "team-a"}
	queryService := &mcpHTTPQueryService{source: source}
	server := NewMCPServer(queryService, mcpHTTPRepository{}, nil)
	httpServer := httptest.NewServer(server.StreamableHTTPHandler(jwtManager))
	defer httpServer.Close()

	t.Run("missing bearer token is rejected before MCP", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, httpServer.URL+"/mcp", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") != "Bearer" {
			t.Fatalf("response = %d, WWW-Authenticate=%q; want 401 Bearer challenge", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
		}
	})

	t.Run("invalid bearer token is rejected", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, httpServer.URL+"/mcp", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer not-a-jwt")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("expired bearer token is rejected", func(t *testing.T) {
		expiredManager := security.NewJWTManager(secret, -time.Minute)
		token, err := expiredManager.GenerateToken("alice", "team-a", "Alice", nil)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodGet, httpServer.URL+"/mcp", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})

	callQuery := func(t *testing.T, userID, tenantID string) (*mcp.CallToolResult, *security.Caller) {
		t.Helper()
		token, err := jwtManager.GenerateToken(userID, tenantID, userID, nil)
		if err != nil {
			t.Fatal(err)
		}
		client, err := client.NewStreamableHttpClient(httpServer.URL+"/mcp", transport.WithHTTPHeaders(map[string]string{
			"Authorization": "Bearer " + token,
		}))
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		ctx := context.Background()
		if err := client.Start(ctx); err != nil {
			t.Fatal(err)
		}
		_, err = client.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "test-client", Version: "1"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
			Name: "execute_sql_query",
			Arguments: map[string]any{
				"datasource_id": source.ID,
				"sql":           "SELECT 1",
			},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return result, queryService.lastCaller
	}

	t.Run("foreign data source is denied", func(t *testing.T) {
		result, caller := callQuery(t, "bob", "team-b")
		if !result.IsError {
			t.Fatal("query from a caller outside the data-source scope was accepted")
		}
		if caller == nil || caller.UserID != "bob" || caller.TenantID != "team-b" {
			t.Fatalf("handler caller = %+v, want bob/team-b", caller)
		}
	})

	t.Run("same-tenant query is allowed", func(t *testing.T) {
		result, caller := callQuery(t, "bob", "team-a")
		if result.IsError {
			t.Fatalf("same-tenant query returned an MCP error: %+v", result.Content)
		}
		if caller == nil || caller.UserID != "bob" || caller.TenantID != "team-a" {
			t.Fatalf("handler caller = %+v, want bob/team-a", caller)
		}
	})

	t.Run("owner query is allowed", func(t *testing.T) {
		result, caller := callQuery(t, "alice", "team-a")
		if result.IsError {
			t.Fatalf("owner query returned an MCP error: %+v", result.Content)
		}
		if caller == nil || caller.UserID != "alice" || caller.TenantID != "team-a" {
			t.Fatalf("handler caller = %+v, want alice/team-a", caller)
		}
	})
}

type mcpHTTPQueryService struct {
	source     *model.DataSource
	lastCaller *security.Caller
}

func (s *mcpHTTPQueryService) ValidateQuery(_ context.Context, caller *security.Caller, _ *model.QueryRequest) error {
	s.lastCaller = caller
	if !caller.CanRead(s.source) {
		return security.ErrDataSourceAccessDenied
	}
	return nil
}
func (*mcpHTTPQueryService) GetQueryStats(context.Context) (*model.QueryStats, error) {
	return nil, nil
}
func (s *mcpHTTPQueryService) FetchQuery(_ context.Context, caller *security.Caller, _ *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	s.lastCaller = caller
	if !caller.CanRead(s.source) {
		return nil, security.ErrDataSourceAccessDenied
	}
	return &model.FetchQueryResponse{}, nil
}
func (*mcpHTTPQueryService) FetchNextBatch(context.Context, *security.Caller, string, string, string, int) (*model.FetchQueryResponse, error) {
	return nil, nil
}
func (*mcpHTTPQueryService) GetQueryHistory(context.Context, service.QueryHistoryFilter) (*service.QueryHistoryPage, error) {
	return nil, nil
}

type mcpHTTPRepository struct{}

func (mcpHTTPRepository) Create(context.Context, *model.DataSource) error { return nil }
func (mcpHTTPRepository) GetByID(context.Context, string) (*model.DataSource, error) {
	return nil, errors.New("unused")
}
func (mcpHTTPRepository) GetByName(context.Context, string, string) (*model.DataSource, error) {
	return nil, errors.New("unused")
}
func (mcpHTTPRepository) GetAll(context.Context, *repository.AccessScope, model.DataSourceStatus, int, int) ([]*model.DataSource, int64, error) {
	return nil, 0, nil
}
func (mcpHTTPRepository) Update(context.Context, *model.DataSource) error { return nil }
func (mcpHTTPRepository) Delete(context.Context, string) error            { return nil }
func (mcpHTTPRepository) Activate(context.Context, string) error          { return nil }
func (mcpHTTPRepository) Deactivate(context.Context, string) error        { return nil }
func (mcpHTTPRepository) SetError(context.Context, string) error          { return nil }
func (mcpHTTPRepository) GetActiveByType(context.Context, model.DatabaseType) ([]*model.DataSource, error) {
	return nil, nil
}
func (mcpHTTPRepository) CountByStatus(context.Context, *repository.AccessScope) (map[model.DataSourceStatus]int64, error) {
	return nil, nil
}
func (mcpHTTPRepository) MigrateCredentials(context.Context) (int64, error) { return 0, nil }
