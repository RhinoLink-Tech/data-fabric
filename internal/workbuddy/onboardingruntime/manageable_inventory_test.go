package onboardingruntime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestManageableInventoryUsesCurrentGrantTransaction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:manage-inventory-grant?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text, tenant_id text, created_at datetime, updated_at datetime)").Error; err != nil {
		t.Fatal(err)
	}
	if err := onboarding.MigrateGrants(db); err != nil {
		t.Fatal(err)
	}
	vault, err := security.NewCredentialVault([]byte(strings.Repeat("a", 32)))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := repository.NewDataSourceRepository(db, vault)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, source := range []*model.DataSource{
		{Name: "own", OwnerID: "alice", TenantID: "tenant", Type: model.DatabaseTypeMySQL, Config: model.DataSourceConfig{Host: "db", Port: 3306}},
		{Name: "shared", OwnerID: "bob", TenantID: "tenant", Type: model.DatabaseTypeMySQL, Config: model.DataSourceConfig{Host: "db", Port: 3306}},
	} {
		if err := repo.Create(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	store, err := onboarding.NewGrantStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Provision(ctx, "https://issuer.test", "alice", "alice", "tenant"); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	g := grants{store: store, db: db, repo: repo}
	called := 0
	action := func(txctx context.Context) error {
		called++
		sources, total, err := repo.(repository.ManageableSourceLister).ListManageableSources(txctx, repository.SourceWriteScope{OwnerID: "alice", TenantID: "tenant"}, 10, 0)
		if err != nil || total != 1 || len(sources) != 1 || sources[0].Name != "own" {
			t.Fatalf("grant-bound inventory: %+v total=%d err=%v", sources, total, err)
		}
		return nil
	}
	if err := g.WithManagementGrant(ctx, "https://issuer.test", "alice", "alice", "tenant", action); err != nil {
		t.Fatalf("active grant: %v", err)
	}
	if err := store.Revoke(ctx, "https://issuer.test", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := g.WithManagementGrant(ctx, "https://issuer.test", "alice", "alice", "tenant", action); err == nil || called != 1 {
		t.Fatalf("revoked grant ran inventory: called=%d err=%v", called, err)
	}
}
