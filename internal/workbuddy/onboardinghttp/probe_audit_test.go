package onboardinghttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProbeDefaultDenyAndAllowlist(t *testing.T) {
	if _, e := NewAllowedProbe(nil, []int{3306}, 1); e == nil {
		t.Fatal("empty network policy accepted")
	}
	if _, e := NewAllowedProbe([]string{"0.0.0.0/0"}, []int{3306}, 1); e == nil {
		t.Fatal("wildcard network policy accepted")
	}
	p, e := NewAllowedProbe([]string{"192.0.2.0/24"}, []int{3306}, 1)
	if e != nil {
		t.Fatal(e)
	}
	ok, e := p.Test(context.Background(), model.DatabaseTypeMySQL, model.DataSourceConfig{Host: "127.0.0.1", Port: 3306})
	if ok || e == nil {
		t.Fatal("unapproved address was probed")
	}
}
func TestTransactionalAuditRollbackAndNoSecrets(t *testing.T) {
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "audit.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.AutoMigrate(&auditlog.Event{}); e != nil {
		t.Fatal(e)
	}
	a := TransactionalAudit{FingerprintKey: strings.Repeat("a", 32)}
	actor := Actor{UserID: "synthetic-user", Tenant: "synthetic-tenant"}
	abort := errors.New("abort")
	e = db.Transaction(func(tx *gorm.DB) error {
		if e := a.Created(context.Background(), tx, actor, "00000000-0000-0000-0000-000000000001"); e != nil {
			return e
		}
		return abort
	})
	if !errors.Is(e, abort) {
		t.Fatal("audit transaction did not abort")
	}
	var count int64
	db.Model(&auditlog.Event{}).Count(&count)
	if count != 0 {
		t.Fatal("audit committed on rollback")
	}
	if e = db.Transaction(func(tx *gorm.DB) error {
		return a.Created(context.Background(), tx, actor, "00000000-0000-0000-0000-000000000001")
	}); e != nil {
		t.Fatal(e)
	}
	var event auditlog.Event
	if e = db.First(&event).Error; e != nil {
		t.Fatal(e)
	}
	if event.ActorID != actor.UserID || event.TenantID != actor.Tenant || event.Operation != "datasource.create" || event.Outcome != "success" {
		t.Fatal("wrong audit event")
	}
	if e = a.Attempt(context.Background(), db, actor, "test", "failure", 400); e != nil {
		t.Fatal(e)
	}
	if e = a.Attempt(context.Background(), db, actor, "recover", "success", 200); e != nil {
		t.Fatal(e)
	}
	var events []auditlog.Event
	if e = db.Find(&events).Error; e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(events)
	if strings.Contains(string(b), "synthetic-password") || strings.Contains(string(b), "db.example.test") || !strings.Contains(string(b), "datasource.recover") || !strings.Contains(string(b), "datasource.test") {
		t.Fatal("unsafe or incomplete audit records")
	}

}
func TestProbeSyntheticDatabases(t *testing.T) {
	mysqlPort, me := strconv.Atoi(os.Getenv("DF_TEST_MYSQL_PORT"))
	pgPort, pe := strconv.Atoi(os.Getenv("DF_TEST_POSTGRES_PORT"))
	if me != nil || pe != nil {
		t.Skip("synthetic database ports not supplied")
	}
	p, e := NewAllowedProbe([]string{"127.0.0.1/32"}, []int{mysqlPort, pgPort}, 2)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		typ  model.DatabaseType
		port int
		user string
	}{{model.DatabaseTypeMySQL, mysqlPort, "root"}, {model.DatabaseTypePostgreSQL, pgPort, "postgres"}} {
		c := model.DataSourceConfig{Host: "127.0.0.1", Port: tc.port, Database: "orders", Username: tc.user, Password: "synthetic-password"}
		ok, e := p.Test(context.Background(), tc.typ, c)
		if !ok || e != nil {
			t.Fatalf("synthetic %s probe failed", tc.typ)
		}
		c.Password = "wrong-synthetic-password"
		ok, e = p.Test(context.Background(), tc.typ, c)
		if ok || e == nil {
			t.Fatalf("synthetic %s bad credential accepted", tc.typ)
		}
		c.SSL = true
		ok, e = p.Test(context.Background(), tc.typ, c)
		if ok || e == nil {
			t.Fatalf("synthetic %s TLS downgrade", tc.typ)
		}
	}
}
func TestBrowserEndpointsSyntheticDatabases(t *testing.T) {
	mp, me := strconv.Atoi(os.Getenv("DF_TEST_MYSQL_PORT"))
	pp, pe := strconv.Atoi(os.Getenv("DF_TEST_POSTGRES_PORT"))
	if me != nil || pe != nil {
		t.Skip("synthetic ports not supplied")
	}
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "metadata.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Exec(`CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null default '', tenant_id text not null default '', created_at datetime, updated_at datetime, UNIQUE (tenant_id, name))`).Error; e != nil {
		t.Fatal(e)
	}
	if e = db.AutoMigrate(&auditlog.Event{}); e != nil {
		t.Fatal(e)
	}
	if e = onboarding.Migrate(db); e != nil {
		t.Fatal(e)
	}
	if e = onboarding.MigrateCompletions(db); e != nil {
		t.Fatal(e)
	}
	st, e := onboarding.New(db, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	vault, e := security.NewCredentialVaultWithKeyring("test", map[string][]byte{"test": []byte(strings.Repeat("a", 32))})
	if e != nil {
		t.Fatal(e)
	}
	probe, e := NewAllowedProbe([]string{"127.0.0.1/32"}, []int{mp, pp}, 2)
	if e != nil {
		t.Fatal(e)
	}
	p := onboarding.Principal{Issuer: "https://issuer.test", Subject: "synthetic", Tenant: "tenant"}
	a := &testAuth{actor: Actor{Issuer: p.Issuer, Subject: p.Subject, Tenant: p.Tenant, UserID: "synthetic", CSRF: "csrf"}, allow: true, csrf: true}
	audit := TransactionalAudit{FingerprintKey: strings.Repeat("a", 32)}
	for _, c := range []struct {
		typ  model.DatabaseType
		port int
		user string
	}{{model.DatabaseTypeMySQL, mp, "root"}, {model.DatabaseTypePostgreSQL, pp, "postgres"}} {
		id, e := st.Begin(context.Background(), p)
		if e != nil {
			t.Fatal(e)
		}
		validator := allowBoth{}
		h, e := New(db, vault, validator, a, testStore{st, id, p}, probe, audit, []model.DatabaseType{c.typ})
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(map[string]any{"name": "synthetic-" + string(c.typ), "type": c.typ, "config": map[string]any{"host": "127.0.0.1", "port": c.port, "database": "orders", "username": c.user, "password": "synthetic-password"}})
		run := func(action string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "https://example.test/onboarding", bytes.NewReader(b))
			h.ServeFlow(w, r, id, action)
			return w
		}
		w := run("test")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"connected":true`) {
			t.Fatalf("%s test failed status %d", c.typ, w.Code)
		}
		w = run("sources")
		if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-password") {
			t.Fatalf("%s create failed status %d", c.typ, w.Code)
		}
	}
	var count int64
	db.Model(&auditlog.Event{}).Count(&count)
	if count != 4 {
		t.Fatalf("expected 4 audit entries, got %d", count)
	}
}

type allowBoth struct{}

func (allowBoth) IsSupportedDataSourceType(t model.DatabaseType) bool {
	return t == model.DatabaseTypeMySQL || t == model.DatabaseTypePostgreSQL
}
func (allowBoth) ValidateDataSourceConfiguration(*model.DataSourceConfig, model.DatabaseType) error {
	return nil
}

func TestDeniedProbePortNeverDials(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	allowed := port + 1
	if allowed > 65535 {
		allowed = port - 1
	}
	p, err := NewAllowedProbe([]string{"127.0.0.1/32"}, []int{allowed}, 1)
	if err != nil {
		t.Fatal(err)
	}
	c := model.DataSourceConfig{Host: "127.0.0.1", Port: port, Username: "reader", Database: "orders"}
	if _, err = p.Pin(context.Background(), model.DatabaseTypePostgreSQL, c); err == nil {
		t.Fatal("pin accepted denied port")
	}
	if _, err = p.Test(context.Background(), model.DatabaseTypePostgreSQL, c); err == nil {
		t.Fatal("probe accepted denied port")
	}
	if err = listener.(*net.TCPListener).SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	conn, err := listener.Accept()
	if err == nil {
		conn.Close()
		t.Fatal("connection reached forbidden port")
	}
	if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatal(err)
	}
}
