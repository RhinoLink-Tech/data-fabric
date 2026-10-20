package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/controller"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"

	"github.com/gin-gonic/gin"
)

// testJWTSecret satisfies the production validation rules: at least 32 bytes
// and not a known placeholder.
const testJWTSecret = "route-test-secret-0123456789abcdef0123456789"

type stubUnifiedQueryService struct{}

func (stubUnifiedQueryService) ValidateQuery(context.Context, *security.Caller, *model.QueryRequest) error {
	return nil
}
func (stubUnifiedQueryService) GetQueryStats(context.Context) (*model.QueryStats, error) {
	return &model.QueryStats{}, nil
}
func (stubUnifiedQueryService) FetchQuery(context.Context, *security.Caller, *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	return &model.FetchQueryResponse{QueryID: "stub-query"}, nil
}
func (stubUnifiedQueryService) FetchNextBatch(context.Context, *security.Caller, string, string, string, int) (*model.FetchQueryResponse, error) {
	return &model.FetchQueryResponse{QueryID: "stub-query"}, nil
}
func (stubUnifiedQueryService) GetQueryHistory(context.Context, service.QueryHistoryFilter) (*service.QueryHistoryPage, error) {
	return &service.QueryHistoryPage{Records: []service.QueryRecord{}, Limit: 100}, nil
}

type stubDataSourceService struct{}

func (stubDataSourceService) CreateDataSource(context.Context, *security.Caller, *service.CreateDataSourceRequest) (*model.DataSource, error) {
	return &model.DataSource{}, nil
}
func (stubDataSourceService) GetDataSource(context.Context, *security.Caller, string) (*model.DataSource, error) {
	return &model.DataSource{}, nil
}
func (stubDataSourceService) GetDataSourceByName(context.Context, *security.Caller, string) (*model.DataSource, error) {
	return &model.DataSource{}, nil
}
func (stubDataSourceService) ListDataSources(context.Context, *security.Caller, *service.ListDataSourcesRequest) (*service.ListDataSourcesResponse, error) {
	return &service.ListDataSourcesResponse{}, nil
}
func (stubDataSourceService) UpdateDataSource(context.Context, *security.Caller, string, *service.UpdateDataSourceRequest) (*model.DataSource, error) {
	return &model.DataSource{}, nil
}
func (stubDataSourceService) DeleteDataSource(context.Context, *security.Caller, string) error {
	return nil
}
func (stubDataSourceService) ActivateDataSource(context.Context, *security.Caller, string) error {
	return nil
}
func (stubDataSourceService) DeactivateDataSource(context.Context, *security.Caller, string) error {
	return nil
}
func (stubDataSourceService) GetDataSourceStats(context.Context, *security.Caller) (*service.DataSourceStatsResponse, error) {
	return &service.DataSourceStatsResponse{}, nil
}

// newTestRouter builds the real route table with stub controllers. It returns
// a Bearer token signed with the same secret the middleware verifies against.
func newTestRouter(t *testing.T, enableAuth bool) (*gin.Engine, string) {
	t.Helper()
	router, tokens := newTestRouterWithTokens(t, enableAuth)
	return router, tokens.admin
}

type testTokens struct {
	admin string
	user  string
}

// newTestRouterWithTokens also mints a non-admin token so route-level
// authorization tests can act as a regular caller.
func newTestRouterWithTokens(t *testing.T, enableAuth bool) (*gin.Engine, testTokens) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Server:   config.ServerConfig{Mode: "debug"},
		Security: config.SecurityConfig{EnableAuth: enableAuth},
	}
	jwtManager := security.NewJWTManager(testJWTSecret, time.Hour)
	tokens := testTokens{}
	var err error
	if tokens.admin, err = jwtManager.GenerateToken("user-1", "", "tester", []string{"admin"}); err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	if tokens.user, err = jwtManager.GenerateToken("user-2", "acme", "plain-user", []string{"user"}); err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	router := newRouter(routerDeps{
		cfg:                      cfg,
		authMiddleware:           security.NewAuthMiddleware(jwtManager),
		healthController:         controller.NewHealthController(nil),
		datasourceController:     controller.NewDataSourceController(stubDataSourceService{}),
		queryController:          controller.NewQueryController(stubUnifiedQueryService{}),
		databaseController:       controller.NewDatabaseController(nil, nil),
		sqlTranslationController: controller.NewSQLTranslationController(),
	})
	return router, tokens
}

