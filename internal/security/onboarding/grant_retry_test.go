package onboarding

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestEnsureProvisionedDoesNotChangeOrRestoreGrants(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:grant-retry-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = MigrateGrants(db); err != nil {
		t.Fatal(err)
	}
	store, err := NewGrantStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	issuer := "https://issuer.example"
	if err = store.EnsureProvisioned(ctx, issuer, "alice", "alice", "tenant-a"); err != nil {
		t.Fatal(err)
	}
	if err = store.EnsureProvisioned(ctx, issuer, "alice", "alice", "tenant-a"); err != nil {
		t.Fatalf("identical retry failed: %v", err)
	}
	if err = store.EnsureProvisioned(ctx, issuer, "alice", "alice", "tenant-b"); err == nil {
		t.Fatal("tenant changed on retry")
	}
	if err = store.EnsureProvisioned(ctx, "https://other.example", "ALICE", "ALICE", "tenant-b"); err == nil {
		t.Fatal("case-folded owner collision accepted")
	}
	if err = store.EnsureProvisioned(ctx, issuer, "bob", "alice", "tenant-a"); err == nil {
		t.Fatal("unmatched subject and owner accepted")
	}
	if err = store.Revoke(ctx, issuer, "alice"); err != nil {
		t.Fatal(err)
	}
	if err = store.EnsureProvisioned(ctx, issuer, "alice", "alice", "tenant-a"); err == nil {
		t.Fatal("retry restored revoked grant")
	}
	if _, _, ok, _ := store.Resolve(ctx, issuer, "alice"); ok {
		t.Fatal("revoked grant became active")
	}
}
