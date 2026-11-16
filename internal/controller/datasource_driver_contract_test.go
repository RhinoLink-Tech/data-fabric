package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"github.com/gin-gonic/gin"
)

const driverValidationID = "e8c3cfb8-21dd-4f29-a197-7a616e0165ba"

type driverValidationRepository struct {
	repository.DataSourceRepository
	source           *model.DataSource
	creates, updates int
}

func (r *driverValidationRepository) GetByName(context.Context, string, string) (*model.DataSource, error) {
	return nil, repository.ErrDataSourceNotFound
}
func (r *driverValidationRepository) Create(_ context.Context, source *model.DataSource) error {
	copy := *source
	copy.ID = driverValidationID
	r.source = &copy
	r.creates++
	return nil
}
func (r *driverValidationRepository) UpdateSourceLocked(_ context.Context, _ string, _ []string, mutate func(*model.DataSource) error) (*model.DataSource, error) {
	if r.source == nil {
		return nil, repository.ErrDataSourceNotFound
	}
	copy := *r.source
	if err := mutate(&copy); err != nil {
		return nil, err
	}
	r.source = &copy
	r.updates++
	return &copy, nil
}

func driverValidationRequest(repo *driverValidationRepository, method, body string) *httptest.ResponseRecorder {
	controller := NewDataSourceController(service.NewDataSourceService(repo, nil, database.NewHealthChecker(nil)))
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, "/api/v1/datasources/"+driverValidationID, strings.NewReader(body))
	ctx.Params = gin.Params{{Key: "id", Value: driverValidationID}}
	if method == http.MethodPost {
		controller.CreateDataSource(ctx)
	} else {
		controller.UpdateDataSource(ctx)
	}
	return recorder
}

func TestDataSourceCRUDRealDriverValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Run("SQL Server default port on create", func(t *testing.T) {
		r := &driverValidationRepository{}
		out := driverValidationRequest(r, http.MethodPost, `{"name":"orders","type":"sqlserver","config":{"host":"sql.example.test","database":"analytics","username":"reader","password":"synthetic-secret"}}`)
		if out.Code != 201 || r.creates != 1 || r.source.Config.Port != 1433 {
			t.Fatalf("create status=%d creates=%d source=%+v", out.Code, r.creates, r.source)
		}
		if strings.Contains(out.Body.String(), "synthetic-secret") {
			t.Fatal("response disclosed password")
		}
	})
	t.Run("Ozone CSV config without port", func(t *testing.T) {
		r := &driverValidationRepository{}
		out := driverValidationRequest(r, http.MethodPost, `{"name":"events","type":"ozone_csv","config":{"host":"warehouse/events.csv","database":"analytics","username":"access","password":"synthetic-secret","additionalProps":{"endpoint_url":"https://ozone.example.test"}}}`)
		if out.Code != 201 || r.creates != 1 {
			t.Fatalf("create status=%d creates=%d body=%s", out.Code, r.creates, out.Body.String())
		}
	})
	for _, tc := range []struct{ name, body string }{
		{"missing config", `{"name":"bad","type":"sqlserver"}`},
		{"null config", `{"name":"bad","type":"sqlserver","config":null}`},
		{"invalid config shape", `{"name":"bad","type":"sqlserver","config":[]}`},
		{"unknown connector", `{"name":"bad","type":"not_registered","config":{"host":"db","database":"db","username":"reader"}}`},
		{"out-of-range port", `{"name":"bad","type":"sqlserver","config":{"host":"db","port":65536,"database":"db","username":"reader"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &driverValidationRepository{}
			out := driverValidationRequest(r, http.MethodPost, tc.body)
			if out.Code < 400 || out.Code >= 500 || r.creates != 0 {
				t.Fatalf("status=%d creates=%d", out.Code, r.creates)
			}
		})
	}
	t.Run("update preserves password and default port", func(t *testing.T) {
		r := &driverValidationRepository{source: &model.DataSource{ID: driverValidationID, Name: "orders", Type: model.DatabaseTypeSQLServer,
			Config: model.DataSourceConfig{Host: "sql.example.test", Port: 1433, Database: "analytics", Username: "reader", Password: "synthetic-secret"}}}
		out := driverValidationRequest(r, http.MethodPut, `{"config":{"host":"sql.example.test","database":"analytics2","username":"reader"}}`)
		if out.Code != 200 || r.updates != 1 || r.source.Config.Port != 1433 || r.source.Config.Password != "synthetic-secret" {
			t.Fatalf("update status=%d updates=%d", out.Code, r.updates)
		}
		if strings.Contains(out.Body.String(), "synthetic-secret") {
			t.Fatal("response disclosed password")
		}
	})
	t.Run("invalid update is not persisted", func(t *testing.T) {
		r := &driverValidationRepository{source: &model.DataSource{ID: driverValidationID, Name: "orders", Type: model.DatabaseTypeSQLServer,
			Config: model.DataSourceConfig{Host: "sql.example.test", Port: 1433, Database: "analytics", Username: "reader", Password: "synthetic-secret"}}}
		out := driverValidationRequest(r, http.MethodPut, `{"config":{"host":"","database":"analytics","username":"reader"}}`)
		if out.Code < 400 || out.Code >= 500 || r.updates != 0 || r.source.Config.Host != "sql.example.test" {
			t.Fatalf("status=%d updates=%d", out.Code, r.updates)
		}
	})
}
