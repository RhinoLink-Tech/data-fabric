package onboardingruntime

import (
	"context"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"os"
	"testing"
	"time"
)

func TestGrantRevocationAndCreateSerializeMySQL(t *testing.T) {
	dsn := os.Getenv("DF_WB_E2E_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal("test MySQL unavailable")
	}
	if !db.Migrator().HasTable("df_onboarding_identity_grants") {
		if err = onboarding.MigrateGrants(db); err != nil {
			t.Fatal(err)
		}
	}
	g, err := onboarding.NewGrantStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	issuer := "https://grant-test.example"
	subject := "concurrency-user"
	tenant := "concurrency-tenant"
	if err = g.Provision(ctx, issuer, subject, subject, tenant); err != nil {
		t.Fatal(err)
	}
	locked := make(chan struct{})
	release := make(chan struct{})
	created := make(chan error, 1)
	revoked := make(chan error, 1)
	go func() {
		created <- db.Transaction(func(tx *gorm.DB) error {
			if err := onboarding.LockAndVerifyGrant(ctx, tx, issuer, subject, subject, tenant); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	go func() { revoked <- g.Revoke(ctx, issuer, subject) }()
	select {
	case err := <-revoked:
		t.Fatalf("revoke returned before transaction released: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		return onboarding.LockAndVerifyGrant(ctx, tx, issuer, subject, subject, tenant)
	})
	if err == nil {
		t.Fatal("revoked grant admitted another create")
	}
}

// Verify the actual WorkBuddy management wrapper holds a grant lock through a
// service mutation in the same transaction, including when another replica
// revokes the grant. Requires an isolated disposable MySQL schema.
func TestManagementGrantSerializesRevocationAcrossReplicasMySQL(t *testing.T) {
	dsn := os.Getenv("DF_WB_E2E_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = onboarding.MigrateGrants(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE IF NOT EXISTS test_wb_grant_mutation (id INT PRIMARY KEY, value INT NOT NULL) ENGINE=InnoDB").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("INSERT INTO test_wb_grant_mutation (id,value) VALUES (1,0) ON DUPLICATE KEY UPDATE value=0").Error; err != nil {
		t.Fatal(err)
	}
	store, err := onboarding.NewGrantStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	issuer, subject, tenant := "https://management-test.example", "management-user", "management-tenant"
	if err = store.Provision(ctx, issuer, subject, subject, tenant); err != nil {
		t.Fatal(err)
	}
	first := grants{store: store, db: db}
	second := grants{store: store, db: db}
	started := make(chan struct{})
	release := make(chan struct{})
	operation := make(chan error, 1)
	revoked := make(chan error, 1)
	go func() {
		operation <- first.WithManagementGrant(ctx, issuer, subject, subject, tenant, func(actionCtx context.Context) error {
			close(started)
			<-release
			return repository.SourceManagementDB(actionCtx, db).Exec("UPDATE test_wb_grant_mutation SET value=1 WHERE id=1").Error
		})
	}()
	<-started
	go func() { revoked <- store.Revoke(ctx, issuer, subject) }()
	select {
	case err := <-revoked:
		t.Fatalf("revoke returned before mutation committed: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err = <-operation; err != nil {
		t.Fatal(err)
	}
	if err = <-revoked; err != nil {
		t.Fatal(err)
	}
	var value int
	if err = db.Raw("SELECT value FROM test_wb_grant_mutation WHERE id=1").Scan(&value).Error; err != nil || value != 1 {
		t.Fatalf("mutation did not commit before revoke: %d %v", value, err)
	}
	err = second.WithManagementGrant(ctx, issuer, subject, subject, tenant, func(actionCtx context.Context) error {
		return repository.SourceManagementDB(actionCtx, db).Exec("UPDATE test_wb_grant_mutation SET value=2 WHERE id=1").Error
	})
	if err == nil {
		t.Fatal("other replica allowed a revoked grant")
	}
	if err = db.Raw("SELECT value FROM test_wb_grant_mutation WHERE id=1").Scan(&value).Error; err != nil || value != 1 {
		t.Fatalf("revoked mutation executed: %d %v", value, err)
	}
}

func TestCanceledManagementCannotCommitAfterRevocationMySQL(t *testing.T) {
	dsn := os.Getenv("DF_WB_E2E_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = onboarding.MigrateGrants(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE IF NOT EXISTS test_wb_cancel_mutation (id INT PRIMARY KEY, value INT NOT NULL) ENGINE=InnoDB").Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("INSERT INTO test_wb_cancel_mutation (id,value) VALUES (1,0) ON DUPLICATE KEY UPDATE value=0").Error; err != nil {
		t.Fatal(err)
	}
	store, err := onboarding.NewGrantStore(db)
	if err != nil {
		t.Fatal(err)
	}
	issuer, subject, tenant := "https://cancel-test.example", "cancel-user", "cancel-tenant"
	if err = store.Provision(context.Background(), issuer, subject, subject, tenant); err != nil {
		t.Fatal(err)
	}
	server := grants{store: store, db: db}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- server.WithManagementGrant(ctx, issuer, subject, subject, tenant, func(actionCtx context.Context) error {
			if err := repository.SourceManagementDB(actionCtx, db).Exec("UPDATE test_wb_cancel_mutation SET value=1 WHERE id=1").Error; err != nil {
				return err
			}
			close(started)
			<-release
			return repository.SourceManagementDB(actionCtx, db).Exec("UPDATE test_wb_cancel_mutation SET value=2 WHERE id=1").Error
		})
	}()
	<-started
	cancel()
	revokeCtx, revokeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer revokeCancel()
	if err = store.Revoke(revokeCtx, issuer, subject); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = <-finished; err == nil {
		t.Fatal("canceled transaction returned success")
	}
	var value int
	if err = db.Raw("SELECT value FROM test_wb_cancel_mutation WHERE id=1").Scan(&value).Error; err != nil || value != 0 {
		t.Fatalf("canceled write committed after revocation: %d %v", value, err)
	}
}
