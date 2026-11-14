package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/mcp"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	unifiedservice "github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
	"github.com/RhinoLink-Tech/data-fabric/internal/workbuddy/onboardingruntime"
	sdk "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	protocol "github.com/mark3labs/mcp-go/mcp"
	"golang.org/x/oauth2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Optional isolated Keycloak integration; token and CA must be synthetic and
// point to a disposable realm, never to the deployed service.
func TestSelfServiceRealKeycloakMCPInitialize(t *testing.T) {
	issuer := os.Getenv("DF_SELF_SERVICE_TEST_ISSUER")
	if issuer == "" {
		t.Skip("isolated Keycloak not configured")
	}
	cert, err := os.ReadFile(os.Getenv("DF_SELF_SERVICE_TEST_CA"))
	if err != nil {
		t.Fatal("test CA unavailable")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cert) {
		t.Fatal("test CA invalid")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	verifier, err := security.NewOIDCTokenValidatorWithHTTPClient(context.Background(), security.OIDCTokenConfig{Issuer: issuer, Audience: "data-fabric-mcp", RequiredScopes: []string{"openid"}}, client)
	if err != nil {
		t.Fatal("test issuer validation unavailable")
	}
	raw, err := os.ReadFile(os.Getenv("DF_SELF_SERVICE_TEST_TOKEN"))
	if err != nil {
		t.Fatal("test token unavailable")
	}
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null default '', tenant_id text not null default '', created_at datetime, updated_at datetime, UNIQUE(tenant_id,name))").Error; err != nil {
		t.Fatal(err)
	}
	if err = onboarding.MigrateGrants(db); err != nil {
		t.Fatal(err)
	}
	if err = auditlog.Migrate(db); err != nil {
		t.Fatal(err)
	}
	vault, _ := security.NewCredentialVault([]byte(strings.Repeat("k", 32)))
	port, err := strconv.Atoi(os.Getenv("DF_SELF_SERVICE_TEST_SOURCE_PORT"))
	if err != nil || port < 1 {
		t.Fatal("isolated PostgreSQL port unavailable")
	}
	policy, err := database.NewFixedIPEgressPolicy([]string{"127.0.0.1/32"}, []int{port})
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := repository.NewDataSourceRepository(db, vault)
	sample := &model.DataSource{Name: "Template", Type: model.DatabaseTypePostgreSQL, Status: model.DataSourceStatusActive, OwnerID: "operator", TenantID: "original", Config: model.DataSourceConfig{Host: "127.0.0.1", Port: port, Database: "synthetic_orders", Username: "demo_reader", Password: "synthetic-password"}}
	if err = repo.Create(context.Background(), sample); err != nil {
		t.Fatal(err)
	}
	v := &selfServiceValidator{next: verifier, issuer: issuer, provision: func(ctx context.Context, subject, tenant string) error {
		return provisionSelfServiceGrant(ctx, db, issuer, subject, tenant, strings.Repeat("k", 32), func(ctx context.Context, tx *gorm.DB) error {
			_, err := seedSyntheticForSubject(ctx, tx, vault, policy, issuer, subject, tenant, sample.ID, "self-service", strings.Repeat("k", 32))
			return err
		})
	}}
	connPool, err := database.NewRestrictedConnectionPool(database.GetDriverRegistry(), nil, vault, false, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer connPool.CloseAll()
	pagination, streaming := service.NewGatewayQueryServices(repo, connPool, false, nil, nil, nil, time.Minute, nil)
	defer pagination.Shutdown()
	defer streaming.Shutdown()
	cfg := &config.Config{}
	cfg.Query.ExecutionMode = "pagination"
	m := mcp.NewMCPServer(unifiedservice.NewUnifiedQueryService(pagination, streaming, cfg), repo, nil)
	m.SetDataSourceManagementService(service.NewDataSourceService(repo, connPool, database.NewHealthChecker(connPool)))
	idVerifier, err := security.NewOIDCIDTokenVerifier(context.Background(), issuer, "trial-client", client)
	if err != nil {
		t.Fatal("identity verifier unavailable")
	}
	runtime, err := onboardingruntime.New(context.Background(), onboardingruntime.Options{
		Enabled: true, Origin: "https://gateway.example", MCPIssuer: issuer, AuditKey: strings.Repeat("k", 32),
		CIDRs: []string{"127.0.0.1/32"}, Ports: []int{port}, DB: db, Vault: vault,
		Validator: database.NewHealthChecker(connPool), MCP: m, Fallback: http.NotFoundHandler(),
		OAuth:    oauth2.Config{ClientID: "trial-client", RedirectURL: "https://gateway.example/onboarding/auth/callback", Scopes: []string{"openid"}, Endpoint: oauth2.Endpoint{AuthURL: issuer + "/protocol/openid-connect/auth", TokenURL: issuer + "/protocol/openid-connect/token"}},
		Verifier: idVerifier, HTTPClient: client, Pool: connPool, Policy: policy,
	})
	if err != nil {
		t.Fatal("onboarding runtime unavailable:", err)
	}
	defer runtime.Close()
	handler := mcp.StrictTenantHTTPHandler(m.StreamableHTTPHandler(v))
	payload := []byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-03-26\",\"capabilities\":{},\"clientInfo\":{\"name\":\"selfservice-test\",\"version\":\"1\"}}}")
	req := httptest.NewRequest("POST", "https://gateway.example/mcp", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("MCP initialize returned %d", rec.Code)
	}
	var count int64
	if err = db.Table("data_sources").Where("tenant_id <> ?", "original").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("sample count=%d", count)
	}
	var source model.DataSource
	if err = db.Where("tenant_id <> ?", "original").First(&source).Error; err != nil {
		t.Fatal("sample missing")
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	mc, err := sdk.NewStreamableHttpClient(server.URL+"/mcp", transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + strings.TrimSpace(string(raw))}))
	if err != nil {
		t.Fatal(err)
	}
	defer mc.Close()
	ctx := context.Background()
	if err = mc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = mc.Initialize(ctx, protocol.InitializeRequest{Params: protocol.InitializeParams{ProtocolVersion: protocol.LATEST_PROTOCOL_VERSION, ClientInfo: protocol.Implementation{Name: "selfservice-test", Version: "1"}}}); err != nil {
		t.Fatal(err)
	}
	query, err := mc.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: "execute_sql_query", Arguments: map[string]any{"datasource_id": source.ID, "sql": "SELECT COUNT(*) AS order_count FROM public.orders", "execution_mode": "pagination"}}})
	if err != nil || query.IsError {
		t.Fatalf("sample query failed: %v", err)
	}
	result, err := json.Marshal(query)
	if err != nil || !bytes.Contains(result, []byte("2")) {
		t.Fatal("sample order count missing")
	}
	management, err := mc.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: "rename_data_source", Arguments: map[string]any{"datasource_id": source.ID, "name": "Orders Renamed"}}})
	if err != nil || management.IsError {
		t.Fatal("new user could not manage own source")
	}
	setup, err := mc.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: "begin_data_source_onboarding", Arguments: map[string]any{}}})
	if err != nil || setup.IsError {
		t.Fatal("new user could not start source onboarding")
	}
	otherRaw, err := os.ReadFile(os.Getenv("DF_SELF_SERVICE_TEST_OTHER_TOKEN"))
	if err != nil {
		t.Fatal("second test token unavailable")
	}
	other, err := sdk.NewStreamableHttpClient(server.URL+"/mcp", transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + strings.TrimSpace(string(otherRaw))}))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err = other.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = other.Initialize(ctx, protocol.InitializeRequest{Params: protocol.InitializeParams{ProtocolVersion: protocol.LATEST_PROTOCOL_VERSION, ClientInfo: protocol.Implementation{Name: "second-user-test", Version: "1"}}}); err != nil {
		t.Fatal(err)
	}
	foreign, err := other.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: "execute_sql_query", Arguments: map[string]any{"datasource_id": source.ID, "sql": "SELECT COUNT(*) FROM public.orders", "execution_mode": "pagination"}}})
	if err != nil || !foreign.IsError {
		t.Fatal("another user accessed the first user's source")
	}
	foreignManage, err := other.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: "rename_data_source", Arguments: map[string]any{"datasource_id": source.ID, "name": "Wrong User"}}})
	if err != nil || !foreignManage.IsError {
		t.Fatal("another user managed the first user's source")
	}
	listed, err := other.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: "list_data_sources", Arguments: map[string]any{}}})
	if err != nil || listed.IsError {
		t.Fatal("second user's source listing failed")
	}
	listBytes, _ := json.Marshal(listed)
	if bytes.Contains(listBytes, []byte(source.ID)) {
		t.Fatal("another user saw the first user's source")
	}
}
