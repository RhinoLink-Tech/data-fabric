package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
	"github.com/gin-gonic/gin"
)

// #9 controller-layer authorization. The uniform 403 is an anti-oracle: for
// non-admin callers, a missing data source and someone else's data source
// must produce byte-identical responses, on every management endpoint and on
// /fetch, so existence cannot be probed.

const authzTestUUID = "11111111-1111-4111-8111-111111111111"

// normalizeErrorBody strips the per-request timestamp so two responses can
// be compared byte for byte; everything else must match exactly.
func normalizeErrorBody(body string) string {
	return regexp.MustCompile(`"timestamp":"[^"]*"`).ReplaceAllString(body, `"timestamp":"<ts>"`)
}

func authzContext(t *testing.T, claims *security.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if claims != nil {
		ctx.Set("user_claims", claims)
	}
	return ctx, recorder
}

// authzMutationContext prepares a request whose body satisfies the JSON-bound
// handlers (update).
func authzMutationContext(t *testing.T, claims *security.Claims, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	ctx, recorder := authzContext(t, claims)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	return ctx, recorder
}

func nonAdminClaims() *security.Claims {
	return &security.Claims{UserID: "eve", TenantID: "contoso", Username: "eve"}
}

func adminClaims() *security.Claims {
	return &security.Claims{UserID: "root", Roles: []string{security.AdminRole}, Username: "root"}
}

// managementEndpoints exercises every data-source handler that resolves a
// record by ID; each stub wires one service error into the handler's path.
func managementEndpoints(stub *dataSourceControllerServiceStub) []struct {
	name    string
	handler func(*DataSourceController) func(*gin.Context)
} {
	return []struct {
		name    string
		handler func(*DataSourceController) func(*gin.Context)
	}{
		{"get", func(dc *DataSourceController) func(*gin.Context) { return dc.GetDataSource }},
		{"update", func(dc *DataSourceController) func(*gin.Context) { return dc.UpdateDataSource }},
		{"delete", func(dc *DataSourceController) func(*gin.Context) { return dc.DeleteDataSource }},
		{"activate", func(dc *DataSourceController) func(*gin.Context) { return dc.ActivateDataSource }},
		{"deactivate", func(dc *DataSourceController) func(*gin.Context) { return dc.DeactivateDataSource }},
	}
}

// The two indistinguishable outcomes: the record does not exist, and the
// record exists but belongs to someone else.
func managementErrorCases() []struct {
	name string
	err  error
} {
	return []struct {
		name string
		err  error
	}{
		{"missing", repository.ErrDataSourceNotFound},
		{"forbidden", security.ErrDataSourceAccessDenied},
	}
}

func TestManagementErrorsAreUniformForNonAdmins(t *testing.T) {
	for _, endpoint := range managementEndpoints(&dataSourceControllerServiceStub{}) {
		for _, errorCase := range managementErrorCases() {
			name := endpoint.name + " " + errorCase.name
			t.Run(name, func(t *testing.T) {
				// One response per error case, captured with fresh stubs so
				// the only difference between runs is the service error.
				bodies := make([]string, len(managementErrorCases()))
				statuses := make([]int, len(managementErrorCases()))
				for i, candidate := range managementErrorCases() {
					stub := &dataSourceControllerServiceStub{
						dataSource: &model.DataSource{ID: authzTestUUID, Name: "warehouse"},
					}
					switch endpoint.name {
					case "get":
						stub.getErr = candidate.err
					case "update":
						stub.updateErr = candidate.err
					case "delete":
						stub.deleteErr = candidate.err
					case "activate":
						stub.activateErr = candidate.err
					case "deactivate":
						stub.deactivateErr = candidate.err
					}
					controller := NewDataSourceController(stub)
					var ctx *gin.Context
					var recorder *httptest.ResponseRecorder
					if endpoint.name == "update" {
						ctx, recorder = authzMutationContext(t, nonAdminClaims(), `{"name":"renamed"}`)
					} else {
						ctx, recorder = authzContext(t, nonAdminClaims())
					}
					ctx.Params = gin.Params{{Key: "id", Value: authzTestUUID}}
					endpoint.handler(controller)(ctx)
					statuses[i] = recorder.Code
					bodies[i] = recorder.Body.String()
				}

				for i := range bodies {
					if statuses[i] != http.StatusForbidden {
						t.Fatalf("%s: status = %d, want 403; body: %s", managementErrorCases()[i].name, statuses[i], bodies[i])
					}
					if !strings.Contains(bodies[i], "DATASOURCE_ACCESS_DENIED") {
						t.Fatalf("body missing the uniform code: %s", bodies[i])
					}
					// The 403 must not echo the resource.
					if strings.Contains(bodies[i], authzTestUUID) || strings.Contains(bodies[i], "warehouse") {
						t.Fatalf("403 body echoes the resource: %s", bodies[i])
					}
				}
				if normalizeErrorBody(bodies[0]) != normalizeErrorBody(bodies[1]) {
					t.Fatalf("missing vs forbidden responses differ:\nmissing:   %s\nforbidden: %s", bodies[0], bodies[1])
				}
			})
		}
	}
}

