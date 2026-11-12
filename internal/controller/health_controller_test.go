package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/buildinfo"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestHealthReportsBuildIdentityAndDatabaseStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		closed     bool
		statusCode int
		health     string
		database   string
	}{
		{"healthy", false, http.StatusOK, "healthy", "connected"},
		{"unhealthy", true, http.StatusServiceUnavailable, "unhealthy", "disconnected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			if test.closed {
				if err := sqlDB.Close(); err != nil {
					t.Fatal(err)
				}
			}
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/health", nil)
			NewHealthController(db).HealthCheck(ctx)
			if recorder.Code != test.statusCode {
				t.Fatalf("HTTP status = %d, want %d", recorder.Code, test.statusCode)
			}
			var response HealthResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Status != test.health || response.Database.Status != test.database {
				t.Fatalf("health=%q database=%q, want %q/%q", response.Status, response.Database.Status, test.health, test.database)
			}
			if response.Version != buildinfo.Version || response.SourceRevision != buildinfo.SourceRevision {
				t.Fatalf("build identity %q/%q, want %q/%q", response.Version, response.SourceRevision, buildinfo.Version, buildinfo.SourceRevision)
			}
		})
	}
}
