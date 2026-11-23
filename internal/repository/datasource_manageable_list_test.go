package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestManageableSourceInventoryFiltersBeforePagination(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:manageable-inventory?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE data_sources (id text primary key, name text, type text, status text, owner_id text, tenant_id text, config text, created_at datetime, deleted_at datetime)").Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, owner, tenant string }{
		{"own-1", "alice", "team"}, {"own-2", "alice", "team"},
		{"shared", "bob", "team"}, {"foreign", "alice", "other"},
		{"case", "ALICE", "team"}, {"tenant-case", "alice", "TEAM"},
	} {
		// Intentionally invalid ciphertext: inventory must not load or decrypt it.
		if err := db.Exec("INSERT INTO data_sources (id,name,type,status,owner_id,tenant_id,config,created_at) VALUES (?,?,'mysql','active',?,?,?,?)", row.id, row.id, row.owner, row.tenant, `{"password":"plaintext-marker"}`, "2026-01-01").Error; err != nil {
			t.Fatal(err)
		}
	}
	vault, err := security.NewCredentialVaultWithKeyring("test", map[string][]byte{"test": []byte(strings.Repeat("a", 32))})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := NewDataSourceRepository(db, vault)
	if err != nil {
		t.Fatal(err)
	}
	lister := repo.(ManageableSourceLister)
	scope := SourceWriteScope{OwnerID: "alice", TenantID: "team"}
	first, total, err := lister.ListManageableSources(context.Background(), scope, 1, 0)
	if err != nil || total != 2 || len(first) != 1 || first[0].ID != "own-2" {
		t.Fatalf("first page: %+v total=%d err=%v", first, total, err)
	}
	second, total, err := lister.ListManageableSources(context.Background(), scope, 1, 1)
	if err != nil || total != 2 || len(second) != 1 || second[0].ID != "own-1" {
		t.Fatalf("second page: %+v total=%d err=%v", second, total, err)
	}
	empty, total, err := lister.ListManageableSources(context.Background(), scope, 1, 2)
	if err != nil || total != 2 || len(empty) != 0 {
		t.Fatalf("empty page: %+v total=%d err=%v", empty, total, err)
	}
	all, total, err := lister.ListManageableSources(context.Background(), SourceWriteScope{OwnerID: "admin", Admin: true}, 100, 0)
	if err != nil || total != 6 || len(all) != 6 {
		t.Fatalf("admin page: %+v total=%d err=%v", all, total, err)
	}
	for _, bad := range []struct {
		scope         SourceWriteScope
		limit, offset int
	}{
		{SourceWriteScope{}, 1, 0}, {scope, 0, 0}, {scope, 1001, 0}, {scope, 1, -1},
	} {
		if _, _, err := lister.ListManageableSources(context.Background(), bad.scope, bad.limit, bad.offset); err == nil {
			t.Fatalf("invalid scope/page accepted: %+v", bad)
		}
	}

	// The inventory must use the grant-bound transaction, not a second pooled
	// connection that misses uncommitted state or can deadlock a one-slot pool.
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	if err := tx.Exec("INSERT INTO data_sources (id,name,type,status,owner_id,tenant_id,config,created_at) VALUES ('new','new','mysql','active','alice','team','{}','2026-01-01')").Error; err != nil {
		t.Fatal(err)
	}
	ctx, _ := WithSourceManagementTransaction(context.Background(), tx)
	_, total, err = lister.ListManageableSources(ctx, scope, 10, 0)
	if err != nil || total != 3 {
		t.Fatalf("transaction-bound inventory: total=%d err=%v", total, err)
	}
}
