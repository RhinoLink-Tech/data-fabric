package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// A name-only mutation must preserve changes made after another caller read
// the source, including secret rotation and source deactivation.
func TestScopedRenamePreservesConcurrentChanges(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepository(t, vaultForTest(t))
	ds := &model.DataSource{Name: "orders", Type: model.DatabaseTypeMySQL, OwnerID: "alice", TenantID: "a", Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{Host: "db", Port: 3306, Database: "orders", Username: "reader", Password: "old"}}
	if err := repo.Create(ctx, ds); err != nil {
		t.Fatal(err)
	}
	stale, err := repo.GetByID(ctx, ds.ID)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := repo.GetByID(ctx, ds.ID)
	if err != nil {
		t.Fatal(err)
	}
	fresh.Config.Password = "rotated"
	fresh.Status = model.DataSourceStatusInactive
	if err = repo.Update(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if stale.Config.Password != "old" {
		t.Fatal("fixture did not capture old state")
	}
	mutator := repo.(AtomicSourceManagement)
	renamed, err := mutator.RenameSourceScoped(ctx, SourceWriteScope{OwnerID: "alice", TenantID: "a"}, ds.ID, "orders-new")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "orders-new" || renamed.Status != model.DataSourceStatusInactive {
		t.Fatalf("unexpected result: %+v", renamed)
	}
	got, err := repo.GetByID(ctx, ds.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Password != "rotated" || got.Status != model.DataSourceStatusInactive {
		t.Fatal("rename overwrote another change")
	}
}

func TestScopedSourceMutationsAuthorizeAtomically(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepository(t, vaultForTest(t))
	ds := &model.DataSource{Name: "orders", Type: model.DatabaseTypeMySQL, OwnerID: "alice", TenantID: "a", Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{Host: "db", Port: 3306, Database: "orders", Username: "reader"}}
	if err := repo.Create(ctx, ds); err != nil {
		t.Fatal(err)
	}
	m := repo.(AtomicSourceManagement)
	for _, scope := range []SourceWriteScope{{OwnerID: "alice", TenantID: "b"}, {OwnerID: "bob", TenantID: "a"}, {OwnerID: "ALICE", TenantID: "a"}, {OwnerID: "alice", TenantID: "A"}, {OwnerID: "alice ", TenantID: "a"}, {TenantID: "a"}} {
		if _, err := m.RenameSourceScoped(ctx, scope, ds.ID, "bad"); !errors.Is(err, ErrDataSourceNotFound) {
			t.Fatalf("rename with %+v: %v", scope, err)
		}
		if err := m.SetSourceStatusScoped(ctx, scope, ds.ID, model.DataSourceStatusInactive); !errors.Is(err, ErrDataSourceNotFound) {
			t.Fatalf("status with %+v: %v", scope, err)
		}
	}
	// Idempotent calls remain successful for an authorized caller.
	owner := SourceWriteScope{OwnerID: "alice", TenantID: "a"}
	if _, err := m.RenameSourceScoped(ctx, owner, ds.ID, "orders"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetSourceStatusScoped(ctx, owner, ds.ID, model.DataSourceStatusActive); err != nil {
		t.Fatal(err)
	}
	if err := m.SetSourceStatusScoped(ctx, SourceWriteScope{Admin: true}, ds.ID, model.DataSourceStatusInactive); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, ds.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "orders" || got.Status != model.DataSourceStatusInactive {
		t.Fatalf("unexpected persisted state: %+v", got)
	}
}

func TestScopedRenameCollisionPreservesRecord(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepository(t, vaultForTest(t))
	for _, name := range []string{"orders", "reports"} {
		ds := &model.DataSource{Name: name, Type: model.DatabaseTypeMySQL, OwnerID: "alice", TenantID: "a", Config: model.DataSourceConfig{Host: "db", Port: 3306}}
		if err := repo.Create(ctx, ds); err != nil {
			t.Fatal(err)
		}
	}
	target, err := repo.GetByName(ctx, "a", "reports")
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.(AtomicSourceManagement).RenameSourceScoped(ctx, SourceWriteScope{OwnerID: "alice", TenantID: "a"}, target.ID, "orders")
	if !errors.Is(err, ErrDataSourceExists) {
		t.Fatalf("collision: %v", err)
	}
	got, err := repo.GetByID(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "reports" {
		t.Fatal("collision modified source")
	}
}

func TestScopedIdentityUsesByteEqualityEvenWithSQLiteNoCase(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open("file:identity_nocase?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text collate nocase, tenant_id text collate nocase, created_at datetime, updated_at datetime)").Error; err != nil {
		t.Fatal(err)
	}
	repo, err := NewDataSourceRepository(db, vaultForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ owner, tenant, otherOwner, otherTenant string }{
		{"alice", "tenant", "ALICE", "tenant"},
		{"alice", "tenant", "alice", "TENANT"},
		{"\u00e9", "\u00e9", "e\u0301", "\u00e9"},
		{"\u00e9", "\u00e9", "\u00e9", "e"},
		{"alice", "tenant", "alice ", "tenant"},
	} {
		ds := &model.DataSource{Name: "source", Type: model.DatabaseTypeMySQL, OwnerID: tc.owner, TenantID: tc.tenant, Config: model.DataSourceConfig{Host: "db", Port: 3306}}
		if err = repo.Create(ctx, ds); err != nil {
			t.Fatal(err)
		}
		_, err = repo.(AtomicSourceManagement).RenameSourceScoped(ctx, SourceWriteScope{OwnerID: tc.otherOwner, TenantID: tc.otherTenant}, ds.ID, "bad")
		if !errors.Is(err, ErrDataSourceNotFound) {
			t.Fatalf("nonexact identity: %v", err)
		}
	}
}
