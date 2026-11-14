package onboardinghttp

import (
	"bytes"
	"context"
	"errors"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"github.com/google/uuid"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type updateAuditStub struct {
	fail  bool
	calls int
}

func (a *updateAuditStub) Updated(context.Context, *gorm.DB, Actor, string) error {
	a.calls++
	if a.fail {
		return errors.New("audit error")
	}
	return nil
}

type invalidatorStub struct{ calls int }

func (p *invalidatorStub) InvalidateDataSource(string) error { p.calls++; return nil }
func TestUpdateAtomicityRecoveryAndRevocation(t *testing.T) {
	var db *gorm.DB
	var e error
	ddl := `CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null default '', tenant_id text not null default '', created_at datetime, updated_at datetime, UNIQUE (tenant_id, name))`
	if dsn := os.Getenv("DF_ONBOARDING_UPDATE_TEST_MYSQL_DSN"); dsn != "" {
		db, e = gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		ddl = `CREATE TABLE data_sources (id varchar(36) primary key, name varchar(255), type varchar(80), config json not null, status varchar(20), owner_id varchar(255) not null default '', tenant_id varchar(255) not null default '', created_at datetime(6), updated_at datetime(6), UNIQUE KEY uniq_tenant_name (tenant_id, name))`
	} else {
		db, e = gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	}
	if e != nil {
		t.Fatal("open isolated database")
	}
	if e = db.Exec(ddl).Error; e != nil {
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
	ctx := context.Background()
	sourceID := uuid.NewString()
	p := onboarding.Principal{Issuer: "iss", Subject: "user", Tenant: "tenant"}
	ds := &model.DataSource{ID: sourceID, Name: "orders", Type: model.DatabaseTypeMySQL, OwnerID: "user", TenantID: "tenant", Config: model.DataSourceConfig{Host: "127.0.0.1", Port: 3306, Database: "orders", Username: "reader", Password: "old-synthetic"}}
	if e = repo.Create(ctx, ds); e != nil {
		t.Fatal(e)
	}
	flow, e := onboarding.New(db, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	id, e := flow.BeginBoundedUpdate(ctx, p, sourceID, 4, 8, 16)
	if e != nil {
		t.Fatal(e)
	}
	a := &testAuth{actor: Actor{Issuer: "iss", Subject: "user", Tenant: "tenant", UserID: "user", CSRF: "x"}, allow: true, csrf: true}
	h, e := New(db, vault, valid{}, a, flow, &testProbe{}, &testAudit{}, []model.DatabaseType{model.DatabaseTypeMySQL})
	if e != nil {
		t.Fatal(e)
	}
	audit := &updateAuditStub{fail: true}
	pool := &invalidatorStub{}
	cfg := ds.Config
	cfg.Password = "new-synthetic"
	if _, e = h.applyUpdate(ctx, flow, audit, pool, a.actor, id, sourceID, model.DatabaseTypeMySQL, cfg); e == nil {
		t.Fatal("failed audit accepted")
	}
	saved, e := repo.GetByID(ctx, sourceID)
	if e != nil || saved.Config.Password != "old-synthetic" || pool.calls != 0 {
		t.Fatal("failed transaction changed source", e)
	}
	a.allow = false
	audit.fail = false
	if _, e = h.applyUpdate(ctx, flow, audit, pool, a.actor, id, sourceID, model.DatabaseTypeMySQL, cfg); e == nil {
		t.Fatal("revoked grant accepted")
	}
	a.allow = true
	got, e := h.applyUpdate(ctx, flow, audit, pool, a.actor, id, sourceID, model.DatabaseTypeMySQL, cfg)
	if e != nil || got != sourceID {
		t.Fatal(got, e)
	}
	saved, e = repo.GetByID(ctx, sourceID)
	if e != nil || saved.Config.Password != "new-synthetic" {
		t.Fatal("password not updated", e)
	}
	got, e = h.applyUpdate(ctx, flow, audit, pool, a.actor, id, sourceID, model.DatabaseTypeMySQL, cfg)
	if e != nil || got != sourceID || pool.calls != 2 || audit.calls != 2 {
		t.Fatal("retry repeated change or failed", got, e, pool.calls, audit.calls)
	}
	a.allow = false
	if _, e = h.applyUpdate(ctx, flow, audit, pool, a.actor, id, sourceID, model.DatabaseTypeMySQL, cfg); e == nil {
		t.Fatal("revoked recovery accepted")
	}
	a.allow = true
	id2, e := flow.BeginBoundedUpdate(ctx, p, sourceID, 4, 8, 16)
	if e != nil {
		t.Fatal(e)
	}
	payload := `{"name":"","type":"mysql","config":{"host":"127.0.0.1","port":3306,"database":"orders","username":"reader","password":"another-synthetic"}}`
	call := func(action string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "https://example.test/onboarding", bytes.NewBufferString(payload))
		h.ServeUpdateFlow(w, r, flow, audit, pool, id2, sourceID, action)
		return w
	}
	a.csrf = false
	if w := call("update"); w.Code != 403 {
		t.Fatalf("csrf bypass %d", w.Code)
	}
	a.csrf = true
	if w := call("test"); w.Code != 200 || !strings.Contains(w.Body.String(), "true") {
		t.Fatalf("connection test failed %d", w.Code)
	}
	if w := call("update"); w.Code != 200 || strings.Contains(w.Body.String(), "another-synthetic") {
		t.Fatalf("update failed or leaked %d", w.Code)
	}
	saved, e = repo.GetByID(ctx, sourceID)
	if e != nil || saved.Config.Password != "another-synthetic" {
		t.Fatal("HTTP update did not persist", e)
	}
	saved.Config.SSL = true
	saved.Config.TLSCA = "custom"
	saved.Config.TLSServerName = "db.example.test"
	if e = repo.Update(ctx, saved); e != nil {
		t.Fatal(e)
	}
	probe := &tlsRecordingProbe{}
	h.probe = probe
	id3, e := flow.BeginBoundedUpdate(ctx, p, sourceID, 4, 8, 16)
	if e != nil {
		t.Fatal(e)
	}
	invoke := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "https://example.test/onboarding", bytes.NewBufferString(body))
		h.ServeUpdateFlow(w, r, flow, audit, pool, id3, sourceID, "update")
		return w
	}
	downgrade := strings.Replace(payload, `"password":"another-synthetic"`, `"password":"another-synthetic","ssl":false`, 1)
	if w := invoke(downgrade); w.Code != 422 {
		t.Fatalf("TLS downgrade accepted %d", w.Code)
	}
	if w := invoke(payload); w.Code != 200 {
		t.Fatalf("omitted TLS should preserve: %d", w.Code)
	}
	saved, e = repo.GetByID(ctx, sourceID)
	if e != nil || !saved.Config.SSL || saved.Config.TLSCA != "custom" || !probe.last.SSL || probe.last.TLSCA != "custom" {
		t.Fatal("TLS settings not preserved", e)
	}
}

type tlsRecordingProbe struct{ last model.DataSourceConfig }

func (p *tlsRecordingProbe) Pin(_ context.Context, _ model.DatabaseType, c model.DataSourceConfig) (model.DataSourceConfig, error) {
	p.last = c
	if c.SSL {
		c.TLSServerName = c.Host
	}
	return c, nil
}
func (p *tlsRecordingProbe) Test(_ context.Context, _ model.DatabaseType, c model.DataSourceConfig) (bool, error) {
	p.last = c
	return true, nil
}