// TestFetchEndpointsAlwaysRequireAuth is the #6 regression test: /fetch must
// reject unauthenticated requests under BOTH configurations of
// security.enable_auth. Before #6 the group was mounted outside the auth
// middleware chain and stayed anonymous even with authentication enabled.
func TestFetchEndpointsAlwaysRequireAuth(t *testing.T) {
	fetchBody := `{"dataSourceId":"12345678-1234-4234-8234-123456789012","sql":"SELECT 1"}`

	for _, enableAuth := range []bool{true, false} {
		router, token := newTestRouter(t, enableAuth)
		requests := []struct {
			name       string
			method     string
			path       string
			body       string
			authHeader string
			want       int
		}{
			{"POST without credentials", http.MethodPost, "/api/v1/fetch", fetchBody, "", http.StatusUnauthorized},
			{"POST with invalid token", http.MethodPost, "/api/v1/fetch", fetchBody, "Bearer not-a-jwt", http.StatusUnauthorized},
			{"GET batch without credentials", http.MethodGet, "/api/v1/fetch/q/s/t", "", "", http.StatusUnauthorized},
			{"GET batch with invalid token", http.MethodGet, "/api/v1/fetch/q/s/t", "", "Bearer not-a-jwt", http.StatusUnauthorized},
			{"POST with valid token", http.MethodPost, "/api/v1/fetch", fetchBody, "Bearer " + token, http.StatusOK},
			{"GET batch with valid token", http.MethodGet, "/api/v1/fetch/q/s/t", "", "Bearer " + token, http.StatusOK},
		}
		for _, tt := range requests {
			t.Run(fmt.Sprintf("enable_auth=%v/%s", enableAuth, tt.name), func(t *testing.T) {
				var req *http.Request
				if tt.body != "" {
					req = httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
					req.Header.Set("Content-Type", "application/json")
				} else {
					req = httptest.NewRequest(tt.method, tt.path, nil)
				}
				if tt.authHeader != "" {
					req.Header.Set("Authorization", tt.authHeader)
				}
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, req)
				if recorder.Code != tt.want {
					t.Fatalf("status = %d, want %d; body: %s", recorder.Code, tt.want, recorder.Body.String())
				}
			})
		}
	}
}

