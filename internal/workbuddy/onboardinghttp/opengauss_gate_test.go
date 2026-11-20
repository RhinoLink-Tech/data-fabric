package onboardinghttp

import (
	"context"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type validOpenGauss struct{}

func (validOpenGauss) IsSupportedDataSourceType(t model.DatabaseType) bool {
	return t == model.DatabaseTypeOpenGauss
}
func (validOpenGauss) ValidateDataSourceConfiguration(*model.DataSourceConfig, model.DatabaseType) error {
	return nil
}

func TestOpenGaussWizardTypeGateAndCreation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "maria.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec(`CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null default '', tenant_id text not null default '', created_at datetime, updated_at datetime, UNIQUE (tenant_id, name))`).Error; err != nil {
		t.Fatal(err)
	}
	if err = onboarding.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = onboarding.MigrateCompletions(db); err != nil {
		t.Fatal(err)
	}
	flow, err := onboarding.New(db, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	principal := onboarding.Principal{Issuer: "https://issuer.test", Subject: "alice", Tenant: "a"}
	id, err := flow.Begin(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := security.NewCredentialVaultWithKeyring("test", map[string][]byte{"test": []byte(strings.Repeat("a", 32))})
	if err != nil {
		t.Fatal(err)
	}
	auth := &testAuth{actor: Actor{Issuer: principal.Issuer, Subject: principal.Subject, Tenant: principal.Tenant, UserID: "alice", CSRF: "csrf"}, allow: true, csrf: true}
	probe := &testProbe{}
	handler, err := New(db, vault, validOpenGauss{}, auth, testStore{flow, id, principal}, probe, &testAudit{}, []model.DatabaseType{model.DatabaseTypeMySQL})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"name":"orders","type":"opengauss","config":{"host":"db.example.test","port":5432,"database":"orders","username":"reader","password":"synthetic-secret","ssl":true}}`
	invoke := func(action string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeFlow(w, httptest.NewRequest("POST", "https://example.test", strings.NewReader(body)), id, action)
		return w
	}
	if handler.AllowsSourceType(model.DatabaseTypeOpenGauss) {
		t.Fatal("disabled type advertised")
	}
	if w := invoke("test"); w.Code != 400 || probe.calls != 0 {
		t.Fatal("disabled type was probed")
	}
	handler, err = New(db, vault, validOpenGauss{}, auth, testStore{flow, id, principal}, probe, &testAudit{}, []model.DatabaseType{model.DatabaseTypeOpenGauss})
	if err != nil {
		t.Fatal(err)
	}
	if !handler.AllowsSourceType(model.DatabaseTypeOpenGauss) {
		t.Fatal("enabled type omitted")
	}
	if w := invoke("test"); w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-secret") {
		t.Fatal("enabled probe failed or leaked")
	}
	if w := invoke("sources"); w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-secret") {
		t.Fatal("enabled creation failed or leaked")
	}
	var row struct {
		Type   string
		Config string
	}
	if err = db.Table("data_sources").Select("type, config").Where("name = ?", "orders").Scan(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Type != "opengauss" || strings.Contains(row.Config, "synthetic-secret") || !strings.Contains(row.Config, "dfcred:v1:") {
		t.Fatal("type or encrypted credential was not preserved")
	}
}

func TestOpenGaussPinRequiresTLS(t *testing.T) {
	probe, err := NewAllowedProbe([]string{"127.0.0.1/32"}, []int{5432}, 1)
	if err != nil {
		t.Fatal(err)
	}
	probe.resolver = loopbackResolver{}
	cfg := model.DataSourceConfig{Host: "localhost", Port: 5432}
	if _, err = probe.Pin(context.Background(), model.DatabaseTypeOpenGauss, cfg); err == nil {
		t.Fatal("plaintext openGauss probe accepted")
	}
	cfg.SSL = true
	pinned, err := probe.Pin(context.Background(), model.DatabaseTypeOpenGauss, cfg)
	if err != nil || pinned.Host != "127.0.0.1" || pinned.TLSServerName != "localhost" {
		t.Fatalf("invalid TLS pin: %+v %v", pinned, err)
	}
}
