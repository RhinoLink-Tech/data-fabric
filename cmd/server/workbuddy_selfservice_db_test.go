package main

import (
	"context"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSelfServiceProvisionTransactionAndRevocation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE data_sources (owner_id text, tenant_id text)").Error; err != nil {
		t.Fatal(err)
	}
	if err = onboarding.MigrateGrants(db); err != nil {
		t.Fatal(err)
	}
	if err = auditlog.Migrate(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := strings.Repeat("k", 32)
	issuer := "https://issuer.example"
	if err = provisionSelfServiceGrant(ctx, db, issuer, "alice", "tenant-a", key, nil); err != nil {
		t.Fatal(err)
	}
	if err = provisionSelfServiceGrant(ctx, db, issuer, "alice", "tenant-a", key, nil); err != nil {
		t.Fatal(err)
	}
	store, _ := onboarding.NewGrantStore(db)
	if err = store.Revoke(ctx, issuer, "alice"); err != nil {
		t.Fatal(err)
	}
	if err = provisionSelfServiceGrant(ctx, db, issuer, "alice", "tenant-a", key, nil); err == nil {
		t.Fatal("revoked grant restored")
	}
	if err = db.Exec("INSERT INTO data_sources(owner_id,tenant_id) VALUES (?,?)", "BOB", "other").Error; err != nil {
		t.Fatal(err)
	}
	if err = provisionSelfServiceGrant(ctx, db, issuer, "bob", "tenant-b", key, nil); err == nil {
		t.Fatal("colliding legacy owner accepted")
	}
	if err = db.Exec("DROP TABLE audit_events").Error; err != nil {
		t.Fatal(err)
	}
	if err = provisionSelfServiceGrant(ctx, db, issuer, "charlie", "tenant-c", key, nil); err == nil {
		t.Fatal("missing audit accepted")
	}
	if _, _, ok, _ := store.Resolve(ctx, issuer, "charlie"); ok {
		t.Fatal("grant survived audit failure")
	}
}