// TestEnableAuthSwitchGatesOnlyManagementEndpoints pins the semantics the
// #6 declaration comment recorded: the switch controls the data-source
// management API, while /fetch stays authenticated either way. The
// auth-disabled "open" case below is pre-existing, intentional behavior —
// narrowing it is a follow-up, not part of #6.
func TestEnableAuthSwitchGatesOnlyManagementEndpoints(t *testing.T) {
	const path = "/api/v1/datasources"

	router, token := newTestRouter(t, true)

	req := httptest.NewRequest(http.MethodGet, path, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("auth enabled, no token: status = %d, want %d; body: %s", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("auth enabled, valid token: status = %d, want %d; body: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	router, _ = newTestRouter(t, false)
	req = httptest.NewRequest(http.MethodGet, path, nil)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("auth disabled: status = %d, want %d (pre-existing open management API); body: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
}

func TestFetchBodyLimitLeavesGETContinuationsUnaffected(t *testing.T) {
	router, token := newTestRouter(t, false)
	oversizedBody := strings.Repeat("x", (1<<20)+1)

	post := httptest.NewRequest(http.MethodPost, "/api/v1/fetch", strings.NewReader(oversizedBody))
	post.Header.Set("Content-Type", "application/json")
	post.Header.Set("Authorization", "Bearer "+token)
	postRecorder := httptest.NewRecorder()
	router.ServeHTTP(postRecorder, post)
	if postRecorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized POST status = %d, want %d; body: %s", postRecorder.Code, http.StatusRequestEntityTooLarge, postRecorder.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/api/v1/fetch/q/s/t", strings.NewReader(oversizedBody))
	get.Header.Set("Authorization", "Bearer "+token)
	getRecorder := httptest.NewRecorder()
	router.ServeHTTP(getRecorder, get)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET continuation status = %d, want %d; body: %s", getRecorder.Code, http.StatusOK, getRecorder.Body.String())
	}
}

// TestQueryEndpointsRegisteredAndAuthenticated is the #102 smoke check: the
// documented query routes (validate, stats, history) must exist behind the
// same always-on authentication as the fetch group, under both settings of
// security.enable_auth. Before #102 these handlers were documented but never
// routed, so the API returned 404.
func TestQueryEndpointsRegisteredAndAuthenticated(t *testing.T) {
	validateBody := `{"dataSourceId":"12345678-1234-4234-8234-123456789012","sql":"SELECT 1"}`

	for _, enableAuth := range []bool{true, false} {
		router, token := newTestRouter(t, enableAuth)
		requests := []struct {
			name       string
			method     string
			path       string
			body       string
			authHeader string
			want       int
		}{
			{"POST validate without credentials", http.MethodPost, "/api/v1/query/validate", validateBody, "", http.StatusUnauthorized},
			{"GET stats without credentials", http.MethodGet, "/api/v1/query/stats", "", "", http.StatusUnauthorized},
			{"GET history without credentials", http.MethodGet, "/api/v1/query/history", "", "", http.StatusUnauthorized},
			{"POST validate with invalid token", http.MethodPost, "/api/v1/query/validate", validateBody, "Bearer not-a-jwt", http.StatusUnauthorized},
			{"POST validate with valid token", http.MethodPost, "/api/v1/query/validate", validateBody, "Bearer " + token, http.StatusOK},
			{"GET stats with valid token", http.MethodGet, "/api/v1/query/stats", "", "Bearer " + token, http.StatusOK},
			{"GET history with valid token", http.MethodGet, "/api/v1/query/history", "", "Bearer " + token, http.StatusOK},
		}
		for _, tt := range requests {
			t.Run(fmt.Sprintf("enable_auth=%v/%s", enableAuth, tt.name), func(t *testing.T) {
				var req *http.Request
				if tt.body != "" {
					req = httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
					req.Header.Set("Content-Type", "application/json")
				} else {
					req = httptest.NewRequest(tt.method, tt.path, nil)
				}
				if tt.authHeader != "" {
					req.Header.Set("Authorization", tt.authHeader)
				}
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, req)
				if recorder.Code != tt.want {
					t.Fatalf("status = %d, want %d; body: %s", recorder.Code, tt.want, recorder.Body.String())
				}
			})
		}
	}
}

// TestDatabaseFleetEndpointsAreAdminOnly is the #9 route gate: pool stats and
// the fleet health probe enumerate every stored data source (names, types,
// hosts, connectivity), so with enable_auth on, only admins may reach them.
// The controllers are wired with nil dependencies, so a caller that gets
// past the gate sees the handler's 503 — which is exactly what distinguishes
// "rejected by the route" (401/403) from "reached the handler" here.
func TestDatabaseFleetEndpointsAreAdminOnly(t *testing.T) {
	paths := []string{"/api/v1/database/connections/stats", "/api/v1/database/health"}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			router, tokens := newTestRouterWithTokens(t, true)

			requests := []struct {
				name       string
				authHeader string
				want       int
			}{
				{"no token", "", http.StatusUnauthorized},
				{"non-admin token", "Bearer " + tokens.user, http.StatusForbidden},
				{"admin token reaches handler", "Bearer " + tokens.admin, http.StatusServiceUnavailable},
			}
			for _, tt := range requests {
				t.Run(tt.name, func(t *testing.T) {
					req := httptest.NewRequest(http.MethodGet, path, nil)
					if tt.authHeader != "" {
						req.Header.Set("Authorization", tt.authHeader)
					}
					recorder := httptest.NewRecorder()
					router.ServeHTTP(recorder, req)
					if recorder.Code != tt.want {
						t.Fatalf("status = %d, want %d; body: %s", recorder.Code, tt.want, recorder.Body.String())
					}
				})
			}
		})
	}
}

