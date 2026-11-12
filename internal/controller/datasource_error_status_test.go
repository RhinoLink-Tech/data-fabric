package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"github.com/gin-gonic/gin"
)

// The controller maps service errors onto HTTP statuses. The service returns the
// repository's sentinels, some of them wrapped with %w, so the mapping has to
// match by identity through the wrapping rather than by equality.
func TestDataSourceErrorStatusMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const createBody = `{"name":"federated","type":"trino","config":{"host":"coordinator","port":8443,"database":"lakehouse","username":"gateway"}}`

	tests := []struct {
		name       string
		err        error
		body       string
		handler    func(*DataSourceController, *gin.Context)
		wantStatus int
	}{
		{
			name:       "missing data source is a 404",
			err:        fmt.Errorf("failed to get data source: %w", repository.ErrDataSourceNotFound),
			handler:    func(dc *DataSourceController, c *gin.Context) { dc.GetDataSource(c) },
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "wrapped missing source on update is a 404",
			err:        fmt.Errorf("failed to update data source: %w", repository.ErrDataSourceNotFound),
			body:       `{"config":{"host":"db.example.test","port":5432,"database":"analytics","username":"gateway"}}`,
			handler:    func(dc *DataSourceController, c *gin.Context) { dc.UpdateDataSource(c) },
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "wrapped missing source on delete is a 404",
			err:        fmt.Errorf("failed to delete data source: %w", repository.ErrDataSourceNotFound),
			handler:    func(dc *DataSourceController, c *gin.Context) { dc.DeleteDataSource(c) },
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "wrapped missing source on activate is a 404",
			err:        fmt.Errorf("failed to activate data source: %w", repository.ErrDataSourceNotFound),
			handler:    func(dc *DataSourceController, c *gin.Context) { dc.ActivateDataSource(c) },
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "wrapped missing source on deactivate is a 404",
			err:        fmt.Errorf("failed to deactivate data source: %w", repository.ErrDataSourceNotFound),
			handler:    func(dc *DataSourceController, c *gin.Context) { dc.DeactivateDataSource(c) },
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "malformed id is a 400",
			err:        repository.ErrInvalidUUID,
			handler:    func(dc *DataSourceController, c *gin.Context) { dc.GetDataSource(c) },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "duplicate name is a 409",
			err:        repository.ErrDataSourceExists,
			body:       createBody,
			handler:    func(dc *DataSourceController, c *gin.Context) { dc.CreateDataSource(c) },
			wantStatus: http.StatusConflict,
		},
		{
			name:       "duplicate name on update is a 409",
			err:        fmt.Errorf("failed to update data source: %w", repository.ErrDataSourceExists),
			body:       `{"name":"existing-orders","config":{"host":"db.example.test","port":5432,"database":"analytics","username":"gateway"}}`,
			handler:    func(dc *DataSourceController, c *gin.Context) { dc.UpdateDataSource(c) },
			wantStatus: http.StatusConflict,
		},
		{
			name:       "unsupported data-source type is a 400",
			err:        service.ErrUnsupportedDataSourceType,
			body:       createBody,
			handler:    func(dc *DataSourceController, c *gin.Context) { dc.CreateDataSource(c) },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid data-source config on update is a 400",
			err:        service.ErrInvalidDataSourceConfig,
			body:       `{"config":{"host":"db.example.test","port":5432,"database":"analytics","username":"gateway"}}`,
			handler:    func(dc *DataSourceController, c *gin.Context) { dc.UpdateDataSource(c) },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unexpected failure is a 500",
			err:        errors.New("connection reset by peer"),
			handler:    func(dc *DataSourceController, c *gin.Context) { dc.GetDataSource(c) },
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := NewDataSourceController(&dataSourceControllerServiceStub{
				createErr:     tt.err,
				getErr:        tt.err,
				updateErr:     tt.err,
				deleteErr:     tt.err,
				activateErr:   tt.err,
				deactivateErr: tt.err,
			})

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/datasources/source-id", strings.NewReader(tt.body))
			ctx.Params = gin.Params{{Key: "id", Value: "source-id"}}

			tt.handler(controller, ctx)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; response: %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
		})
	}
}
