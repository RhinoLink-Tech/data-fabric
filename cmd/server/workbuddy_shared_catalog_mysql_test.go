package main

import (
	"context"
	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"os"
	"strings"
	"testing"
)

func TestSharedCatalogMySQLLifecycle(t *testing.T) {
	dsn := os.Getenv("DF_SHARED_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal("isolated MySQL unavailable")
	}
	if err = db.Exec("CREATE TABLE data_sources (id varchar(36) primary key, name varchar(255) not null, type varchar(50) not null, config json not null, status varchar(32), owner_id varchar(255) not null, tenant_id varchar(255) not null, created_at datetime(3), updated_at datetime(3), UNIQUE KEY unique_name(tenant_id,name)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4").Error; err != nil {
		t.Fatal(err)
	}
	if err = onboarding.MigrateGrants(db); err != nil {
		t.Fatal(err)
	}
	if err = auditlog.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&sharedTestSourceRecord{}); err != nil {
		t.Fatal(err)
	}
	vault, err := security.NewCredentialVault([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	entries := []sharedTestEntry{{Key: "synthetic", Name: "Synthetic", Type: model.DatabaseTypeMySQL, ReadOnly: true, Config: model.DataSourceConfig{Host: "10.1.2.3", Port: 3306, Database: "sample", Username: "reader", Password: "temporary-secret"}}}
	ctx := context.Background()
	issuer := "https://issuer.example"
	key := strings.Repeat("a", 32)
	for _, u := range []struct{ subject, tenant string }{{"alice", "tenant-a"}, {"bob", "tenant-b"}} {
		provision := func(ctx context.Context, tx *gorm.DB) error {
			return provisionSharedTestSources(ctx, tx, vault, issuer, u.subject, u.tenant, key, entries)
		}
		if err = provisionSelfServiceGrant(ctx, db, issuer, u.subject, u.tenant, key, nil, provision); err != nil {
			t.Fatal(err)
		}
		if err = provisionSelfServiceGrant(ctx, db, issuer, u.subject, u.tenant, key, nil, provision); err != nil {
			t.Fatal(err)
		}
	}
	var n int64
	if err = db.Table("data_sources").Count(&n).Error; err != nil || n != 2 {
		t.Fatalf("expected two isolated sources, got %d", n)
	}
	revoke := func(ctx context.Context, tx *gorm.DB) error {
		return provisionSharedTestSources(ctx, tx, vault, issuer, "alice", "tenant-a", key, nil)
	}
	if err = provisionSelfServiceGrant(ctx, db, issuer, "alice", "tenant-a", key, nil, revoke); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.Table("data_sources").Select("status").Where("tenant_id = ?", "tenant-a").Scan(&status).Error; err != nil || status != "inactive" {
		t.Fatal("revoked source stayed active")
	}
}
