package service

import (
	"context"
	"errors"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRESTMutationMySQLLocksAuthorization(t *testing.T) {
	dsn := os.Getenv("DF_ATOMIC_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires disposable MySQL")
	}
	for _, action := range []string{"activate", "deactivate", "delete"} {
		t.Run(action, func(t *testing.T) {
			db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if err = db.Exec("CREATE TABLE data_sources (id varchar(36) primary key, name varchar(255), type varchar(80), config json not null, status varchar(20), owner_id varchar(255) not null default " + string(rune(39)) + string(rune(39)) + ", tenant_id varchar(255) not null default " + string(rune(39)) + string(rune(39)) + ", created_at datetime(6), updated_at datetime(6), UNIQUE KEY uniq_tenant_name (tenant_id, name))").Error; err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Exec("DROP TABLE data_sources") })
			vault, err := security.NewCredentialVaultWithKeyring("key", map[string][]byte{"key": []byte(strings.Repeat("a", 32))})
			if err != nil {
				t.Fatal(err)
			}
			repo, err := repository.NewDataSourceRepository(db, vault)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			source := &model.DataSource{Name: "orders", Type: model.DatabaseTypeMySQL, OwnerID: "alice", TenantID: "a", Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{Host: "db", Port: 3306, Database: "a", Username: "u"}}
			if err = repo.Create(ctx, source); err != nil {
				t.Fatal(err)
			}
			tx := db.Begin()
			if tx.Error != nil {
				t.Fatal(tx.Error)
			}
			defer tx.Rollback()
			var locked model.DataSource
			if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", source.ID).First(&locked).Error; err != nil {
				t.Fatal(err)
			}
			if err = tx.Model(&model.DataSource{}).Where("id = ?", source.ID).Update("owner_id", "bob").Error; err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			svc := NewDataSourceService(repo, nil, &datasourceConfigValidatorStub{supported: true})
			go func() {
				caller := &security.Caller{UserID: "alice", TenantID: "a"}
				switch action {
				case "activate":
					done <- svc.ActivateDataSource(ctx, caller, source.ID)
				case "deactivate":
					done <- svc.DeactivateDataSource(ctx, caller, source.ID)
				case "delete":
					done <- svc.DeleteDataSource(ctx, caller, source.ID)
				}
			}()
			select {
			case e := <-done:
				t.Fatalf("write did not wait for row lock: %v", e)
			case <-time.After(150 * time.Millisecond):
			}
			if err = tx.Commit().Error; err != nil {
				t.Fatal(err)
			}
			select {
			case e := <-done:
				if !errors.Is(e, security.ErrDataSourceAccessDenied) {
					t.Fatalf("wanted denied, got %v", e)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("mutation did not resume")
			}
			got, err := repo.GetByID(ctx, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.OwnerID != "bob" || got.Status != model.DataSourceStatusActive {
				t.Fatal("unauthorized operation changed row")
			}
		})
	}
}
