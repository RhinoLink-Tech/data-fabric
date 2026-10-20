package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nexus-gateway/internal/config"
	"nexus-gateway/internal/controller"
	"nexus-gateway/internal/model"
	"nexus-gateway/internal/security"
	"nexus-gateway/internal/service"

	"github.com/gin-gonic/gin"
)

// testJWTSecret satisfies the production validation rules: at least 32 bytes
// and not a known placeholder.
const testJWTSecret = "route-test-secret-0123456789abcdef0123456789"

type stubUnifiedQueryService struct{}

func (stubUnifiedQueryService) ValidateQuery(context.Context, *model.QueryRequest) error {
	return nil
}
func (stubUnifiedQueryService) GetQueryStats(context.Context) (*model.QueryStats, error) {
	return &model.QueryStats{}, nil
}
func (stubUnifiedQueryService) FetchQuery(context.Context, *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	return &model.FetchQueryResponse{QueryID: "stub-query"}, nil
}
func (stubUnifiedQueryService) FetchNextBatch(context.Context, string, string, string, int) (*model.FetchQueryResponse, error) {
	return &model.FetchQueryResponse{QueryID: "stub-query"}, nil
}

type stubDataSourceService struct{}

func (stubDataSourceService) CreateDataSource(context.Context, *service.CreateDataSourceRequest) (*model.DataSource, error) {
	return &model.DataSource{}, nil
}
func (stubDataSourceService) GetDataSource(context.Context, string) (*model.DataSource, error) {
	return &model.DataSource{}, nil
}
func (stubDataSourceService) GetDataSourceByName(context.Context, string) (*model.DataSource, error) {
	return &model.DataSource{}, nil
}
func (stubDataSourceService) ListDataSources(context.Context, *service.ListDataSourcesRequest) (*service.ListDataSourcesResponse, error) {
	return &service.ListDataSourcesResponse{}, nil
}
func (stubDataSourceService) UpdateDataSource(context.Context, string, *service.UpdateDataSourceRequest) (*model.DataSource, error) {
	return &model.DataSource{}, nil
}
func (stubDataSourceService) DeleteDataSource(context.Context, string) error { return nil }
func (stubDataSourceService) ActivateDataSource(context.Context, string) error {
	return nil
}
func (stubDataSourceService) DeactivateDataSource(context.Context, string) error {
	return nil
}
func (stubDataSourceService) GetDataSourceStats(context.Context) (*service.DataSourceStatsResponse, error) {
	return &service.DataSourceStatsResponse{}, nil
}

// newTestRouter builds the real route table with stub controllers. It returns
// a Bearer token signed with the same secret the middleware verifies against.
func newTestRouter(t *testing.T, enableAuth bool) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Server:   config.ServerConfig{Mode: "debug"},
		Security: config.SecurityConfig{EnableAuth: enableAuth},
	}
	jwtManager := security.NewJWTManager(testJWTSecret, time.Hour)
	token, err := jwtManager.GenerateToken("user-1", "tester", []string{"admin"})
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	router := newRouter(routerDeps{
		cfg:                  cfg,
		authMiddleware:       security.NewAuthMiddleware(jwtManager),
		healthController:     controller.NewHealthController(nil),
		datasourceController: controller.NewDataSourceController(stubDataSourceService{}),
		queryController:      controller.NewQueryController(stubUnifiedQueryService{}),
		databaseController:   controller.NewDatabaseController(nil, nil),
	})
	return router, token
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
