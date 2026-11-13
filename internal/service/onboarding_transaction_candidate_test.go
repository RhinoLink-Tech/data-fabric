package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestOnboardingCreateUsesSharedTransaction(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "onboarding.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec(`CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null default '', tenant_id text not null default '', created_at datetime, updated_at datetime, UNIQUE (tenant_id, name))`).Error; err != nil {
		t.Fatal(err)
	}
	if err = onboarding.Migrate(db); err != nil {
		t.Fatal(err)
	}
	store, err := onboarding.New(db, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := security.NewCredentialVaultWithKeyring("key-1", map[string][]byte{"key-1": []byte(strings.Repeat("a", 32))})
	if err != nil {
		t.Fatal(err)
	}
	actor := onboarding.Principal{Issuer: "https://issuer.example", Subject: "synthetic-user", Tenant: "synthetic-tenant"}
	caller := &security.Caller{UserID: actor.Subject, TenantID: actor.Tenant}
	id, err := store.Begin(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	req := &CreateDataSourceRequest{Name: "synthetic", Type: model.DatabaseTypeMySQL, Config: model.DataSourceConfig{Host: "db.example", Database: "orders", Username: "reader", Password: "synthetic-secret"}}
	fail := errors.New("abort transaction")
	create := func(tx *gorm.DB, abort bool) error {
		repo, err := repository.NewDataSourceRepository(tx, vault)
		if err != nil {
			return err
		}
		svc := NewDataSourceService(repo, nil, &datasourceConfigValidatorStub{supported: true})
		_, err = svc.CreateDataSource(ctx, caller, req)
		if err != nil {
			return err
		}
		if abort {
			return fail
		}
		return nil
	}
	if err = store.ConsumeAndApply(ctx, id, actor, func(tx *gorm.DB) error { return create(tx, true) }); !errors.Is(err, fail) {
		t.Fatalf("expected rollback: %v", err)
	}
	var count int64
	if err = db.Table("data_sources").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rollback persisted count=%d err=%v", count, err)
	}
	if err = store.ConsumeAndApply(ctx, id, actor, func(tx *gorm.DB) error { return create(tx, false) }); err != nil {
		t.Fatal(err)
	}
	var stored struct {
		OwnerID  string
		TenantID string
		Config   string
	}
	if err = db.Table("data_sources").Select("owner_id, tenant_id, config").Where("name = ?", req.Name).Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.OwnerID != actor.Subject || stored.TenantID != actor.Tenant || strings.Contains(stored.Config, "synthetic-secret") || !strings.Contains(stored.Config, "dfcred:v1:") {
		t.Fatal("ownership or credential storage mismatch")
	}
}
