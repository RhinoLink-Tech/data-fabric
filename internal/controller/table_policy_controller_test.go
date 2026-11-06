package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTablePolicyRESTValidatePublishAndAuditOutcomes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:table-policy-controller-"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	initial := security.TableAuthorizationConfig{
		Enabled: true,
		DataSources: map[string]security.DataSourceTablePolicy{
			"sales": {Rules: []security.TableAccessRule{{
				UserID: "alice", TenantID: "tenant-a",
				Read: []security.TableScope{{Schema: "public", Table: "orders"}},
			}}},
		},
	}
	authorizer := security.NewTableAuthorizer(initial)
	manager, err := security.NewTablePolicyManager(db, authorizer, initial, "")
	if err != nil {
		t.Fatal(err)
	}
	controller := NewTablePolicyController(manager)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_claims", &security.Claims{UserID: "policy-admin", Roles: []string{security.AdminRole}})
		c.Next()
	})
	router.POST("/validate", controller.Validate)
	router.POST("/publish", controller.Publish)
	router.GET("/publications", controller.Publications)

	validate := httptest.NewRecorder()
	router.ServeHTTP(validate, httptest.NewRequest(http.MethodPost, "/validate", strings.NewReader("{\"policy\":{\"enabled\":true,\"data_sources\":{}}}")))
	if validate.Code != http.StatusOK {
		t.Fatalf("preflight status=%d body=%s", validate.Code, validate.Body.String())
	}

	publish := httptest.NewRecorder()
	router.ServeHTTP(publish, httptest.NewRequest(http.MethodPost, "/publish", strings.NewReader("{\"expectedRevision\":1,\"policy\":{\"enabled\":true,\"data_sources\":{}}}")))
	if publish.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", publish.Code, publish.Body.String())
	}
	var result security.TablePolicyPublicationResult
	if err := json.Unmarshal(publish.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Revision != "2" || !result.AuditRecorded || result.PublishedBy != "policy-admin" {
		t.Fatalf("unexpected publication result: %+v", result)
	}

	invalid := httptest.NewRecorder()
	router.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/publish", strings.NewReader("{\"expectedRevision\":2,\"policy\":{\"enabled\":true,\"data_sources\":{\"sales\":{\"rules\":[{\"user_id\":\"alice\",\"read\":[{}]}]}}}}")))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid update status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	if manager.Status().Revision != "2" {
		t.Fatalf("invalid update replaced active policy: %+v", manager.Status())
	}

	conflict := httptest.NewRecorder()
	router.ServeHTTP(conflict, httptest.NewRequest(http.MethodPost, "/publish", strings.NewReader("{\"expectedRevision\":1,\"policy\":{\"enabled\":true,\"data_sources\":{}}}")))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("stale CAS status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	history := httptest.NewRecorder()
	router.ServeHTTP(history, httptest.NewRequest(http.MethodGet, "/publications?limit=10", nil))
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), "\"publishedBy\":\"policy-admin\"") ||
		!strings.Contains(history.Body.String(), "\"outcome\":\"conflict\"") {
		t.Fatalf("publication history status=%d body=%s", history.Code, history.Body.String())
	}
	if time.Since(manager.Status().LastSyncAt) > time.Minute {
		t.Fatalf("unexpected stale local status: %+v", manager.Status())
	}
}

func TestQueryFailureStatusReportsStalePolicy(t *testing.T) {
	status, code := queryFailureStatusAndCode(security.ErrTablePolicyUnavailable)
	if status != http.StatusServiceUnavailable || code != "TABLE_POLICY_STALE" {
		t.Fatalf("stale policy mapped to (%d, %q)", status, code)
	}
}
