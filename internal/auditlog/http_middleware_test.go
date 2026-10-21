package auditlog

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/gin-gonic/gin"
)

func TestHTTPMiddlewarePersistsDeniedQueryWithoutRawSQLAndPreservesBody(t *testing.T) {
	store := testStore(t, "stable deployment secret", 30)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(HTTPMiddleware(store, 1024))
	router.POST("/api/v1/fetch", func(c *gin.Context) {
		var payload map[string]any
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		if payload["sql"] != "SELECT id FROM orders WHERE id = 7" {
			c.Status(http.StatusBadRequest)
			return
		}
		c.Set("user_claims", &security.Claims{UserID: "alice", TenantID: "team-a"})
		c.Set("correlation_id", "request-123")
		c.Set(RowCountContextKey, 2)
		c.Status(http.StatusForbidden)
	})
	body := []byte(`{"dataSourceId":"source-1","sql":"SELECT id FROM orders WHERE id = 7"}`)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/fetch", bytes.NewReader(body))
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
	page, err := store.List(context.Background(), Filter{CorrelationID: "request-123"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Fatalf("audit event count = %d, want 1", page.Total)
	}
	event := page.Events[0]
	if event.ActorID != "alice" || event.TenantID != "team-a" || event.DataSourceID != "source-1" || event.Decision != DecisionDeny || event.RowsReturned != 2 {
		t.Fatalf("audit event missing request identity or outcome fields: %+v", event)
	}
	if event.QueryFingerprint == "" {
		t.Fatal("query fingerprint was not recorded")
	}
	encoded, _ := json.Marshal(event)
	if bytes.Contains(encoded, []byte("SELECT id FROM orders")) {
		t.Fatal("raw SQL was persisted")
	}
}

func TestHTTPMiddlewareRecordsSuccessAndFailure(t *testing.T) {
	store := testStore(t, "stable deployment secret", 30)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(HTTPMiddleware(store, 1024))
	router.GET("/api/v1/success", func(c *gin.Context) { c.Set("correlation_id", "success-1"); c.Status(http.StatusOK) })
	router.GET("/api/v1/failure", func(c *gin.Context) { c.Set("correlation_id", "failure-1"); c.Status(http.StatusInternalServerError) })
	for _, path := range []string{"/api/v1/success", "/api/v1/failure"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	}
	page, err := store.List(context.Background(), Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Fatalf("audit event count = %d, want 2", page.Total)
	}
	byID := map[string]Event{}
	for _, event := range page.Events {
		byID[event.CorrelationID] = event
	}
	if byID["success-1"].Decision != DecisionAllow || byID["success-1"].Outcome != "success" {
		t.Fatalf("success event = %+v", byID["success-1"])
	}
	if byID["failure-1"].Decision != DecisionError || byID["failure-1"].Outcome != "failed" {
		t.Fatalf("failure event = %+v", byID["failure-1"])
	}
}
