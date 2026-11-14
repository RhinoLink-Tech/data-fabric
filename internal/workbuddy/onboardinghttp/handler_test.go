package onboardinghttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type testAuth struct {
	actor Actor
	allow bool
	csrf  bool
}

func (a *testAuth) Current(*http.Request) (Actor, error) {
	if !a.allow {
		return Actor{}, errors.New("denied")
	}
	return a.actor, nil
}
func (a *testAuth) LockAuthorization(_ context.Context, _ *gorm.DB, _ Actor) error {
	if !a.allow {
		return errors.New("revoked")
	}
	return nil
}
func (a *testAuth) CheckMutation(_ *http.Request, _ string) bool { return a.csrf }

type testStore struct {
	*onboarding.Store
	id    string
	actor onboarding.Principal
}

func (s testStore) Inspect(_ context.Context, id string, p onboarding.Principal) (string, string, error) {
	if id != s.id || p != s.actor {
		return "", "", errors.New("not found")
	}
	return "pending", "", nil
}

type valid struct{}

func (valid) IsSupportedDataSourceType(t model.DatabaseType) bool {
	return t == model.DatabaseTypeMySQL
}
func (valid) ValidateDataSourceConfiguration(*model.DataSourceConfig, model.DatabaseType) error {
	return nil
}

type testProbe struct{ calls int }

func (p *testProbe) Pin(_ context.Context, _ model.DatabaseType, c model.DataSourceConfig) (model.DataSourceConfig, error) {
	return c, nil
}
func (p *testProbe) Test(_ context.Context, _ model.DatabaseType, _ model.DataSourceConfig) (bool, error) {
	p.calls++
	return true, nil
}

type testAudit struct{ fail bool }

func (a *testAudit) Attempt(context.Context, *gorm.DB, Actor, string, string, int) error { return nil }
func (a *testAudit) Created(_ context.Context, _ *gorm.DB, _ Actor, _ string) error {
	if a.fail {
		return errors.New("synthetic-audit-failure")
	}
	return nil
}
func TestBoundCreationAndRecovery(t *testing.T) {
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "state.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Exec(`CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null default '', tenant_id text not null default '', created_at datetime, updated_at datetime, UNIQUE (tenant_id, name))`).Error; e != nil {
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
	p := onboarding.Principal{Issuer: "https://issuer.test", Subject: "subject", Tenant: "tenant"}
	id, e := st.Begin(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	vault, e := security.NewCredentialVaultWithKeyring("test", map[string][]byte{"test": []byte(strings.Repeat("a", 32))})
	if e != nil {
		t.Fatal(e)
	}
	a := &testAuth{actor: Actor{Issuer: p.Issuer, Subject: p.Subject, Tenant: p.Tenant, UserID: "canonical", CSRF: "csrf"}, allow: true, csrf: true}
	probe := &testProbe{}
	audit := &testAudit{}
	h, e := New(db, vault, valid{}, a, testStore{st, id, p}, probe, audit, []model.DatabaseType{model.DatabaseTypeMySQL})
	if e != nil {
		t.Fatal(e)
	}
	invoke := func(body, action string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "https://example.test/onboarding", bytes.NewBufferString(body))
		h.ServeFlow(w, r, id, action)
		return w
	}
	bad := `{"name":"order","type":"mysql","config":{"host":"db.test?token=secret","port":3306,"database":"orders","username":"reader","password":"synthetic-secret"}}`
	if w := invoke(bad, "test"); w.Code != 400 || probe.calls != 0 || strings.Contains(w.Body.String(), "synthetic-secret") {
		t.Fatal("unsafe host was accepted or leaked")
	}
	good := `{"name":"order","type":"mysql","config":{"host":"db.test","port":3306,"database":"orders","username":"reader","password":"synthetic-secret"}}`
	a.csrf = false
	if w := invoke(good, "sources"); w.Code != 403 {
		t.Fatal("missing csrf accepted")
	}
	a.csrf = true
	audit.fail = true
	if w := invoke(good, "sources"); w.Code != 400 {
		t.Fatalf("audit failure response %d", w.Code)
	}
	var count int64
	db.Table("data_sources").Count(&count)
	if count != 0 {
		t.Fatal("audit failure committed data source")
	}
	audit.fail = false
	w := invoke(good, "sources")
	if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-secret") {
		t.Fatalf("creation failed: %d", w.Code)
	}
	first := w.Body.String()
	w = invoke(good, "sources")
	if w.Code != 200 || w.Body.String() != first {
		t.Fatal("retry did not recover result")
	}
	db.Table("data_sources").Count(&count)
	if count != 1 {
		t.Fatal("duplicate source")
	}
	var stored struct{ Config string }
	if e = db.Table("data_sources").Select("config").Where("name = ?", "order").Scan(&stored).Error; e != nil || strings.Contains(stored.Config, "synthetic-secret") || !strings.Contains(stored.Config, "dfcred:v1:") {
		t.Fatal("password was not encrypted at rest")
	}
	a.actor.Tenant = "other"
	if w = invoke(good, "sources"); w.Code != 404 {
		t.Fatal("other tenant accessed flow")
	}
	a.actor.Tenant = p.Tenant
	a.allow = false
	if w = invoke(good, "sources"); w.Code != 403 {
		t.Fatal("revoked manager accessed result")
	}
}

// Each create resolves independently and stores the allowed IP. Testing a
// hostname once cannot authorize a later, DNS-rebound create.
type sequenceResolver struct {
	replies [][]netip.Addr
	i       int
}

