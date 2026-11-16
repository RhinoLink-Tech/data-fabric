package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/gin-gonic/gin"
)

// Connector-specific rules (for example, a port that is optional for object
// storage or defaulted for SQL) belong to the selected driver. The REST
// handlers must not reject configurations the driver accepts.
func TestDataSourceCRUDAcceptsDriverValidPortlessConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stored := &model.DataSource{
		ID: "source-id", Name: "orders", Type: model.DatabaseTypeOzoneCSV,
		Config: model.DataSourceConfig{Host: "ozone.example.test", Database: "analytics", Username: "gateway"},
		Status: model.DataSourceStatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}

	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		handler func(*DataSourceController, *gin.Context)
		status  int
	}{
		{
			name: "create portless object storage config", method: http.MethodPost, path: "/api/v1/datasources",
			body:    `{"name":"synthetic-orders","type":"ozone_csv","config":{"host":"ozone.example.test","database":"analytics","username":"gateway","additionalProps":{"endpoint_url":"https://ozone.example.test"}}}`,
			handler: func(dc *DataSourceController, c *gin.Context) { dc.CreateDataSource(c) },
			status:  http.StatusCreated,
		},
		{
			name: "update portless object storage config", method: http.MethodPut, path: "/api/v1/datasources/source-id",
			body:    `{"config":{"host":"ozone.example.test","database":"analytics","username":"gateway","additionalProps":{"endpoint_url":"https://ozone.example.test"}}}`,
			handler: func(dc *DataSourceController, c *gin.Context) { dc.UpdateDataSource(c) },
			status:  http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := NewDataSourceController(&dataSourceControllerServiceStub{dataSource: stored})
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
		})
	}
}
