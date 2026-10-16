package controller

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"github.com/gin-gonic/gin"
)

type dataSourceControllerServiceStub struct {
	dataSource *model.DataSource
	createErr  error
}

func (s *dataSourceControllerServiceStub) CreateDataSource(context.Context, *service.CreateDataSourceRequest) (*model.DataSource, error) {
	return s.dataSource, s.createErr
}
func (s *dataSourceControllerServiceStub) GetDataSource(context.Context, string) (*model.DataSource, error) {
	return s.dataSource, nil
}
func (s *dataSourceControllerServiceStub) GetDataSourceByName(context.Context, string) (*model.DataSource, error) {
	return s.dataSource, nil
}
func (s *dataSourceControllerServiceStub) ListDataSources(context.Context, *service.ListDataSourcesRequest) (*service.ListDataSourcesResponse, error) {
	return &service.ListDataSourcesResponse{
		DataSources: []*model.DataSource{s.dataSource}, Total: 1, Limit: 20, Offset: 0,
	}, nil
}
func (s *dataSourceControllerServiceStub) UpdateDataSource(context.Context, string, *service.UpdateDataSourceRequest) (*model.DataSource, error) {
	return s.dataSource, nil
}
func (s *dataSourceControllerServiceStub) DeleteDataSource(context.Context, string) error { return nil }
func (s *dataSourceControllerServiceStub) ActivateDataSource(context.Context, string) error {
	return nil
}
func (s *dataSourceControllerServiceStub) DeactivateDataSource(context.Context, string) error {
	return nil
}
func (s *dataSourceControllerServiceStub) GetDataSourceStats(context.Context) (*service.DataSourceStatsResponse, error) {
	return &service.DataSourceStatsResponse{Total: 1}, nil
}

func TestDataSourceCRUDResponsesRedactCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dataSource := &model.DataSource{
		ID: "source-id", Name: "federated", Type: model.DatabaseTypeTrino,
		Config: model.DataSourceConfig{
			Host: "coordinator.example.com", Port: 8443, Database: "lakehouse", Username: "gateway",
			Password: "password-sentinel",
			AdditionalProps: map[string]interface{}{
				"source":       "data-fabric",
				"access_token": "token-sentinel",
				"nested": map[string]interface{}{
					"client_secret": "nested-secret-sentinel",
					"enabled":       true,
				},
			},
		},
		Status: model.DataSourceStatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	updateBody := `{"config":{"host":"coordinator.example.com","port":8443,"database":"lakehouse","username":"gateway"}}`
	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		handler func(*DataSourceController, *gin.Context)
		status  int
	}{
		{name: "create", method: http.MethodPost, path: "/api/v1/datasources", body: `{"name":"federated","type":"trino","config":{"host":"coordinator.example.com","port":8443,"database":"lakehouse","username":"gateway","password":"password-sentinel","additionalProps":{"access_token":"token-sentinel","source":"data-fabric"}}}`, handler: func(dc *DataSourceController, c *gin.Context) { dc.CreateDataSource(c) }, status: http.StatusCreated},
		{name: "get", method: http.MethodGet, path: "/api/v1/datasources/source-id", handler: func(dc *DataSourceController, c *gin.Context) { dc.GetDataSource(c) }, status: http.StatusOK},
		{name: "list", method: http.MethodGet, path: "/api/v1/datasources", handler: func(dc *DataSourceController, c *gin.Context) { dc.ListDataSources(c) }, status: http.StatusOK},
		{name: "update without existing password", method: http.MethodPut, path: "/api/v1/datasources/source-id", body: updateBody, handler: func(dc *DataSourceController, c *gin.Context) { dc.UpdateDataSource(c) }, status: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := NewDataSourceController(&dataSourceControllerServiceStub{dataSource: dataSource})
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if strings.Contains(tt.path, "source-id") {
				ctx.Params = gin.Params{{Key: "id", Value: "source-id"}}
			}

			tt.handler(controller, ctx)

			if recorder.Code != tt.status {
				t.Fatalf("status = %d, want %d; response: %s", recorder.Code, tt.status, recorder.Body.String())
			}
			response := recorder.Body.String()
			for _, secret := range []string{"password-sentinel", "token-sentinel", "nested-secret-sentinel"} {
				if strings.Contains(response, secret) {
					t.Errorf("response leaked %q: %s", secret, response)
				}
			}
			if strings.Contains(response, `"password"`) || strings.Contains(response, `"access_token"`) || strings.Contains(response, `"client_secret"`) {
				t.Errorf("response contains a credential field: %s", response)
			}
			if !strings.Contains(response, "data-fabric") {
				t.Errorf("response should retain non-secret config: %s", response)
			}
		})
	}
}

func TestDataSourceCreateErrorDoesNotExposeCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controller := NewDataSourceController(&dataSourceControllerServiceStub{
		createErr: errors.New("insert failed while handling password-sentinel"),
	})
	recorder := httptest.NewRecorder()
	var logs bytes.Buffer
	router := gin.New()
	router.Use(gin.LoggerWithWriter(&logs))
	router.POST("/api/v1/datasources", controller.CreateDataSource)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/datasources", strings.NewReader(
		`{"name":"federated","type":"trino","config":{"host":"coordinator","port":8443,"database":"lakehouse","username":"gateway","password":"password-sentinel"}}`,
	))
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if strings.Contains(recorder.Body.String(), "password-sentinel") {
		t.Fatalf("error response leaked a credential: %s", recorder.Body.String())
	}
	if strings.Contains(logs.String(), "password-sentinel") {
		t.Fatalf("request log leaked a credential: %s", logs.String())
	}
}
