package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/golang-jwt/jwt/v5"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type manageableRepoStub struct {
	repository.DataSourceRepository
	calls int
	scope repository.SourceWriteScope
	err   error
}

func (s *manageableRepoStub) ListManageableSources(_ context.Context, scope repository.SourceWriteScope, limit, offset int) ([]repository.ManageableSource, int64, error) {
	s.calls++
	s.scope = scope
	if s.err != nil {
		return nil, 0, s.err
	}
	if offset > 0 {
		return nil, 1, nil
	}
	return []repository.ManageableSource{{ID: "source", Name: "orders", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, OwnerID: "alice", TenantID: "tenant"}}, 1, nil
}

func TestManageableSourceToolRequiresScopeAndCurrentGrant(t *testing.T) {
	repo := &manageableRepoStub{}
	server := NewMCPServer(nil, repo, nil)
	if _, ok := server.server.ListTools()["list_manageable_data_sources"]; ok {
		t.Fatal("management inventory must not be advertised before management is enabled")
	}
	server.SetDataSourceManagementService(&managementStub{})
	grant := &testManagementGrant{enabled: true}
	if err := server.SetSourceManagementGrant(grant); err != nil {
		t.Fatal(err)
	}
	tool, ok := server.server.ListTools()["list_manageable_data_sources"]
	if !ok {
		t.Fatal("management inventory not advertised")
	}
	ctx := context.WithValue(context.Background(), callerContextKey{}, &security.Caller{UserID: "alice", TenantID: "tenant", Scopes: []string{sourceManagementScope}})
	ctx = context.WithValue(ctx, verifiedIdentityContextKey{}, verifiedOnboardingIdentity{Issuer: "https://issuer.example", Subject: "alice", Tenant: "tenant"})
	result, err := tool.Handler(ctx, callToolRequest("list_manageable_data_sources", map[string]any{"limit": 1}))
	if err != nil || result == nil || result.IsError || repo.calls != 1 || repo.scope.OwnerID != "alice" || repo.scope.TenantID != "tenant" || repo.scope.Admin {
		t.Fatalf("authorized listing: result=%+v calls=%d scope=%+v err=%v", result, repo.calls, repo.scope, err)
	}
	text := resultText(t, result)
	for _, forbidden := range []string{"owner_id", "tenant_id", "password", "host", "config"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("inventory exposed %s: %s", forbidden, text)
		}
	}
	var payload struct {
		DataSources []struct {
			ID            string `json:"id"`
			OwnedByCaller bool   `json:"owned_by_caller"`
		} `json:"data_sources"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil || len(payload.DataSources) != 1 || payload.DataSources[0].ID != "source" || !payload.DataSources[0].OwnedByCaller || payload.Total != 1 {
		t.Fatalf("invalid inventory payload: %+v err=%v", payload, err)
	}
	for _, bad := range []context.Context{
		context.Background(),
		context.WithValue(ctx, callerContextKey{}, &security.Caller{UserID: "alice", TenantID: "tenant"}),
		context.WithValue(ctx, verifiedIdentityContextKey{}, verifiedOnboardingIdentity{Issuer: "https://issuer.example", Subject: "bob", Tenant: "tenant"}),
	} {
		result, err = tool.Handler(bad, callToolRequest("list_manageable_data_sources", nil))
		if err != nil || result == nil || !result.IsError || repo.calls != 1 {
			t.Fatal("missing or mismatched identity/scope must not list sources")
		}
	}
	grant.enabled = false
	result, err = tool.Handler(ctx, callToolRequest("list_manageable_data_sources", nil))
	if err != nil || result == nil || !result.IsError || repo.calls != 1 {
		t.Fatal("revoked grant must not list sources")
	}
	grant.enabled = true
	for _, args := range []map[string]any{{"limit": 0}, {"offset": -1}, {"limit": 1.5}, {"unknown": "secret-marker"}} {
		result, err = tool.Handler(ctx, callToolRequest("list_manageable_data_sources", args))
		if err != nil || result == nil || !result.IsError || repo.calls != 1 || strings.Contains(resultText(t, result), "secret-marker") {
			t.Fatalf("invalid page accepted or leaked: %+v", args)
		}
	}
	repo.err = errors.New("sensitive backend marker")
	result, err = tool.Handler(ctx, callToolRequest("list_manageable_data_sources", nil))
	if err != nil || result == nil || !result.IsError || strings.Contains(resultText(t, result), "sensitive backend marker") {
		t.Fatal("backend failure must be sanitized")
	}
	if grant.auditCount != 0 {
		t.Fatal("a read-only inventory must not emit mutation audit events")
	}
}

func TestManageableSourceToolAdminOwnershipAndEmptyPage(t *testing.T) {
	repo := &manageableRepoStub{}
	server := NewMCPServer(nil, repo, nil)
	server.SetDataSourceManagementService(&managementStub{})
	tool := server.server.ListTools()["list_manageable_data_sources"]
	ctx := context.WithValue(context.Background(), callerContextKey{}, &security.Caller{
		UserID: "admin", TenantID: "tenant", Roles: []string{security.AdminRole}, Scopes: []string{sourceManagementScope},
	})
	result, err := tool.Handler(ctx, callToolRequest("list_manageable_data_sources", nil))
	if err != nil || result == nil || result.IsError || !repo.scope.Admin {
		t.Fatalf("admin inventory failed: %+v err=%v", result, err)
	}
	var payload struct {
		DataSources []struct {
			OwnedByCaller *bool `json:"owned_by_caller"`
		} `json:"data_sources"`
	}
	if err := json.Unmarshal([]byte(resultText(t, result)), &payload); err != nil || len(payload.DataSources) != 1 || payload.DataSources[0].OwnedByCaller == nil || *payload.DataSources[0].OwnedByCaller {
		t.Fatalf("admin must not be reported as owner of another user's source: %+v err=%v", payload, err)
	}
	result, err = tool.Handler(ctx, callToolRequest("list_manageable_data_sources", map[string]any{"offset": 1}))
	if err != nil || result == nil || result.IsError || !strings.Contains(resultText(t, result), `"data_sources":[]`) {
		t.Fatalf("empty inventory page: %+v err=%v", result, err)
	}
}

func TestManageableSourceToolFiltersBeforePagingWithRealRepository(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:manageable-mcp?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE data_sources (id text primary key, name text, type text, status text, owner_id text, tenant_id text, config text, created_at datetime, deleted_at datetime)").Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, owner, tenant string }{
		{"own-a", "alice", "tenant"}, {"own-b", "alice", "tenant"}, {"shared", "bob", "tenant"}, {"foreign", "alice", "other"},
	} {
		if err := db.Exec("INSERT INTO data_sources (id,name,type,status,owner_id,tenant_id,config,created_at) VALUES (?,?,'mysql','active',?,?,?,?)", row.id, row.id, row.owner, row.tenant, `{"password":"plaintext-marker"}`, "2026-01-01").Error; err != nil {
			t.Fatal(err)
		}
	}
	vault, err := security.NewCredentialVaultWithKeyring("test", map[string][]byte{"test": []byte(strings.Repeat("a", 32))})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := repository.NewDataSourceRepository(db, vault)
	if err != nil {
		t.Fatal(err)
	}
	server := NewMCPServer(nil, repo, nil)
	server.SetDataSourceManagementService(&managementStub{})
	tool := server.server.ListTools()["list_manageable_data_sources"]
	ctx := context.WithValue(context.Background(), callerContextKey{}, &security.Caller{UserID: "alice", TenantID: "tenant", Scopes: []string{sourceManagementScope}})
	for offset, wantID := range map[int]string{0: "own-b", 1: "own-a"} {
		result, err := tool.Handler(ctx, callToolRequest("list_manageable_data_sources", map[string]any{"limit": 1, "offset": offset}))
		if err != nil || result == nil || result.IsError {
			t.Fatalf("page %d failed: %+v %v", offset, result, err)
		}
		var page struct {
			DataSources []struct {
				ID string `json:"id"`
			} `json:"data_sources"`
			Total      int  `json:"total"`
			HasMore    bool `json:"has_more"`
			NextOffset int  `json:"next_offset"`
		}
		text := resultText(t, result)
		if strings.Contains(text, "plaintext-marker") {
			t.Fatal("inventory leaked stored credentials")
		}
		if err := json.Unmarshal([]byte(text), &page); err != nil || len(page.DataSources) != 1 || page.DataSources[0].ID != wantID || page.Total != 2 || page.HasMore != (offset == 0) || (offset == 0 && page.NextOffset != 1) {
			t.Fatalf("incorrect page %d: %+v %v", offset, page, err)
		}
	}
}

func TestManageableSourceToolOverAuthenticatedMCP(t *testing.T) {
	const signingKey = "0123456789abcdef0123456789abcdef"
	repo := &manageableRepoStub{}
	server := NewMCPServer(nil, repo, nil)
	server.SetDataSourceManagementService(&managementStub{})
	httpClient := &http.Client{Transport: mcpHandlerTransport{server.StreamableHTTPHandler(security.NewJWTManager(signingKey, time.Hour))}}
	for _, scope := range []string{"data-fabric.query", sourceManagementScope} {
		claims := &security.Claims{UserID: "alice", TenantID: "tenant", Scope: scope, RegisteredClaims: jwt.RegisteredClaims{
			Subject: "alice", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		}}
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(signingKey))
		if err != nil {
			t.Fatal(err)
		}
		c, err := client.NewStreamableHttpClient(mcpTestURL, transport.WithHTTPBasicClient(httpClient), transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}))
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if err := c.Start(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION, ClientInfo: mcp.Implementation{Name: "test", Version: "1"}}}); err != nil {
			t.Fatal(err)
		}
		result, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "list_manageable_data_sources", Arguments: map[string]any{}}})
		if err != nil || result == nil || result.IsError != (scope != sourceManagementScope) {
			t.Fatalf("scope %s: result=%+v err=%v", scope, result, err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if repo.calls != 1 || repo.scope.OwnerID != "alice" || repo.scope.TenantID != "tenant" {
		t.Fatalf("unexpected authorized calls=%d scope=%+v", repo.calls, repo.scope)
	}
}
