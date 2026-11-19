package controller

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/gin-gonic/gin"
)

type editedProbeStub struct {
	config model.DataSourceConfig
	calls  int
	err    error
}

func (s *editedProbeStub) IsSupportedDataSourceType(model.DatabaseType) bool { return true }
func (s *editedProbeStub) ValidateDataSourceConfiguration(*model.DataSourceConfig, model.DatabaseType) error {
	return nil
}
func (s *editedProbeStub) CheckDataSourceConnectivity(ctx context.Context, c *model.DataSourceConfig, _ model.DatabaseType) (*database.HealthCheckResult, error) {
	s.calls++
	s.config = *c
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if s.err != nil {
		return nil, s.err
	}
	return &database.HealthCheckResult{Status: "healthy", Message: c.Password}, nil
}

func TestEditedSourceProbeRetainsAndReplacesCredentials(t *testing.T) {
	for _, tc := range []struct{ name, password, expected string }{{"retained", "", "stored-pass"}, {"replacement", "replacement-pass", "replacement-pass"}} {
		t.Run(tc.name, func(t *testing.T) {
			source := &model.DataSource{ID: authzTestUUID, Type: model.DatabaseTypeMySQL, OwnerID: "owner", TenantID: "team", Config: model.DataSourceConfig{Host: "db.local", Port: 3306, Database: "original", Username: "reader", Password: "stored-pass", AdditionalProps: map[string]interface{}{"token": "saved-token", "region": "north"}}}
			stub := &dataSourceControllerServiceStub{dataSource: source}
			tester := &editedProbeStub{}
			dc := NewDataSourceController(stub)
			dc.SetConnectionTester(tester)
			ctx, rec := authzMutationContext(t, &security.Claims{UserID: "owner", TenantID: "team"}, `{"host":"db.local","port":3306,"database":"edited","username":"reader","password":"`+tc.password+`","additionalProps":{"region":"north"}}`)
			ctx.Params = gin.Params{{Key: "id", Value: authzTestUUID}}
			dc.TestEditedDataSourceConnection(ctx)
			if rec.Code != http.StatusOK || tester.calls != 1 {
				t.Fatalf("status %d calls %d body %s", rec.Code, tester.calls, rec.Body.String())
			}
			if tester.config.Password != tc.expected || tester.config.Database != "edited" || tester.config.AdditionalProps["token"] != "saved-token" {
				t.Fatal("wrong effective config")
			}
			if source.Config.Password != "stored-pass" || source.Config.Database != "original" {
				t.Fatal("probe mutated the saved source")
			}
			for _, secret := range []string{"stored-pass", "replacement-pass", "saved-token"} {
				if strings.Contains(rec.Body.String(), secret) {
					t.Fatal("secret in response")
				}
			}
		})
	}
}

func TestEditedSourceProbeDeniedOrUnsafeWithoutContact(t *testing.T) {
	source := &model.DataSource{ID: authzTestUUID, Type: model.DatabaseTypeMySQL, OwnerID: "owner", TenantID: "team", Config: model.DataSourceConfig{Host: "db.local", Port: 3306, Username: "reader", Password: "stored-pass"}}
	for _, tc := range []struct {
		name   string
		claims *security.Claims
		body   string
		want   int
	}{
		{"tenant reader", &security.Claims{UserID: "other", TenantID: "team"}, `{"host":"db.local","port":3306,"username":"reader"}`, 403},
		{"changed host", &security.Claims{UserID: "owner", TenantID: "team"}, `{"host":"attacker.local","port":3306,"username":"reader"}`, 400},
		{"changed transport", &security.Claims{UserID: "owner", TenantID: "team"}, `{"host":"db.local","port":3306,"username":"reader","tlsServerName":"attacker.local"}`, 400},
		{"changed property", &security.Claims{UserID: "owner", TenantID: "team"}, `{"host":"db.local","port":3306,"username":"reader","additionalProps":{"endpoint":"attacker.local"}}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tester := &editedProbeStub{}
			dc := NewDataSourceController(&dataSourceControllerServiceStub{dataSource: source})
			dc.SetConnectionTester(tester)
			ctx, rec := authzMutationContext(t, tc.claims, tc.body)
			ctx.Params = gin.Params{{Key: "id", Value: authzTestUUID}}
			dc.TestEditedDataSourceConnection(ctx)
			if rec.Code != tc.want || tester.calls != 0 {
				t.Fatalf("status %d calls %d body %s", rec.Code, tester.calls, rec.Body.String())
			}
		})
	}
}

func TestEditedSourceProbeRedactsFailureAndPropagatesCancellation(t *testing.T) {
	source := &model.DataSource{ID: authzTestUUID, Type: model.DatabaseTypeMySQL, OwnerID: "owner", Config: model.DataSourceConfig{Host: "db.local", Port: 3306, Username: "reader", Password: "secret-value"}}
	for _, canceled := range []bool{false, true} {
		tester := &editedProbeStub{err: errors.New("failed with secret-value")}
		dc := NewDataSourceController(&dataSourceControllerServiceStub{dataSource: source})
		dc.SetConnectionTester(tester)
		ctx, rec := authzMutationContext(t, &security.Claims{UserID: "owner"}, `{"host":"db.local","port":3306,"username":"reader"}`)
		ctx.Params = gin.Params{{Key: "id", Value: authzTestUUID}}
		if canceled {
			requestCtx, cancel := context.WithCancel(ctx.Request.Context())
			cancel()
			ctx.Request = ctx.Request.WithContext(requestCtx)
		}
		dc.TestEditedDataSourceConnection(ctx)
		if rec.Code != 500 || tester.calls != 1 || strings.Contains(rec.Body.String(), "secret-value") {
			t.Fatalf("canceled=%v status=%d calls=%d body=%s", canceled, rec.Code, tester.calls, rec.Body.String())
		}
	}
}

// Optional live contract against an isolated synthetic PostgreSQL instance.
// It is intentionally skipped unless its dynamically allocated port is provided.
func TestEditedSourceProbePostgresLive(t *testing.T) {
	portText := os.Getenv("DATA_FABRIC_EDIT_PROBE_PG_PORT")
	if portText == "" {
		t.Skip("no isolated PostgreSQL fixture configured")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	source := &model.DataSource{ID: authzTestUUID, Type: model.DatabaseTypePostgreSQL, OwnerID: "owner", Config: model.DataSourceConfig{Host: "127.0.0.1", Port: port, Database: "postgres", Username: "postgres", Password: "synthetic-515-password"}}
	dc := NewDataSourceController(&dataSourceControllerServiceStub{dataSource: source})
	dc.SetConnectionTester(database.NewHealthChecker(database.NewConnectionPool(nil, nil, nil, false)))
	for _, tc := range []struct{ name, password, status string }{{"retained", "", "healthy"}, {"replacement", "wrong-synthetic-password", "unhealthy"}} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"host":"127.0.0.1","port":` + portText + `,"database":"postgres","username":"postgres","password":"` + tc.password + `"}`
			ctx, rec := authzMutationContext(t, &security.Claims{UserID: "owner"}, body)
			ctx.Params = gin.Params{{Key: "id", Value: authzTestUUID}}
			dc.TestEditedDataSourceConnection(ctx)
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"`+tc.status+`"`) {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), source.Config.Password) || strings.Contains(rec.Body.String(), "wrong-synthetic-password") {
				t.Fatal("credential leaked")
			}
			if source.Config.Password != "synthetic-515-password" {
				t.Fatal("probe persisted the replacement")
			}
		})
	}
}
