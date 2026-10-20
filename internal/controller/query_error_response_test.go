package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
	"github.com/gin-gonic/gin"
)

type queryErrorResponseService struct {
	unifiedservice.UnifiedQueryService
	fetchErr    error
	validateErr error
	statsErr    error
	historyErr  error
	historyPage *service.QueryHistoryPage
}

func (s queryErrorResponseService) FetchQuery(context.Context, *security.Caller, *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	return nil, s.fetchErr
}
func (s queryErrorResponseService) ValidateQuery(context.Context, *security.Caller, *model.QueryRequest) error {
	return s.validateErr
}
func (s queryErrorResponseService) GetQueryStats(context.Context) (*model.QueryStats, error) {
	return nil, s.statsErr
}
func (s queryErrorResponseService) GetQueryHistory(context.Context, service.QueryHistoryFilter) (*service.QueryHistoryPage, error) {
	return s.historyPage, s.historyErr
}

func queryErrorContext(t *testing.T, method, path, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		ctx.Request.Header.Set("Content-Type", "application/json")
	}
	ctx.Set("user_claims", &security.Claims{UserID: "alice"})
	return ctx, recorder
}

func assertResponseOmitsSecrets(t *testing.T, recorder *httptest.ResponseRecorder, secrets ...string) {
	t.Helper()
	body := recorder.Body.String()
	for _, secret := range secrets {
		if strings.Contains(body, secret) {
			t.Errorf("response leaked %q: %s", secret, body)
		}
	}
}

func TestFetchQueryErrorResponseDoesNotLeakSQLOrDriverDetails(t *testing.T) {
	const sqlText = "SELECT 'sql-secret' FROM private.customers"
	internalErr := errors.New("postgres://user:db-secret@db.internal:5432/analytics rejected SQL: " + sqlText)
	controller := NewQueryController(queryErrorResponseService{fetchErr: internalErr})
	ctx, recorder := queryErrorContext(t, http.MethodPost, "/api/v1/fetch", `{"dataSourceId":"11111111-1111-4111-8111-111111111111","sql":"SELECT 'sql-secret' FROM private.customers"}`)

	controller.FetchQuery(ctx)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "QUERY_ENGINE_ERROR") {
		t.Fatalf("response lacks stable error code: %s", recorder.Body.String())
	}
	assertResponseOmitsSecrets(t, recorder, sqlText, "db-secret", "db.internal", "analytics")
}

func TestValidateQueryDoesNotEchoSubmittedSQL(t *testing.T) {
	const sqlText = "SELECT 'query-secret' FROM private.customers"
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "valid query"},
		{name: "invalid query", err: errors.New("driver leaked password=internal-secret")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller := NewQueryController(queryErrorResponseService{validateErr: tc.err})
			ctx, recorder := queryErrorContext(t, http.MethodPost, "/api/v1/query/validate", `{"dataSourceId":"11111111-1111-4111-8111-111111111111","sql":"SELECT 'query-secret' FROM private.customers"}`)
			controller.ValidateQuery(ctx)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body.String())
			}
			assertResponseOmitsSecrets(t, recorder, sqlText, "query-secret", "internal-secret")
		})
	}
}

func TestQueryHistoryResponseOmitsRawSQL(t *testing.T) {
	stub := queryErrorResponseService{historyPage: &service.QueryHistoryPage{
		Records: []service.QueryRecord{{QueryID: "q-1", DataSourceID: "ds-1", SQL: "SELECT 'history-secret' FROM private.customers", User: "alice"}},
		Total:   1,
		Limit:   100,
	}}
	controller := NewQueryController(stub)
	ctx, recorder := queryErrorContext(t, http.MethodGet, "/api/v1/query/history", "")
	controller.GetQueryHistory(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body.String())
	}
	assertResponseOmitsSecrets(t, recorder, "history-secret", "private.customers", "SELECT")
	if !strings.Contains(recorder.Body.String(), "q-1") {
		t.Fatalf("response omitted safe history metadata: %s", recorder.Body.String())
	}
}

func TestQueryStatsAndHistoryErrorsDoNotLeakInternals(t *testing.T) {
	internalErr := errors.New("db.internal returned DSN password=stats-secret and token=private")
	tests := []struct {
		name    string
		path    string
		err     error
		handler func(*QueryController, *gin.Context)
		code    string
	}{
		{name: "stats", path: "/api/v1/query/stats", err: internalErr, handler: (*QueryController).GetQueryStats, code: "STATS_ERROR"},
		{name: "history", path: "/api/v1/query/history", err: internalErr, handler: (*QueryController).GetQueryHistory, code: "HISTORY_ERROR"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := queryErrorResponseService{}
			if tc.name == "stats" {
				stub.statsErr = tc.err
			} else {
				stub.historyErr = tc.err
			}
			controller := NewQueryController(stub)
			ctx, recorder := queryErrorContext(t, http.MethodGet, tc.path, "")
			tc.handler(controller, ctx)
			if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), tc.code) {
				t.Fatalf("status/body = %d %s; want 500 %s", recorder.Code, recorder.Body.String(), tc.code)
			}
			assertResponseOmitsSecrets(t, recorder, "db.internal", "stats-secret", "private")
		})
	}
}
