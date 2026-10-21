package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/controller"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAuditEventsRequireAdminEvenWhenManagementAuthIsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:audit-routes-"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := auditlog.Migrate(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	store, err := auditlog.NewStore(db, testJWTSecret, 30)
	if err != nil {
		t.Fatal(err)
	}
	manager := security.NewJWTManager(testJWTSecret, time.Hour)
	admin, err := manager.GenerateToken("admin-1", "team-a", "admin", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.GenerateToken("user-1", "team-a", "user", []string{"user"})
	if err != nil {
		t.Fatal(err)
	}
	router := newRouter(routerDeps{
		cfg:                      &config.Config{Server: config.ServerConfig{Mode: "debug"}, Security: config.SecurityConfig{EnableAuth: false}, Audit: config.AuditConfig{Enabled: true}},
		authMiddleware:           security.NewAuthMiddleware(manager),
		healthController:         controller.NewHealthController(nil),
		datasourceController:     controller.NewDataSourceController(stubDataSourceService{}),
		queryController:          controller.NewQueryController(stubUnifiedQueryService{}),
		databaseController:       controller.NewDatabaseController(nil, nil),
		sqlTranslationController: controller.NewSQLTranslationController(),
		auditController:          controller.NewAuditController(store),
		auditRecorder:            store,
	})
	for _, tc := range []struct {
		name, token string
		want        int
	}{
		{name: "anonymous", want: http.StatusUnauthorized},
		{name: "regular user", token: user, want: http.StatusForbidden},
		{name: "admin", token: admin, want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/audit/events", nil)
			if tc.token != "" {
				request.Header.Set("Authorization", "Bearer "+tc.token)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tc.want, response.Body.String())
			}
		})
	}
}
