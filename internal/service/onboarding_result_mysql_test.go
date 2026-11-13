package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The DSN must designate a disposable database. This test creates tables there.
func TestMySQLOnboardingResultCreatesSourceExactlyOnce(t *testing.T) {
	dsn := os.Getenv("DF_ONBOARDING_ISOLATED_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL DSN required")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = db.Exec(`CREATE TABLE data_sources (id varchar(36) primary key, name varchar(255) not null, type varchar(80), config json not null, status varchar(20), owner_id varchar(255) not null default '', tenant_id varchar(255) not null default '', created_at datetime(6), updated_at datetime(6), UNIQUE KEY unique_tenant_name (tenant_id,name))`).Error; err != nil {
		t.Fatal(err)
	}
	if err = onboarding.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = onboarding.MigrateCompletions(db); err != nil {
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
	p := onboarding.Principal{Issuer: "https://issuer.example", Subject: "user", Tenant: "tenant"}
	caller := &security.Caller{UserID: p.Subject, TenantID: p.Tenant}
	id, err := store.Begin(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	var called atomic.Int32
	create := func(tx *gorm.DB) (string, error) {
		called.Add(1)
		repo, e := repository.NewDataSourceRepository(tx, vault)
		if e != nil {
			return "", e
		}
		svc := NewDataSourceService(repo, nil, &datasourceConfigValidatorStub{supported: true})
		ds, e := svc.CreateDataSource(ctx, caller, &CreateDataSourceRequest{Name: "synthetic", Type: model.DatabaseTypeMySQL, Config: model.DataSourceConfig{Host: "db.example", Database: "orders", Username: "reader", Password: "synthetic-secret"}})
		if e != nil {
			return "", e
		}
		return ds.ID, nil
	}
	abort := errors.New("rollback")
	_, err = store.ApplyResult(ctx, id, p, func(tx *gorm.DB) (string, error) {
		_, e := create(tx)
		if e != nil {
			return "", e
		}
		return "", abort
	})
	if !errors.Is(err, abort) {
		t.Fatalf("rollback: %v", err)
	}
	var count int64
	if e := db.Table("data_sources").Count(&count).Error; e != nil || count != 0 {
		t.Fatalf("rollback count %d err %v", count, e)
	}
	called.Store(0)
	const n = 12
	var wg sync.WaitGroup
	ids := make(chan string, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x, e := store.ApplyResult(ctx, id, p, create)
			if e != nil {
				errs <- e
			} else {
				ids <- x
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Errorf("apply: %v", e)
	}
	first := ""
	for x := range ids {
		if first == "" {
			first = x
		}
		if x != first {
			t.Fatal("inconsistent result")
		}
	}
	if first == "" || called.Load() != 1 {
		t.Fatalf("created=%q callbacks=%d", first, called.Load())
	}
	// Simulate a lost successful HTTP response by repeating with the same identity.
	recovered, e := store.ApplyResult(ctx, id, p, create)
	if e != nil || recovered != first || called.Load() != 1 {
		t.Fatalf("recovery err=%v", e)
	}
	if _, e = store.ApplyResult(ctx, id, onboarding.Principal{Issuer: p.Issuer, Subject: "other", Tenant: p.Tenant}, create); !errors.Is(e, onboarding.ErrUnavailable) {
		t.Fatalf("cross principal %v", e)
	}
	var stored struct {
		OwnerID  string
		TenantID string
		Config   string
	}
	if e = db.Table("data_sources").Select("owner_id,tenant_id,config").Where("id = ?", first).Scan(&stored).Error; e != nil {
		t.Fatal(e)
	}
	if stored.OwnerID != p.Subject || stored.TenantID != p.Tenant || strings.Contains(stored.Config, "synthetic-secret") || !strings.Contains(stored.Config, "dfcred:v1:") {
		t.Fatal("ownership/encryption mismatch")
	}
}
