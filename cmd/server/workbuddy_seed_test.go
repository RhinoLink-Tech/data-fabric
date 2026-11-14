package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSeedSyntheticCreatesIsolatedEncryptedSourceAndAudits(t *testing.T) {
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
	if err = auditlog.Migrate(db); err != nil {
		t.Fatal(err)
	}
	vault, err := security.NewCredentialVault([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := database.NewFixedIPEgressPolicy([]string{"10.254.81.10/32"}, []int{5432})
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := repository.NewDataSourceRepository(db, vault)
	sample := &model.DataSource{Name: "Sample Orders", Type: model.DatabaseTypePostgreSQL, Status: model.DataSourceStatusActive, OwnerID: "operator", TenantID: "original", Config: model.DataSourceConfig{Host: "10.254.81.10", Port: 5432, Database: "synthetic_orders", Username: "demo_reader", Password: "synthetic-password"}}
	ctx := context.Background()
	if err = repo.Create(ctx, sample); err != nil {
		t.Fatal(err)
	}
	grants, _ := onboarding.NewGrantStore(db)
	issuer := "https://issuer.example"
	if err = grants.Provision(ctx, issuer, "alice", "alice", "tenant-a"); err != nil {
		t.Fatal(err)
	}
	id, err := seedSyntheticForSubject(ctx, db, vault, policy, issuer, "alice", "tenant-a", sample.ID, "operator", strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	clone, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if clone.OwnerID != "alice" || clone.TenantID != "tenant-a" || clone.ID == sample.ID {
		t.Fatal("incorrect isolation")
	}
	var stored string
	if err = db.Raw("SELECT config FROM data_sources WHERE id = ?", id).Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "synthetic-password") {
		t.Fatal("plaintext credential persisted")
	}
	again, err := seedSyntheticForSubject(ctx, db, vault, policy, issuer, "alice", "tenant-a", sample.ID, "operator", strings.Repeat("k", 32))
	if err != nil || again != id {
		t.Fatal("retry not idempotent")
	}
	if err = grants.Revoke(ctx, issuer, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err = seedSyntheticForSubject(ctx, db, vault, policy, issuer, "alice", "tenant-a", sample.ID, "operator", strings.Repeat("k", 32)); err == nil {
		t.Fatal("revoked grant accepted")
	}
	if err = grants.Provision(ctx, issuer, "bob", "bob", "tenant-b"); err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("DROP TABLE audit_events").Error; err != nil {
		t.Fatal(err)
	}
	if _, err = seedSyntheticForSubject(ctx, db, vault, policy, issuer, "bob", "tenant-b", sample.ID, "operator", strings.Repeat("k", 32)); err == nil {
		t.Fatal("missing audit accepted")
	}
	var count int64
	if err = db.Table("data_sources").Where("tenant_id = ?", "tenant-b").Count(&count).Error; err != nil || count != 0 {
		t.Fatal("source survived failed audit")
	}
}

func TestNewAccountGrantAndSampleAreAtomicAndSeededOnce(t *testing.T) {
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
	if err = auditlog.Migrate(db); err != nil {
		t.Fatal(err)
	}
	vault, _ := security.NewCredentialVault([]byte(strings.Repeat("k", 32)))
	policy, _ := database.NewFixedIPEgressPolicy([]string{"10.254.81.10/32"}, []int{5432})
	repo, _ := repository.NewDataSourceRepository(db, vault)
	ctx := context.Background()
	key := strings.Repeat("k", 32)
	issuer := "https://issuer.example"
	sample := &model.DataSource{Name: "Template", Type: model.DatabaseTypePostgreSQL, Status: model.DataSourceStatusActive, OwnerID: "operator", TenantID: "original", Config: model.DataSourceConfig{Host: "10.254.81.10", Port: 5432, Database: "synthetic_orders", Username: "demo_reader", Password: "synthetic-password"}}
	if err = repo.Create(ctx, sample); err != nil {
		t.Fatal(err)
	}
	calls := 0
	seed := func(ctx context.Context, tx *gorm.DB) error {
		calls++
		_, err := seedSyntheticForSubject(ctx, tx, vault, policy, issuer, "alice", "tenant-a", sample.ID, "self-service", key)
		return err
	}
	if err = provisionSelfServiceGrant(ctx, db, issuer, "alice", "tenant-a", key, seed); err != nil {
		t.Fatal(err)
	}
	if err = provisionSelfServiceGrant(ctx, db, issuer, "alice", "tenant-a", key, seed); err != nil || calls != 1 {
		t.Fatal("retry reseeded account")
	}
	if err = db.Exec("DELETE FROM data_sources WHERE tenant_id = ?", "tenant-a").Error; err != nil {
		t.Fatal(err)
	}
	if err = provisionSelfServiceGrant(ctx, db, issuer, "alice", "tenant-a", key, seed); err != nil || calls != 1 {
		t.Fatal("deleted sample was recreated")
	}
	badSeed := func(context.Context, *gorm.DB) error { return errors.New("seed failure") }
	if err = provisionSelfServiceGrant(ctx, db, issuer, "bob", "tenant-b", key, badSeed); err == nil {
		t.Fatal("seed failure accepted")
	}
	grants, _ := onboarding.NewGrantStore(db)
	if _, _, ok, _ := grants.Resolve(ctx, issuer, "bob"); ok {
		t.Fatal("grant survived failed sample setup")
	}
}