// With enable_auth disabled there is no identity to check: the fleet
// endpoints stay reachable without credentials, matching the switch's
// documented meaning for the management API.
func TestDatabaseFleetEndpointsOpenWhenAuthDisabled(t *testing.T) {
	router, _ := newTestRouterWithTokens(t, false)
	for _, path := range []string{"/api/v1/database/connections/stats", "/api/v1/database/health"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d, want 503 (handler reached without credentials); body: %s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestMetricsEndpointRequiresAdminWhenAuthenticationIsEnabled(t *testing.T) {
	router, tokens := newTestRouterWithTokens(t, true)
	requests := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{name: "missing token", wantStatus: http.StatusUnauthorized},
		{name: "regular user", token: tokens.user, wantStatus: http.StatusForbidden},
		{name: "administrator", token: tokens.admin, wantStatus: http.StatusOK},
	}

	for _, tt := range requests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("GET /metrics status = %d, want %d; body: %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if tt.wantStatus == http.StatusOK && !strings.Contains(recorder.Body.String(), "data_fabric_http_requests_total") {
				t.Fatal("metrics response does not contain the registered HTTP request metric")
			}
		})
	}
}

func TestMetricsEndpointRemainsAdminProtectedWhenAuthenticationIsDisabled(t *testing.T) {
	router, token := newTestRouter(t, false)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /metrics status = %d, want %d; body: %s", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin GET /metrics status = %d, want %d; body: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
}

func TestHTTPMetricsUseRouteTemplatesAndDoNotExposeRawPathsOrSourceIDs(t *testing.T) {
	router, tokens := newTestRouterWithTokens(t, true)

	queryRequest := httptest.NewRequest(http.MethodGet, "/api/v1/query/stats", nil)
	queryRequest.Header.Set("Authorization", "Bearer "+tokens.admin)
	queryRecorder := httptest.NewRecorder()
	router.ServeHTTP(queryRecorder, queryRequest)
	if queryRecorder.Code != http.StatusOK {
		t.Fatalf("query stats status = %d, want %d", queryRecorder.Code, http.StatusOK)
	}

	unknownRequest := httptest.NewRequest(http.MethodGet, "/private/secret-source-123", nil)
	unknownRecorder := httptest.NewRecorder()
	router.ServeHTTP(unknownRecorder, unknownRequest)

	metricsRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRequest.Header.Set("Authorization", "Bearer "+tokens.admin)
	metricsRecorder := httptest.NewRecorder()
	router.ServeHTTP(metricsRecorder, metricsRequest)
	body := metricsRecorder.Body.String()
	if !strings.Contains(body, `endpoint="/api/v1/query/stats"`) {
		t.Fatal("metrics do not contain the Gin route template")
	}
	if !strings.Contains(body, `endpoint="unmatched"`) {
		t.Fatal("unmatched routes do not use the bounded endpoint label")
	}
	if strings.Contains(body, "secret-source-123") {
		t.Fatal("metrics exposed an unmatched raw URL path")
	}
	if strings.Contains(body, "datasource_id=") {
		t.Fatal("metrics contain a high-cardinality data-source ID label")
	}
}
