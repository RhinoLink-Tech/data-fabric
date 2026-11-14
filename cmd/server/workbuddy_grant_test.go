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

func TestOperatorGrantIsAuditedAndRevoked(t *testing.T) {
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
	if err = setWorkbuddyGrant(ctx, db, issuer, "alice", "tenant-a", "local-operator", key, "add"); err != nil {
		t.Fatal(err)
	}
	store, _ := onboarding.NewGrantStore(db)
	_, tenant, ok, err := store.Resolve(ctx, issuer, "alice")
	if err != nil || !ok || tenant != "tenant-a" {
		t.Fatal("grant absent")
	}
	if err = setWorkbuddyGrant(ctx, db, issuer, "alice", "wrong-tenant", "local-operator", key, "revoke"); err == nil {
		t.Fatal("wrong tenant revoked grant")
	}
	if err = setWorkbuddyGrant(ctx, db, issuer, "alice", "tenant-a", "local-operator", key, "revoke"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ = store.Resolve(ctx, issuer, "alice"); ok {
		t.Fatal("grant still enabled")
	}
	if err = setWorkbuddyGrant(ctx, db, issuer, "alice", "tenant-a", "local-operator", key, "add"); err == nil {
		t.Fatal("add silently restored revoked grant")
	}
	if err = setWorkbuddyGrant(ctx, db, issuer, "alice", "wrong-tenant", "local-operator", key, "restore"); err == nil {
		t.Fatal("wrong tenant restored grant")
	}
	if err = setWorkbuddyGrant(ctx, db, issuer, "alice", "tenant-a", "local-operator", key, "restore"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ = store.Resolve(ctx, issuer, "alice"); !ok {
		t.Fatal("grant not restored")
	}
	var events []auditlog.Event
	if err = db.Find(&events).Error; err != nil || len(events) != 3 {
		t.Fatalf("audit mismatch: %v %d", err, len(events))
	}
	if strings.Contains(events[0].ReasonCode, "alice") || events[0].ActorID != "local-operator" {
		t.Fatal("audit target or operator invalid")
	}
	if err = db.Exec("INSERT INTO data_sources(owner_id,tenant_id) VALUES (?,?)", "BOB", "other-tenant").Error; err != nil {
		t.Fatal(err)
	}
	if err = setWorkbuddyGrant(ctx, db, issuer, "bob", "tenant-a", "local-operator", key, "add"); err == nil {
		t.Fatal("legacy ownership conflict accepted")
	}
	if err = db.Exec("DROP TABLE audit_events").Error; err != nil {
		t.Fatal(err)
	}
	if err = setWorkbuddyGrant(ctx, db, issuer, "charlie", "tenant-a", "local-operator", key, "add"); err == nil {
		t.Fatal("audit failure accepted")
	}
	if _, _, ok, _ = store.Resolve(ctx, issuer, "charlie"); ok {
		t.Fatal("grant survived failed audit")
	}
}
