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

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	service "github.com/RhinoLink-Tech/data-fabric/internal/service"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestStreamableHTTPAuthenticationAndDataSourceScope(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	jwtManager := security.NewJWTManager(secret, time.Hour)
	source := &model.DataSource{ID: "source-1", OwnerID: "alice", TenantID: "team-a"}
	queryService := &mcpHTTPQueryService{source: source}
	server := NewMCPServer(queryService, mcpHTTPRepository{}, nil)
	db, err := gorm.Open(sqlite.Open("file:mcp-audit-"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := auditlog.Migrate(db); err != nil {
		t.Fatal(err)
	}
	auditStore, err := auditlog.NewStore(db, secret, 30)
	if err != nil {
		t.Fatal(err)
	}
	server.SetAuditRecorder(auditStore)
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
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
		page, err := auditStore.List(context.Background(), auditlog.Filter{Transport: "mcp-http", Decision: auditlog.DecisionDeny})
		if err != nil || page.Total != 1 || page.Events[0].ReasonCode != "missing_or_invalid_bearer_token" {
			t.Fatalf("missing-token audit page = %+v, error = %v", page, err)
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

	callQuery := func(t *testing.T, userID, tenantID, sql string) (*mcp.CallToolResult, *security.Caller) {
		t.Helper()
		token, err := jwtManager.GenerateToken(userID, tenantID, userID, nil)
		if err != nil {
			t.Fatal(err)
		}
		client, err := client.NewStreamableHttpClient(httpServer.URL+"/mcp", transport.WithHTTPHeaders(map[string]string{
			"Authorization":    "Bearer " + token,
			"X-Correlation-ID": "mcp-request-bob",
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
				"sql":           sql,
			},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return result, queryService.lastCaller
	}

	t.Run("foreign data source is denied", func(t *testing.T) {
		result, caller := callQuery(t, "bob", "team-b", "SELECT 1")
		if !result.IsError {
			t.Fatal("query from a caller outside the data-source scope was accepted")
		}
		if caller == nil || caller.UserID != "bob" || caller.TenantID != "team-b" {
			t.Fatalf("handler caller = %+v, want bob/team-b", caller)
		}
		page, err := auditStore.List(context.Background(), auditlog.Filter{ActorID: "bob", Transport: "mcp-http", Operation: "tools/call execute_sql_query", CorrelationID: "mcp-request-bob"})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 1 || page.Events[0].Decision != auditlog.DecisionDeny || page.Events[0].DataSourceID != source.ID || page.Events[0].QueryFingerprint == "" {
			t.Fatalf("MCP query audit events = %+v, want one denied, identity-aware event", page.Events)
		}
	})

	t.Run("same-tenant query is allowed", func(t *testing.T) {
		result, caller := callQuery(t, "bob", "team-a", "SELECT 1")
		if result.IsError {
			t.Fatalf("same-tenant query returned an MCP error: %+v", result.Content)
		}
		if caller == nil || caller.UserID != "bob" || caller.TenantID != "team-a" {
			t.Fatalf("handler caller = %+v, want bob/team-a", caller)
		}
		page, err := auditStore.List(context.Background(), auditlog.Filter{ActorID: "bob", Transport: "mcp-http", Operation: "tools/call execute_sql_query", CorrelationID: "mcp-request-bob"})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 2 || page.Events[0].Decision != auditlog.DecisionAllow {
			t.Fatalf("MCP success audit events = %+v, want allowed and denied events", page.Events)
		}
	})

	t.Run("owner query is allowed", func(t *testing.T) {
		result, caller := callQuery(t, "alice", "team-a", "SELECT 1")
		if result.IsError {
			t.Fatalf("owner query returned an MCP error: %+v", result.Content)
		}
		if caller == nil || caller.UserID != "alice" || caller.TenantID != "team-a" {
			t.Fatalf("handler caller = %+v, want alice/team-a", caller)
		}
	})

	t.Run("backend query failure is recorded", func(t *testing.T) {
		result, _ := callQuery(t, "bob", "team-a", "SELECT FAIL")
		if !result.IsError {
			t.Fatal("backend failure did not produce an MCP tool error")
		}
		page, err := auditStore.List(context.Background(), auditlog.Filter{ActorID: "bob", Transport: "mcp-http", Operation: "tools/call execute_sql_query", Decision: auditlog.DecisionError})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 1 || page.Events[0].Outcome != "failed" {
			t.Fatalf("MCP failed query audit events = %+v, want one failed event", page.Events)
		}
	})

	t.Run("successful MCP authentication is recorded", func(t *testing.T) {
		page, err := auditStore.List(context.Background(), auditlog.Filter{ActorID: "bob", Transport: "mcp-http", Operation: "mcp.authenticate", Decision: auditlog.DecisionAllow})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total == 0 || page.Events[0].ReasonCode != "token_validated" {
			t.Fatalf("MCP authentication audit events = %+v, want successful verified identity events", page.Events)
		}
	})
}

func TestAuthenticateMCPRequestsRejectsInvalidAccessTokenBeforeHandler(t *testing.T) {
	handlerCalled := false
	handler := authenticateMCPRequests(staticMCPTokenValidator{}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		handlerCalled = true
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer rejected-access-token")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	if handlerCalled {
		t.Fatal("MCP handler ran for a rejected access token")
	}
}

func TestStreamableHTTPPropagatesOIDCIdentityToQueryAuthorization(t *testing.T) {
	source := &model.DataSource{ID: "source-oidc", OwnerID: "alice", TenantID: "tenant-a"}
	queryService := &mcpHTTPQueryService{source: source}
	mcpServer := NewMCPServer(queryService, mcpHTTPRepository{}, nil)
	validator := staticMCPTokenValidator{claims: &security.Claims{
		UserID: "oidc-subject-1", TenantID: "tenant-a", Username: "alice",
		Roles: []string{"reader"}, Scope: "data-fabric.query profile",
	}}
	httpServer := httptest.NewServer(mcpServer.StreamableHTTPHandler(validator))
	defer httpServer.Close()

	client, err := client.NewStreamableHttpClient(httpServer.URL+"/mcp", transport.WithHTTPHeaders(map[string]string{
		"Authorization": "Bearer trusted-access-token",
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "oidc-test-client", Version: "1"},
	}}); err != nil {
		t.Fatal(err)
	}
	result, err := client.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "execute_sql_query",
		Arguments: map[string]any{"datasource_id": source.ID, "sql": "SELECT 1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("OIDC identity was not authorized for the same tenant: %+v", result.Content)
	}
	caller := queryService.lastCaller
	if caller == nil || caller.UserID != "oidc-subject-1" || caller.TenantID != "tenant-a" ||
		len(caller.Roles) != 1 || caller.Roles[0] != "reader" ||
		len(caller.Scopes) != 2 || caller.Scopes[0] != "data-fabric.query" {
		t.Fatalf("query authorization received caller = %+v", caller)
	}
	if queryService.lastQueryUser != "oidc-subject-1" {
		t.Fatalf("query history user = %q, want verified subject", queryService.lastQueryUser)
	}
}

type staticMCPTokenValidator struct {
	claims *security.Claims
}

func (v staticMCPTokenValidator) ValidateMCPToken(_ context.Context, token string) (*security.Claims, error) {
	if token != "trusted-access-token" {
		return nil, errors.New("invalid token")
	}
	return v.claims, nil
}

type mcpHTTPQueryService struct {
	source        *model.DataSource
	lastCaller    *security.Caller
	lastQueryUser string
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
func (s *mcpHTTPQueryService) FetchQuery(ctx context.Context, caller *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	s.lastCaller = caller
	s.lastQueryUser = service.QueryUserFromContext(ctx)
	if !caller.CanRead(s.source) {
		return nil, security.ErrDataSourceAccessDenied
	}
	if req.SQL == "SELECT FAIL" {
		return nil, errors.New("backend unavailable")
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
