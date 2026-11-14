package onboardinghttp

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type loopbackResolver struct{}

func (loopbackResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
}

type actualTLSProbe struct {
	base   *AllowedProbe
	policy *database.FixedIPEgressPolicy
}

func (p actualTLSProbe) Pin(ctx context.Context, typ model.DatabaseType, c model.DataSourceConfig) (model.DataSourceConfig, error) {
	pinned, e := p.base.Pin(ctx, typ, c)
	if e != nil {
		return pinned, e
	}
	return pinned, p.policy.Check(&model.DataSource{Type: typ, Config: pinned})
}
func (p actualTLSProbe) Test(ctx context.Context, typ model.DatabaseType, c model.DataSourceConfig) (bool, error) {
	pinned, e := p.Pin(ctx, typ, c)
	if e != nil {
		return false, e
	}
	return p.base.TestPinned(ctx, typ, pinned, p.policy)
}

type tlsValid struct{}

func (tlsValid) IsSupportedDataSourceType(t model.DatabaseType) bool {
	return t == model.DatabaseTypeMySQL || t == model.DatabaseTypePostgreSQL
}
func (tlsValid) ValidateDataSourceConfiguration(*model.DataSourceConfig, model.DatabaseType) error {
	return nil
}
func TestTLSUpdateRealDatabases(t *testing.T) {
	ca := os.Getenv("DF_TLS_CA")
	badCA := os.Getenv("DF_TLS_BAD_CA")
	pwd := os.Getenv("DF_TLS_PASSWORD")
	if ca == "" || badCA == "" || pwd == "" {
		t.Skip("isolated TLS fixtures not set")
	}
	for _, tc := range []struct {
		env, user string
		typ       model.DatabaseType
	}{{"DF_TLS_POSTGRES", "postgres", model.DatabaseTypePostgreSQL}, {"DF_TLS_MYSQL", "root", model.DatabaseTypeMySQL}} {
		t.Run(string(tc.typ), func(t *testing.T) {
			_, portText, e := net.SplitHostPort(os.Getenv(tc.env))
			if e != nil {
				t.Fatal(e)
			}
			port, e := strconv.Atoi(portText)
			if e != nil {
				t.Fatal(e)
			}
			policy, e := database.NewFixedIPEgressPolicyWithCA([]string{"127.0.0.1/32"}, []int{port}, ca)
			if e != nil {
				t.Fatal(e)
			}
			badPolicy, e := database.NewFixedIPEgressPolicyWithCA([]string{"127.0.0.1/32"}, []int{port}, badCA)
			if e != nil {
				t.Fatal(e)
			}
			allowed, e := NewAllowedProbe([]string{"127.0.0.1/32"}, []int{port}, 2)
			if e != nil {
				t.Fatal(e)
			}
			allowed.resolver = loopbackResolver{}
			pool, e := database.NewRestrictedConnectionPool(nil, nil, nil, true, policy)
			if e != nil {
				t.Fatal(e)
			}
			defer pool.CloseAll()
			db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "meta.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if e != nil {
				t.Fatal(e)
			}
			if e = db.Exec(`CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null default '', tenant_id text not null default '', created_at datetime, updated_at datetime, UNIQUE (tenant_id, name))`).Error; e != nil {
				t.Fatal(e)
			}
			for _, m := range []func(*gorm.DB) error{onboarding.Migrate, onboarding.MigrateCompletions, onboarding.MigrateQuota} {
				if e = m(db); e != nil {
					t.Fatal(e)
				}
			}
			vault, e := security.NewCredentialVaultWithKeyring("test", map[string][]byte{"test": []byte(strings.Repeat("a", 32))})
			if e != nil {
				t.Fatal(e)
			}
			repo, e := repository.NewDataSourceRepository(db, vault)
			if e != nil {
				t.Fatal(e)
			}
			actor := Actor{Issuer: "iss", Subject: "user", Tenant: "tenant", UserID: "user", CSRF: "x"}
			principal := onboarding.Principal{Issuer: "iss", Subject: "user", Tenant: "tenant"}
			ds := &model.DataSource{ID: uuid.NewString(), Name: "test", Type: tc.typ, OwnerID: "user", TenantID: "tenant", Config: model.DataSourceConfig{Host: "127.0.0.1", Port: port, Database: "synthetic", Username: tc.user, Password: pwd, SSL: true, TLSCA: "custom", TLSServerName: "db.example.test"}}
			if e = repo.Create(context.Background(), ds); e != nil {
				t.Fatal(e)
			}
			flow, e := onboarding.New(db, time.Minute)
			if e != nil {
				t.Fatal(e)
			}
			id, e := flow.BeginBoundedUpdate(context.Background(), principal, ds.ID, 4, 8, 16)
			if e != nil {
				t.Fatal(e)
			}
			auth := &testAuth{actor: actor, allow: true, csrf: true}
			probe := actualTLSProbe{allowed, policy}
			h, e := New(db, vault, tlsValid{}, auth, flow, probe, &testAudit{}, []model.DatabaseType{tc.typ})
			if e != nil {
				t.Fatal(e)
			}
			audit := &updateAuditStub{}
			inv := &invalidatorStub{}
			input := map[string]any{"name": "", "type": tc.typ, "config": map[string]any{"host": "db.example.test", "port": port, "database": "synthetic", "username": tc.user, "password": pwd}}
			invoke := func(action string) *httptest.ResponseRecorder {
				data, _ := json.Marshal(input)
				w := httptest.NewRecorder()
				req := httptest.NewRequest("POST", "https://example.test", bytes.NewReader(data))
				h.ServeUpdateFlow(w, req, flow, audit, inv, id, ds.ID, action)
				return w
			}
			c := input["config"].(map[string]any)
			c["host"] = "wrong.example.test"
			if w := invoke("test"); w.Code == 200 {
				t.Fatal("wrong hostname accepted")
			}
			c["host"] = "db.example.test"
			h.probe = actualTLSProbe{allowed, badPolicy}
			if w := invoke("test"); w.Code == 200 {
				t.Fatal("wrong CA accepted")
			}
			h.probe = probe
			c["ssl"] = false
			if w := invoke("update"); w.Code != 422 {
				t.Fatal("downgrade accepted", w.Code)
			}
			delete(c, "ssl")
			if w := invoke("test"); w.Code != 200 {
				t.Fatal("correct TLS probe rejected", w.Code)
			}
			if w := invoke("update"); w.Code != 200 {
				t.Fatal("TLS update failed", w.Code)
			}
			current, e := repo.GetByID(context.Background(), ds.ID)
			if e != nil || !current.Config.SSL || current.Config.TLSCA != "custom" || current.Config.TLSServerName != "db.example.test" {
				t.Fatal("TLS settings not preserved", e)
			}
			conn, e := pool.GetConnection(context.Background(), current)
			if e != nil {
				t.Fatal("TLS query connection failed", e)
			}
			var value int
			if e = conn.QueryRow("SELECT 1").Scan(&value); e != nil || value != 1 {
				t.Fatal("TLS query failed", e)
			}
		})
	}
}
