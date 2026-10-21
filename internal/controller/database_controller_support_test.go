package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/gin-gonic/gin"
)

func TestDatabaseTypeAPIsUseSharedSupportCatalog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controller := NewDatabaseController(nil, database.NewHealthChecker(nil))

	tests := []struct {
		name   string
		handle gin.HandlerFunc
	}{
		{name: "types", handle: controller.GetDatabaseTypes},
		{name: "compatibility", handle: controller.GetDatabaseCompatibility},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			tt.handle(ctx)
			body := recorder.Body.String()
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, body)
			}
			if !strings.Contains(body, `"status":"implementation_present"`) || !strings.Contains(body, `"endToEndVerified":false`) {
				t.Fatalf("response lacks the shared verification state: %s", body)
			}
			for _, staleClaim := range []string{`"5.7+"`, `"10.2+"`, `"9.6+"`, `"12c+"`, `"372+"`} {
				if strings.Contains(body, staleClaim) {
					t.Errorf("response still contains an unverified version claim %s", staleClaim)
				}
			}
		})
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	controller.GetDatabaseCompatibility(ctx)
	if !strings.Contains(recorder.Body.String(), `"ozone_parquet"`) || !strings.Contains(recorder.Body.String(), `"status":"unsupported"`) {
		t.Fatalf("compatibility response should expose explicit unsupported connectors: %s", recorder.Body.String())
	}
}