func (s *sequenceResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	if s.i >= len(s.replies) {
		return nil, errors.New("unexpected lookup")
	}
	x := s.replies[s.i]
	s.i++
	return x, nil
}
func TestCreateRepinsAndRejectsUnknownFields(t *testing.T) {
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "network.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Exec(`CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null default '', tenant_id text not null default '', created_at datetime, updated_at datetime, UNIQUE (tenant_id, name))`).Error; e != nil {
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
	p := onboarding.Principal{Issuer: "https://issuer.test", Subject: "subject", Tenant: "tenant"}
	id, e := st.Begin(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	vault, e := security.NewCredentialVaultWithKeyring("test", map[string][]byte{"test": []byte(strings.Repeat("a", 32))})
	if e != nil {
		t.Fatal(e)
	}
	auth := &testAuth{actor: Actor{Issuer: p.Issuer, Subject: p.Subject, Tenant: p.Tenant, UserID: "canonical", CSRF: "csrf"}, allow: true, csrf: true}
	probe, e := NewAllowedProbe([]string{"192.0.2.0/24"}, []int{3306}, 1)
	if e != nil {
		t.Fatal(e)
	}
	dns := &sequenceResolver{replies: [][]netip.Addr{{netip.MustParseAddr("192.0.2.5")}, {netip.MustParseAddr("127.0.0.1")}, {netip.MustParseAddr("192.0.2.8")}}}
	probe.resolver = dns
	cfg := model.DataSourceConfig{Host: "db.example.test", Port: 3306}
	if _, e = probe.Pin(context.Background(), model.DatabaseTypeMySQL, cfg); e != nil {
		t.Fatal("first resolution should pass")
	}
	h, e := New(db, vault, valid{}, auth, testStore{st, id, p}, probe, &testAudit{}, []model.DatabaseType{model.DatabaseTypeMySQL})
	if e != nil {
		t.Fatal(e)
	}
	invoke := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "https://example.test", strings.NewReader(body))
		h.ServeFlow(w, r, id, "sources")
		return w
	}
	bad := `{"name":"orders","type":"mysql","config":{"host":"db.example.test","port":3306,"database":"orders","username":"user","password":"secret","maxPoolSize":100000}}`
	if w := invoke(bad); w.Code != 400 || dns.i != 1 {
		t.Fatal("unexpected resource option reached resolver")
	}
	fifoPath := filepath.Join(t.TempDir(), "untrusted-fifo")
	if runtime.GOOS == "linux" {
		if e := exec.Command("mkfifo", "-m", "600", fifoPath).Run(); e != nil {
			t.Fatal(e)
		}
	}
	fifo := fmt.Sprintf(`{"name":"orders","type":"mysql","config":{"host":"db.example.test","port":3306,"database":"orders","username":"user","password":"secret","sslRootCert":%q}}`, fifoPath)
	if w := invoke(fifo); w.Code != 400 || dns.i != 1 {
		t.Fatal("unexpected certificate path reached resolver")
	}
	injected := `{"name":"orders","type":"mysql","config":{"host":"db.example.test","port":3306,"database":"orders","username":"user:other","password":"secret"}}`
	if w := invoke(injected); w.Code != 400 || dns.i != 1 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal("DSN injection accepted or leaked")
	}
	for _, badField := range []string{`"database":"","username":"user"`, `"database":"orders","username":""`} {
		body := `{"name":"orders","type":"mysql","config":{"host":"db.example.test","port":3306,` + badField + `,"password":"synthetic"}}`
		if w := invoke(body); w.Code != 400 || dns.i != 1 {
			t.Fatal("empty required database identity accepted")
		}
	}
	good := `{"name":"orders","type":"mysql","config":{"host":"db.example.test","port":3306,"database":"orders","username":"user","password":"s@fe:/?&=中文"}}`
	if w := invoke(good); w.Code != 400 || dns.i != 2 {
		t.Fatal("DNS rebound address accepted")
	}
	if w := invoke(good); w.Code != 200 || dns.i != 3 {
		t.Fatalf("allowed direct creation failed: %d", w.Code)
	}
	var row struct{ Config string }
	if e = db.Table("data_sources").Select("config").Where("name = ?", "orders").Scan(&row).Error; e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(row.Config, "192.0.2.8") || strings.Contains(row.Config, "db.example.test") || !strings.Contains(row.Config, `"maxPoolSize":2`) {
		t.Fatal("approved IP or fixed pool was not persisted")
	}
}

func TestTLSPinningRejectsDNSRebinding(t *testing.T) {
	p, err := NewAllowedProbe([]string{"192.0.2.0/24"}, []int{5432}, 1)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &sequenceResolver{replies: [][]netip.Addr{{netip.MustParseAddr("192.0.2.9")}, {netip.MustParseAddr("169.254.169.254")}}}
	p.resolver = resolver
	cfg := model.DataSourceConfig{Host: "db.example.test", Port: 5432, SSL: true, TLSCA: "system"}
	first, err := p.Pin(context.Background(), model.DatabaseTypePostgreSQL, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if first.Host != "192.0.2.9" || first.TLSServerName != cfg.Host {
		t.Fatalf("incorrect pinned target: %+v", first)
	}
	if _, err = p.Pin(context.Background(), model.DatabaseTypePostgreSQL, cfg); err == nil {
		t.Fatal("DNS rebinding accepted")
	}
	if first.Host != "192.0.2.9" {
		t.Fatal("pinned configuration mutated")
	}
}
