package main

import (
	"context"
	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedCatalogProvisioningIsTenantScopedAndIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null default '', tenant_id text not null default '', created_at datetime, updated_at datetime, UNIQUE(tenant_id,name))").Error; err != nil {
		t.Fatal(err)
	}
	if err = onboarding.MigrateGrants(db); err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&sharedTestSourceRecord{}); err != nil {
		t.Fatal(err)
	}
	if err = auditlog.Migrate(db); err != nil {
		t.Fatal(err)
	}
	vault, err := security.NewCredentialVault([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	catalog := []sharedTestEntry{{Key: "population", Name: "人口", Type: model.DatabaseTypeMySQL, ReadOnly: true, Config: model.DataSourceConfig{Host: "10.1.2.3", Port: 3306, Database: "population_db", Username: "reader", Password: "synthetic-secret"}}}
	ctx := context.Background()
	key := strings.Repeat("a", 32)
	issuer := "https://issuer.example"
	for _, u := range []struct{ subject, tenant string }{{"alice", "tenant-a"}, {"bob", "tenant-b"}} {
		shared := func(ctx context.Context, tx *gorm.DB) error {
			return provisionSharedTestSources(ctx, tx, vault, issuer, u.subject, u.tenant, key, catalog)
		}
		for i := 0; i < 2; i++ {
			if err = provisionSelfServiceGrant(ctx, db, issuer, u.subject, u.tenant, key, nil, shared); err != nil {
				t.Fatal(err)
			}
		}
	}
	var rows []struct{ ID, OwnerID, TenantID, Config string }
	if err = db.Table("data_sources").Select("id,owner_id,tenant_id,config").Order("tenant_id").Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID == rows[1].ID || rows[0].OwnerID != "alice" || rows[1].OwnerID != "bob" || strings.Contains(rows[0].Config, "synthetic-secret") {
		t.Fatalf("shared source isolation or encryption failed: %d rows", len(rows))
	}
	// Removing a catalog entry disables existing managed records; restoring it
	// applies the new credential without touching other users' records.
	empty := func(ctx context.Context, tx *gorm.DB) error {
		return provisionSharedTestSources(ctx, tx, vault, issuer, "alice", "tenant-a", key, nil)
	}
	if err = provisionSelfServiceGrant(ctx, db, issuer, "alice", "tenant-a", key, nil, empty); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.Table("data_sources").Select("status").Where("tenant_id = ?", "tenant-a").Scan(&status).Error; err != nil || status != "inactive" {
		t.Fatal("removed entry remained active")
	}
	catalog[0].Config.Password = "rotated-synthetic-secret"
	restored := func(ctx context.Context, tx *gorm.DB) error {
		return provisionSharedTestSources(ctx, tx, vault, issuer, "alice", "tenant-a", key, catalog)
	}
	if err = provisionSelfServiceGrant(ctx, db, issuer, "alice", "tenant-a", key, nil, restored); err != nil {
		t.Fatal(err)
	}
	if err = db.Table("data_sources").Select("status").Where("tenant_id = ?", "tenant-a").Scan(&status).Error; err != nil || status != "active" {
		t.Fatal("restored entry was not active")
	}
	store, _ := onboarding.NewGrantStore(db)
	if err = store.Revoke(ctx, issuer, "alice"); err != nil {
		t.Fatal(err)
	}
	shared := func(ctx context.Context, tx *gorm.DB) error {
		return provisionSharedTestSources(ctx, tx, vault, issuer, "alice", "tenant-a", key, catalog)
	}
	if err = provisionSelfServiceGrant(ctx, db, issuer, "alice", "tenant-a", key, nil, shared); err == nil {
		t.Fatal("revoked account was restored")
	}
}
func TestSharedCatalogRejectsUnrestrictedFileAndPolicy(t *testing.T) {
	policy, err := database.NewFixedIPEgressPolicy([]string{"10.1.2.3/32"}, []int{3306})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "catalog.json")
	s := `[{"key":"p","name":"Population","type":"mysql","readOnly":true,"config":{"host":"10.1.2.3","port":3306,"database":"p","username":"reader","password":"test"}}]`
	if err = os.WriteFile(p, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadSharedTestCatalog(p, policy); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = loadSharedTestCatalog(p, policy); err == nil {
		t.Fatal("accepted world-readable credentials")
	}
	if err = os.Chmod(p, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadSharedTestCatalog(p, nil); err == nil {
		t.Fatal("accepted missing egress policy")
	}
}
