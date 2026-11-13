package onboardingruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/mcp"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	unifiedservice "github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
	"github.com/golang-jwt/jwt/v5"
	sdk "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	protocol "github.com/mark3labs/mcp-go/mcp"
	"golang.org/x/oauth2"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// exchangeBrowser sends browser requests over an actual loopback TLS socket.
func exchangeBrowser(t *testing.T, client *http.Client, origin string, out *httptest.ResponseRecorder, in *http.Request) {
	t.Helper()
	req, err := http.NewRequest(in.Method, origin+in.URL.RequestURI(), in.Body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = in.Header.Clone()
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	for k, v := range res.Header {
		out.Header()[k] = v
	}
	out.WriteHeader(res.StatusCode)
	if _, err = io.Copy(out, res.Body); err != nil {
		t.Fatal(err)
	}
}
func toolJSON(t *testing.T, c *sdk.Client, name string, args map[string]any) map[string]any {
	t.Helper()
	r, e := c.CallTool(context.Background(), protocol.CallToolRequest{Params: protocol.CallToolParams{Name: name, Arguments: args}})
	if e != nil || r.IsError || len(r.Content) == 0 {
		t.Fatalf("tool %s: %v %+v", name, e, r)
	}
	b, e := json.Marshal(r.Content[0])
	if e != nil {
		t.Fatal(e)
	}
	var x struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(b, &x) != nil {
		t.Fatal("invalid content")
	}
	var out map[string]any
	if json.Unmarshal([]byte(x.Text), &out) != nil {
		t.Fatalf("invalid result: %s", x.Text)
	}
	return out
}
func TestSignedOIDCAndMCPBrowserLifecycle(t *testing.T) {
	if os.Getenv("DF_WB_E2E_MYSQL_DSN") == "" {
		t.Skip("isolated MySQL metadata DSN required")
	}
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	var provider *httptest.Server
	var nonce, challenge string
	provider = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{"issuer": provider.URL, "jwks_uri": provider.URL + "/jwks", "authorization_endpoint": provider.URL + "/authorize", "token_endpoint": provider.URL + "/token"})
		case "/jwks":
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "test-key", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/token":
			if r.ParseForm() != nil || r.Form.Get("code") != "synthetic-code" || oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != challenge {
				http.Error(w, "bad code", 400)
				return
			}
			now := time.Now()
			j := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": provider.URL, "sub": "alice", "aud": "browser-client", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "nonce": nonce})
			j.Header["kid"] = "test-key"
			signed, e := j.SignedString(key)
			if e != nil {
				t.Error(e)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": "opaque", "token_type": "Bearer", "id_token": signed})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	ctx := context.Background()

	var db *gorm.DB
	if dsn := os.Getenv("DF_WB_E2E_MYSQL_DSN"); dsn != "" {
		db, e = gorm.Open(mysql.Open(dsn), &gorm.Config{})
		if e != nil {
			t.Fatal("synthetic mysql metadata unavailable")
		}
		if e = repository.MigrateDataSourceSchema(db); e != nil {
			t.Fatal(e)
		}
	} else {
		db, e = gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
		if e != nil {
			t.Fatal(e)
		}
		if e = db.Exec(`CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null default '', tenant_id text not null default '', created_at datetime, updated_at datetime, UNIQUE (tenant_id, name))`).Error; e != nil {
			t.Fatal(e)
		}
	}

	if e = auditlog.Migrate(db); e != nil {
		t.Fatal(e)
	}
	vault, e := security.NewCredentialVault(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	repo, e := repository.NewDataSourceRepository(db, vault)
	if e != nil {
		t.Fatal(e)
	}
	port := 5432
	if raw := os.Getenv("DF_WB_E2E_POSTGRES_PORT"); raw != "" {
		port, e = strconv.Atoi(raw)
		if e != nil {
			t.Fatal(e)
		}
	}
	policy, e := database.NewFixedIPEgressPolicy([]string{"127.0.0.1/32"}, []int{port})
	if e != nil {
		t.Fatal(e)
	}
	pool, e := database.NewRestrictedConnectionPool(database.GetDriverRegistry(), nil, vault, false, policy)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.CloseAll()
	pagination, streaming := service.NewGatewayQueryServices(repo, pool, false, nil, nil, nil, time.Minute, nil)
	defer pagination.Shutdown()
	defer streaming.Shutdown()
	cfg := &config.Config{}
	cfg.Query.ExecutionMode = "pagination"
	server := mcp.NewMCPServer(unifiedservice.NewUnifiedQueryService(pagination, streaming, cfg), repo, nil)
	verifier, e := security.NewOIDCIDTokenVerifier(ctx, provider.URL, "browser-client", provider.Client())
	if e != nil {
		t.Fatal(e)
	}
	var runtimeHandler http.Handler
	browserServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if runtimeHandler == nil {
			http.Error(w, "not ready", 503)
			return
		}
		runtimeHandler.ServeHTTP(w, r)
	}))
	browserServer.StartTLS()
	defer browserServer.Close()
	browserClient := browserServer.Client()
	browserClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	origin := browserServer.URL
	oauth := oauth2.Config{ClientID: "browser-client", RedirectURL: origin + "/onboarding/auth/callback", Scopes: []string{"openid"}, Endpoint: oauth2.Endpoint{AuthURL: provider.URL + "/authorize", TokenURL: provider.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}}
	opts := Options{Enabled: true, Origin: origin, MCPIssuer: provider.URL, AuditKey: strings.Repeat("x", 32), CIDRs: []string{"127.0.0.1/32"}, Ports: []int{port}, DB: db, Vault: vault, Validator: database.NewHealthChecker(pool), MCP: server, Fallback: http.NotFoundHandler(), OAuth: oauth, Verifier: verifier, HTTPClient: provider.Client(), Pool: pool, Policy: policy}
	rt, e := New(ctx, opts)
	if e != nil {
		t.Fatal(e)
	}
	defer rt.Close()
	if e = onboarding.MigrateGrants(db); e != nil {
		t.Fatal("repeated grant migration failed", e)
	}
	retryOpts := opts
	retryOpts.MCP = mcp.NewMCPServer(nil, repo, nil)
	second, e := New(ctx, retryOpts)
	if e != nil {
		t.Fatalf("repeated runtime init failed: %v", e)
	}
	second.Close()

	runtimeHandler = rt.Handler
	if e = rt.Grants.Provision(ctx, provider.URL, "alice", "alice", "tenant-a"); e != nil {
		t.Fatal(e)
	}
	accessValidator, e := security.NewOIDCTokenValidatorWithHTTPClient(ctx, security.OIDCTokenConfig{Issuer: provider.URL, Audience: "fabric-api", RequiredScopes: []string{"data-fabric.query"}}, provider.Client())
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	access := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": provider.URL, "sub": "alice", "aud": "fabric-api", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "tenant": "tenant-a", "scope": "data-fabric.sources.manage data-fabric.query"})
	access.Header["kid"] = "test-key"
	access.Header["typ"] = "at+jwt"
	signedAccess, e := access.SignedString(key)
	if e != nil {
		t.Fatal(e)
	}
	server.SetDataSourceManagementService(service.NewDataSourceService(repo, pool, database.NewHealthChecker(pool)))
	actualMCP := httptest.NewServer(mcp.StrictTenantHTTPHandler(server.StreamableHTTPHandler(accessValidator)))
	defer actualMCP.Close()
	badAccess := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": provider.URL, "sub": "alice", "aud": "wrong-api", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "tenant": "tenant-a", "scope": "data-fabric.sources.manage data-fabric.query"})
	badAccess.Header["kid"] = "test-key"
	badAccess.Header["typ"] = "at+jwt"
	badToken, e := badAccess.SignedString(key)
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"invalid-synthetic-token", badToken} {
		req, e := http.NewRequest("POST", actualMCP.URL+"/mcp", strings.NewReader("{}"))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", "Bearer "+bad)
		resp, e := actualMCP.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatal("bad access token accepted")
		}
	}
	mclient, e := sdk.NewStreamableHttpClient(actualMCP.URL+"/mcp", transport.WithHTTPBasicClient(actualMCP.Client()), transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + signedAccess}))
	if e != nil {
		t.Fatal(e)
	}
	defer mclient.Close()
	if e = mclient.Start(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = mclient.Initialize(ctx, protocol.InitializeRequest{Params: protocol.InitializeParams{ProtocolVersion: protocol.LATEST_PROTOCOL_VERSION, ClientInfo: protocol.Implementation{Name: "synthetic", Version: "1"}}}); e != nil {
		t.Fatal(e)
	}
	begin := toolJSON(t, mclient, "begin_data_source_onboarding", nil)
	flow, ok := begin["flow_id"].(string)
	if !ok || len(flow) != 43 {
		t.Fatalf("bad begin response: %v", begin)
	}
	login := httptest.NewRecorder()
	exchangeBrowser(t, browserClient, origin, login, httptest.NewRequest("GET", "https://fabric.example/onboarding/auth/login?flow="+flow, nil))
	u, e := url.Parse(login.Header().Get("Location"))
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	nonce = q.Get("nonce")
	challenge = q.Get("code_challenge")
	request := httptest.NewRequest("GET", "https://fabric.example/onboarding/auth/callback?state="+q.Get("state")+"&code=synthetic-code", nil)
	for _, c := range login.Result().Cookies() {
		request.AddCookie(c)
	}
	callback := httptest.NewRecorder()
	exchangeBrowser(t, browserClient, origin, callback, request)
	if callback.Code != 303 {
		t.Fatalf("callback: %d %s", callback.Code, callback.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range callback.Result().Cookies() {
		if c.Name == "__Host-df_session" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("missing session")
	}
	getSession := httptest.NewRequest("GET", "https://fabric.example/onboarding/session", nil)
	getSession.AddCookie(cookie)
	session := httptest.NewRecorder()
	exchangeBrowser(t, browserClient, origin, session, getSession)
	var sessionBody struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(session.Body.Bytes(), &sessionBody)
	if sessionBody.CSRF == "" {
		t.Fatalf("missing csrf: %s", session.Body.String())
	}
	payload := fmt.Sprintf(`{"name":"synthetic-orders","type":"postgresql","config":{"host":"127.0.0.1","port":%d,"database":"orders","username":"reader","password":"synthetic-password"}}`, port)
	if os.Getenv("DF_WB_E2E_POSTGRES_PORT") != "" {
		probe := httptest.NewRequest("POST", "https://fabric.example/onboarding/flows/"+flow+"/test", bytes.NewBufferString(payload))
		probe.AddCookie(cookie)
		probe.Header.Set("Origin", origin)
		probe.Header.Set("X-CSRF-Token", sessionBody.CSRF)
		tested := httptest.NewRecorder()
		exchangeBrowser(t, browserClient, origin, tested, probe)
		if tested.Code != 200 || !strings.Contains(tested.Body.String(), `"connected":true`) {
			t.Fatalf("real probe failed: %d %s", tested.Code, tested.Body.String())
		}
	}
	denied := httptest.NewRequest("POST", "https://fabric.example/onboarding/flows/"+flow+"/sources", bytes.NewBufferString(payload))
	denied.AddCookie(cookie)
	denied.Header.Set("Origin", "https://attacker.example")
	denied.Header.Set("X-CSRF-Token", sessionBody.CSRF)
	blocked := httptest.NewRecorder()
	exchangeBrowser(t, browserClient, origin, blocked, denied)
	if blocked.Code == 200 {
		t.Fatal("cross-origin create accepted")
	}

	// Pause after initial flow inspection; revocation before the create lock wins.
	pending := toolJSON(t, mclient, "begin_data_source_onboarding", nil)
	pendingFlow := pending["flow_id"].(string)
	pendingPayload := strings.Replace(payload, "synthetic-orders", "revoked-orders", 1)
	var pauseOnce sync.Once
	inspected := make(chan struct{})
	resume := make(chan struct{})
	done := make(chan int, 1)
	if e = db.Callback().Query().After("gorm:query").Register("test:pause_flow_inspect", func(tx *gorm.DB) {
		if tx.Statement.Table == "df_onboarding_flows" {
			pauseOnce.Do(func() { close(inspected); <-resume })
		}
	}); e != nil {
		t.Fatal(e)
	}
	go func() {
		req, err := http.NewRequest("POST", origin+"/onboarding/flows/"+pendingFlow+"/sources", bytes.NewBufferString(pendingPayload))
		if err != nil {
			done <- 0
			return
		}
		req.AddCookie(cookie)
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", sessionBody.CSRF)
		res, err := browserClient.Do(req)
		if err != nil {
			done <- 0
			return
		}
		res.Body.Close()
		done <- res.StatusCode
	}()
	select {
	case <-inspected:
	case <-time.After(10 * time.Second):
		t.Fatal("flow inspection was not reached")
	}
	if e = rt.Grants.Revoke(ctx, provider.URL, "alice"); e != nil {
		t.Fatal(e)
	}
	close(resume)
	if code := <-done; code == 0 || code == 200 {
		t.Fatalf("revoked in-flight create accepted: %d", code)
	}
	if e = db.Callback().Query().Remove("test:pause_flow_inspect"); e != nil {
		t.Fatal(e)
	}
	var deniedCount int64
	if e = db.Table("data_sources").Where("name = ?", "revoked-orders").Count(&deniedCount).Error; e != nil || deniedCount != 0 {
		t.Fatalf("revoked create persisted: %d %v", deniedCount, e)
	}
	if e = rt.Grants.Restore(ctx, provider.URL, "alice", "alice", "tenant-a"); e != nil {
		t.Fatal(e)
	}
	// Pause the real source insert after the handler acquires the grant lock.
	locked := make(chan struct{})
	release := make(chan struct{})
	created := make(chan error, 1)
	revoked := make(chan error, 1)
	if e = db.Callback().Create().Before("gorm:create").Register("test:pause_source_insert", func(tx *gorm.DB) {
		if tx.Statement.Table == "data_sources" {
			close(locked)
			<-release
		}
	}); e != nil {
		t.Fatal(e)
	}
	go func() {
		req, err := http.NewRequest("POST", origin+"/onboarding/flows/"+flow+"/sources", bytes.NewBufferString(payload))
		if err != nil {
			created <- err
			return
		}
		req.AddCookie(cookie)
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", sessionBody.CSRF)
		res, err := browserClient.Do(req)
		if err == nil {
			res.Body.Close()
		}
		created <- err
	}()
	select {
	case <-locked:
	case <-time.After(10 * time.Second):
		t.Fatal("create did not reach transaction lock")
	}
	go func() { revoked <- rt.Grants.Revoke(ctx, provider.URL, "alice") }()
	select {
	case err := <-revoked:
		t.Fatalf("revoke completed while create held grant: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if e = <-created; e != nil {
		t.Fatal(e)
	}
	if e = <-revoked; e != nil {
		t.Fatal(e)
	}
	if e = db.Callback().Create().Remove("test:pause_source_insert"); e != nil {
		t.Fatal(e)
	}
	var count int64
	if e = db.Table("data_sources").Where("name = ?", "synthetic-orders").Count(&count).Error; e != nil || count != 1 {
		t.Fatalf("create was not committed once: %d %v", count, e)
	}
	revokedStatus, e := mclient.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: "get_data_source_onboarding_status", Arguments: map[string]any{"flow_id": flow}}})
	if e != nil || !revokedStatus.IsError {
		t.Fatal("revoked user can recover status")
	}
	revokedRecovery := httptest.NewRequest("POST", "https://fabric.example/onboarding/flows/"+flow+"/sources", bytes.NewBufferString(payload))
	revokedRecovery.AddCookie(cookie)
	revokedRecovery.Header.Set("Origin", origin)
	revokedRecovery.Header.Set("X-CSRF-Token", sessionBody.CSRF)
	revokedResp := httptest.NewRecorder()
	exchangeBrowser(t, browserClient, origin, revokedResp, revokedRecovery)
	if revokedResp.Code == 200 {
		t.Fatal("revoked user can recover source")
	}
	if e = rt.Grants.Restore(ctx, provider.URL, "alice", "alice", "tenant-a"); e != nil {
		t.Fatal(e)
	}
	// A lost/denied first response can be safely recovered after reauthorization.
	post := httptest.NewRequest("POST", "https://fabric.example/onboarding/flows/"+flow+"/sources", bytes.NewBufferString(payload))
	post.AddCookie(cookie)
	post.Header.Set("Origin", origin)
	post.Header.Set("X-CSRF-Token", sessionBody.CSRF)
	response := httptest.NewRecorder()
	exchangeBrowser(t, browserClient, origin, response, post)
	if response.Code != 200 {
		t.Fatalf("recovery: %d %s", response.Code, response.Body.String())
	}

	status := toolJSON(t, mclient, "get_data_source_onboarding_status", map[string]any{"flow_id": flow})
	if status["status"] != "completed" || status["datasource_id"] == "" {
		t.Fatalf("bad status: %v", status)
	}
	list, e := mclient.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: "list_data_sources", Arguments: map[string]any{}}})
	if e != nil || list.IsError {
		t.Fatalf("list: %v %+v", e, list)
	}
	encoded, _ := json.Marshal(list)
	if !bytes.Contains(encoded, []byte("synthetic-orders")) || bytes.Contains(encoded, []byte("synthetic-password")) {
		t.Fatal("list failed or credential leaked")
	}
	foreign := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": provider.URL, "sub": "alice", "aud": "fabric-api", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "tenant": "tenant-b", "scope": "data-fabric.sources.manage data-fabric.query"})
	foreign.Header["kid"] = "test-key"
	foreign.Header["typ"] = "at+jwt"
	foreignToken, e := foreign.SignedString(key)
	if e != nil {
		t.Fatal(e)
	}
	foreignClient, e := sdk.NewStreamableHttpClient(actualMCP.URL+"/mcp", transport.WithHTTPBasicClient(actualMCP.Client()), transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + foreignToken}))
	if e != nil {
		t.Fatal(e)
	}
	defer foreignClient.Close()
	if e = foreignClient.Start(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = foreignClient.Initialize(ctx, protocol.InitializeRequest{Params: protocol.InitializeParams{ProtocolVersion: protocol.LATEST_PROTOCOL_VERSION, ClientInfo: protocol.Implementation{Name: "foreign-test", Version: "1"}}}); e != nil {
		t.Fatal(e)
	}
	foreignList, e := foreignClient.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: "list_data_sources", Arguments: map[string]any{}}})
	if e != nil || foreignList.IsError {
		t.Fatal("foreign list failed", e)
	}
	foreignBytes, _ := json.Marshal(foreignList)
	if bytes.Contains(foreignBytes, []byte("synthetic-orders")) {
		t.Fatal("foreign tenant saw source")
	}
	foreignQuery, e := foreignClient.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: "execute_sql_query", Arguments: map[string]any{"datasource_id": status["datasource_id"], "sql": "SELECT 42", "execution_mode": "pagination"}}})
	if e != nil || !foreignQuery.IsError {
		t.Fatal("foreign tenant queried source")
	}

	if os.Getenv("DF_WB_E2E_POSTGRES_PORT") != "" {
		query, e := mclient.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: "execute_sql_query", Arguments: map[string]any{"datasource_id": status["datasource_id"], "sql": "SELECT 42 AS answer", "execution_mode": "pagination"}}})
		if e != nil || query.IsError {
			t.Fatalf("query failed: %v %+v", e, query)
		}
		b, _ := json.Marshal(query)
		if !bytes.Contains(b, []byte("42")) {
			t.Fatalf("missing result %s", b)
		}
	}

	deleted, e := mclient.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: "delete_data_source", Arguments: map[string]any{"datasource_id": status["datasource_id"], "confirmation_name": "synthetic-orders"}}})
	if e != nil || deleted.IsError {
		t.Fatalf("delete failed: %v %+v", e, deleted)
	}
	listAfter, e := mclient.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: "list_data_sources", Arguments: map[string]any{}}})
	if e != nil || listAfter.IsError {
		t.Fatalf("list after delete failed: %v %+v", e, listAfter)
	}
	afterBytes, _ := json.Marshal(listAfter)
	if bytes.Contains(afterBytes, []byte("synthetic-orders")) {
		t.Fatal("deleted source still visible")
	}
	if e = rt.Grants.Revoke(ctx, provider.URL, "alice"); e != nil {
		t.Fatal(e)
	}
	rejected, e := mclient.CallTool(ctx, protocol.CallToolRequest{Params: protocol.CallToolParams{Name: "get_data_source_onboarding_status", Arguments: map[string]any{"flow_id": flow}}})
	if e != nil || !rejected.IsError {
		t.Fatal("revoked grant retained onboarding access")
	}

	if e = onboarding.RequireInnoDB(db, onboarding.RequiredTables); e != nil {
		t.Fatalf("valid InnoDB schema rejected: %v", e)
	}
	if e = db.Exec("ALTER TABLE df_browser_sessions ENGINE=MyISAM").Error; e != nil {
		t.Fatal(e)
	}
	if e = onboarding.RequireInnoDB(db, onboarding.RequiredTables); e == nil {
		t.Fatal("nontransactional table accepted")
	}
	badOpts := opts
	badOpts.MCP = mcp.NewMCPServer(nil, repo, nil)
	badRuntime, badErr := New(ctx, badOpts)
	if badErr == nil {
		badRuntime.Close()
		t.Fatal("runtime accepted MyISAM table")
	}
	if !strings.Contains(badErr.Error(), "InnoDB") {
		t.Fatalf("unexpected startup error: %v", badErr)
	}

	if e = db.Exec("ALTER TABLE df_browser_sessions ENGINE=InnoDB").Error; e != nil {
		t.Fatal(e)
	}

}
