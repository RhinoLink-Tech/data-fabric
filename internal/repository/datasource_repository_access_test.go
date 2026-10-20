package repository

import (
	"context"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// #9 repository-level scoping and per-tenant name uniqueness. The seeds below
// cover every scope class: tenant member (alice/bob in acme), a foreign
// tenant (carol in contoso), a tenantless owner (dave), and an admin-only
// legacy row.

func seedScopeFixture(t *testing.T, repo DataSourceRepository) {
	t.Helper()
	ctx := context.Background()
	rows := []*model.DataSource{
		{ID: "alice-olap", Name: "acme-olap", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, OwnerID: "alice", TenantID: "acme", Config: model.DataSourceConfig{Host: "db", Port: 3306}},
		{ID: "bob-mysql", Name: "acme-mysql", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusInactive, OwnerID: "bob", TenantID: "acme", Config: model.DataSourceConfig{Host: "db", Port: 3306}},
		{ID: "carol-pg", Name: "contoso-pg", Type: model.DatabaseTypePostgreSQL, Status: model.DataSourceStatusActive, OwnerID: "carol", TenantID: "contoso", Config: model.DataSourceConfig{Host: "db", Port: 5432}},
		{ID: "dave-solo", Name: "dave-solo", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, OwnerID: "dave", Config: model.DataSourceConfig{Host: "db", Port: 3306}},
		{ID: "legacy-shared", Name: "legacy-shared", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{Host: "db", Port: 3306}},
	}
	for _, row := range rows {
		if err := repo.Create(ctx, row); err != nil {
			t.Fatalf("seed %s: %v", row.ID, err)
		}
	}
}

func idsOf(sources []*model.DataSource) map[string]bool {
	ids := make(map[string]bool, len(sources))
	for _, ds := range sources {
		ids[ds.ID] = true
	}
	return ids
}

func TestGetAllAppliesAccessScope(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepository(t, vaultForTest(t))
	seedScopeFixture(t, repo)

	tests := []struct {
		name  string
		scope *AccessScope
		want  map[string]bool
	}{
		{"nil scope sees everything (admin / authorization disabled)", nil,
			map[string]bool{"alice-olap": true, "bob-mysql": true, "carol-pg": true, "dave-solo": true, "legacy-shared": true}},
		{"tenant member sees own and tenant rows", &AccessScope{OwnerID: "alice", TenantID: "acme"},
			map[string]bool{"alice-olap": true, "bob-mysql": true}},
		{"tenant member without own rows sees only the tenant", &AccessScope{OwnerID: "eve", TenantID: "acme"},
			map[string]bool{"alice-olap": true, "bob-mysql": true}},
		{"foreign tenant member sees only that tenant's rows", &AccessScope{OwnerID: "eve", TenantID: "contoso"},
			map[string]bool{"carol-pg": true}},
		{"member of no known tenant sees no rows", &AccessScope{OwnerID: "eve", TenantID: "globex"},
			map[string]bool{}},
		{"tenantless caller sees own rows, not other tenantless rows", &AccessScope{OwnerID: "bob"},
			map[string]bool{"bob-mysql": true}},
		{"tenantless caller sees own tenantless row", &AccessScope{OwnerID: "dave"},
			map[string]bool{"dave-solo": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sources, total, err := repo.GetAll(ctx, tt.scope, "", 100, 0)
			if err != nil {
				t.Fatal(err)
			}
			if total != int64(len(tt.want)) {
				t.Fatalf("total = %d, want %d (rows: %v)", total, len(tt.want), idsOf(sources))
			}
			got := idsOf(sources)
			for id := range tt.want {
				if !got[id] {
					t.Errorf("scope %+v missing visible row %s; got %v", tt.scope, id, got)
				}
			}
			for id := range got {
				if !tt.want[id] {
					t.Errorf("scope %+v leaked row %s", tt.scope, id)
				}
			}
		})
	}
}

// The scoped total must reflect the full filtered set, not the page, so
// pagination metadata stays truthful under a narrowed scope.
func TestGetAllScopedTotalDrivesPagination(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepository(t, vaultForTest(t))
	seedScopeFixture(t, repo)

	scope := &AccessScope{OwnerID: "alice", TenantID: "acme"} // 2 visible rows
	page, total, err := repo.GetAll(ctx, scope, "", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || total != 2 {
		t.Fatalf("page = %d rows, total = %d; want 2 rows of 2", len(page), total)
	}
}

func TestCountByStatusAppliesAccessScope(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepository(t, vaultForTest(t))
	seedScopeFixture(t, repo)

	// Alice's scope: alice-olap (active), bob-mysql (inactive).
	counts, err := repo.CountByStatus(ctx, &AccessScope{OwnerID: "alice", TenantID: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if counts[model.DataSourceStatusActive] != 1 || counts[model.DataSourceStatusInactive] != 1 {
		t.Fatalf("scoped counts = %v, want active=1 inactive=1", counts)
	}

	all, err := repo.CountByStatus(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if all[model.DataSourceStatusActive] != 4 || all[model.DataSourceStatusInactive] != 1 {
		t.Fatalf("unscoped counts = %v, want active=4 inactive=1", all)
	}
}

// Name uniqueness is per tenant (#9): the same name may exist in different
// tenants and in the default bucket, but not twice in one bucket.
func TestDataSourceNameUniquenessIsPerTenant(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepository(t, vaultForTest(t))
	config := model.DataSourceConfig{Host: "db", Port: 3306, Database: "a", Username: "u"}

	acmeFirst := &model.DataSource{Name: "warehouse", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, OwnerID: "alice", TenantID: "acme", Config: config}
	if err := repo.Create(ctx, acmeFirst); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &model.DataSource{Name: "warehouse", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, OwnerID: "bob", TenantID: "acme", Config: config}); err == nil {
		t.Fatal("duplicate name within one tenant was accepted")
	}
	if err := repo.Create(ctx, &model.DataSource{Name: "warehouse", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, OwnerID: "carol", TenantID: "contoso", Config: config}); err != nil {
		t.Fatalf("same name in another tenant rejected: %v", err)
	}
	if err := repo.Create(ctx, &model.DataSource{Name: "warehouse", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, OwnerID: "dave", Config: config}); err != nil {
		t.Fatalf("same name in the default bucket rejected: %v", err)
	}
}

func TestGetByNameResolvesWithinTenantBucket(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepository(t, vaultForTest(t))
	config := model.DataSourceConfig{Host: "db", Port: 3306, Database: "a", Username: "u"}
	if err := repo.Create(ctx, &model.DataSource{Name: "warehouse", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, OwnerID: "alice", TenantID: "acme", Config: config}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &model.DataSource{Name: "warehouse", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, OwnerID: "dave", Config: config}); err != nil {
		t.Fatal(err)
	}

	acme, err := repo.GetByName(ctx, "acme", "warehouse")
	if err != nil || acme.OwnerID != "alice" {
		t.Fatalf("GetByName(acme) = %+v, %v; want alice's row", acme, err)
	}
	def, err := repo.GetByName(ctx, "", "warehouse")
	if err != nil || def.OwnerID != "dave" {
		t.Fatalf("GetByName(default bucket) = %+v, %v; want dave's row", def, err)
	}
	if _, err := repo.GetByName(ctx, "contoso", "warehouse"); err != ErrDataSourceNotFound {
		t.Fatalf("GetByName(contoso) error = %v, want ErrDataSourceNotFound", err)
	}
}

// The #9 schema migration must retire the pre-#9 globally unique name index,
// which would keep rejecting same-name sources across tenants, and must do
// so idempotently (it runs at every startup). The model's type column uses a
// MySQL enum DDL tag that SQLite cannot reproduce, so AutoMigrate itself
// cannot run here — the index retirement, the part GORM will not do on its
// own, is exercised directly against a hand-created table.
func TestRetireLegacyNameIndexIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null default '', tenant_id text not null default '', created_at datetime, updated_at datetime)").Error; err != nil {
		t.Fatal(err)
	}

	// No legacy index present: the retire step is a no-op.
	if err := retireLegacyNameIndex(db); err != nil {
		t.Fatalf("retire without legacy index: %v", err)
	}

	if err := db.Exec("CREATE UNIQUE INDEX idx_data_sources_name ON data_sources(name)").Error; err != nil {
		t.Fatal(err)
	}
	if err := retireLegacyNameIndex(db); err != nil {
		t.Fatalf("retire with legacy index: %v", err)
	}
	migrator := db.Migrator()
	if migrator.HasIndex(&model.DataSource{}, legacyNameIndex) {
		t.Fatal("legacy global name index survived retirement")
	}
	// Second pass must not fail on the now-missing index.
	if err := retireLegacyNameIndex(db); err != nil {
		t.Fatalf("second retire pass: %v", err)
	}
}
