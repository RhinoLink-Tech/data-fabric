package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
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
	var response struct {
		Data map[string]database.ConnectorSupport `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode compatibility response: %v", err)
	}
	ozone, ok := response.Data[string(model.DatabaseTypeOzoneParquet)]
	if !ok || ozone.Status != database.ConnectorStatusExperimental || !ozone.Capabilities.SupportsSQL {
		t.Fatalf("compatibility response should report the experimental Ozone Parquet SQL adapter: %+v", ozone)
	}
	textProfile, ok := response.Data[string(model.DatabaseTypeOzoneText)]
	if !ok || textProfile.Status != database.ConnectorStatusExperimental || !textProfile.Capabilities.SupportsSQL {
		t.Errorf("compatibility response should report Ozone text as experimental: %+v", textProfile)
	}
	avroProfile, ok := response.Data[string(model.DatabaseTypeOzoneAvro)]
	if !ok || avroProfile.Status != database.ConnectorStatusExperimental || !avroProfile.Capabilities.SupportsSQL {
		t.Errorf("compatibility response should report Ozone Avro as experimental: %+v", avroProfile)
	}
	orcProfile, ok := response.Data[string(model.DatabaseTypeOzoneORC)]
	if !ok || orcProfile.Status != database.ConnectorStatusExperimental || !orcProfile.Capabilities.SupportsSQL {
		t.Errorf("compatibility response should report Ozone ORC as experimental: %+v", orcProfile)
	}
}