func TestManagementErrorsKeepDistinctStatusesForAdmins(t *testing.T) {
	stub := &dataSourceControllerServiceStub{getErr: repository.ErrDataSourceNotFound}
	controller := NewDataSourceController(stub)
	ctx, recorder := authzContext(t, adminClaims())
	ctx.Params = gin.Params{{Key: "id", Value: authzTestUUID}}
	controller.GetDataSource(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("admin missing status = %d, want 404; body: %s", recorder.Code, recorder.Body.String())
	}

	// An access-denied error reaching an admin is a service bug (admins pass
	// every gate); it must surface as 500, not masquerade as 403.
	stub = &dataSourceControllerServiceStub{getErr: security.ErrDataSourceAccessDenied}
	controller = NewDataSourceController(stub)
	ctx, recorder = authzContext(t, adminClaims())
	ctx.Params = gin.Params{{Key: "id", Value: authzTestUUID}}
	controller.GetDataSource(ctx)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("admin access-denied status = %d, want 500; body: %s", recorder.Code, recorder.Body.String())
	}
}

// stubFetchQueryService stands in for the unified query service on /fetch.
type stubFetchQueryService struct {
	unifiedservice.UnifiedQueryService
	fetchErr    error
	validateErr error
}

func (s *stubFetchQueryService) FetchQuery(context.Context, *security.Caller, *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	return nil, s.fetchErr
}

func (s *stubFetchQueryService) ValidateQuery(context.Context, *security.Caller, *model.QueryRequest) error {
	return s.validateErr
}

func TestFetchErrorHidesDataSourceExistenceForNonAdmins(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"missing", repository.ErrDataSourceNotFound},
		{"forbidden", security.ErrDataSourceAccessDenied},
	}

	var bodies []string
	for _, tc := range cases {
		controller := NewQueryController(&stubFetchQueryService{fetchErr: tc.err})
		ctx, recorder := authzContext(t, nonAdminClaims())
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/fetch", strings.NewReader(
			`{"dataSourceId":"`+authzTestUUID+`","sql":"SELECT 1"}`,
		))
		ctx.Request.Header.Set("Content-Type", "application/json")
		controller.FetchQuery(ctx)

		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403; body: %s", tc.name, recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), "DATASOURCE_ACCESS_DENIED") {
			t.Fatalf("%s: body missing the uniform code: %s", tc.name, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), authzTestUUID) {
			t.Fatalf("%s: 403 body echoes the resource: %s", tc.name, recorder.Body.String())
		}
		bodies = append(bodies, recorder.Body.String())
	}
	if normalizeErrorBody(bodies[0]) != normalizeErrorBody(bodies[1]) {
		t.Fatalf("fetch missing vs forbidden responses differ:\nmissing:   %s\nforbidden: %s", bodies[0], bodies[1])
	}

	// Admins keep the distinct 404 so operators can debug real misspellings.
	controller := NewQueryController(&stubFetchQueryService{fetchErr: repository.ErrDataSourceNotFound})
	ctx, recorder := authzContext(t, adminClaims())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/fetch", strings.NewReader(
		`{"dataSourceId":"`+authzTestUUID+`","sql":"SELECT 1"}`,
	))
	ctx.Request.Header.Set("Content-Type", "application/json")
	controller.FetchQuery(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("admin fetch status = %d, want 404; body: %s", recorder.Code, recorder.Body.String())
	}
}

// SafeQueryErrorMessage must not leak internals for the denial path.
func TestFetchAccessDeniedMessageIsSafe(t *testing.T) {
	if msg := service.SafeQueryErrorMessage(security.ErrDataSourceAccessDenied); msg != "You do not have access to this data source" {
		t.Fatalf("SafeQueryErrorMessage() = %q", msg)
	}
	if msg := service.SafeQueryErrorMessage(errors.New("wrapped: %!w(io.EOF) password=secret")); strings.Contains(msg, "secret") {
		t.Fatalf("unexpected leak: %q", msg)
	}
}

func TestValidateQueryHidesDataSourceExistenceForNonAdmins(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"missing", repository.ErrDataSourceNotFound},
		{"forbidden", security.ErrDataSourceAccessDenied},
	}
	var bodies []string
	for _, tc := range cases {
		controller := NewQueryController(&stubFetchQueryService{validateErr: tc.err})
		ctx, recorder := authzContext(t, nonAdminClaims())
		body := "{\"dataSourceId\":\"" + authzTestUUID + "\",\"sql\":\"SELECT 1\"}"
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/query/validate", strings.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		controller.ValidateQuery(ctx)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403; body: %s", tc.name, recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), "DATASOURCE_ACCESS_DENIED") {
			t.Fatalf("%s: missing uniform error: %s", tc.name, recorder.Body.String())
		}
		bodies = append(bodies, recorder.Body.String())
	}
	if normalizeErrorBody(bodies[0]) != normalizeErrorBody(bodies[1]) {
		t.Fatalf("validate missing vs forbidden responses differ:\nmissing:   %s\nforbidden: %s", bodies[0], bodies[1])
	}

	controller := NewQueryController(&stubFetchQueryService{validateErr: repository.ErrDataSourceNotFound})
	ctx, recorder := authzContext(t, adminClaims())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/query/validate", strings.NewReader(
		"{\"dataSourceId\":\""+authzTestUUID+"\",\"sql\":\"SELECT 1\"}",
	))
	ctx.Request.Header.Set("Content-Type", "application/json")
	controller.ValidateQuery(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("admin missing status = %d, want 404; body: %s", recorder.Code, recorder.Body.String())
	}
}
