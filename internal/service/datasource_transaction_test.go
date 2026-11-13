package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type beforeLockedUpdate struct {
	repository.DataSourceRepository
	before func()
}

func (r *beforeLockedUpdate) UpdateSourceLocked(ctx context.Context, id string, fields []string, fn func(*model.DataSource) error) (*model.DataSource, error) {
	if r.before != nil {
		r.before()
	}
	return r.DataSourceRepository.(repository.LockedSourceUpdater).UpdateSourceLocked(ctx, id, fields, fn)
}
func transactionFixture(t *testing.T) (repository.DataSourceRepository, *model.DataSource) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null default '', tenant_id text not null default '', created_at datetime, updated_at datetime, UNIQUE (tenant_id, name))").Error; err != nil {
		t.Fatal(err)
	}
	vault, err := security.NewCredentialVaultWithKeyring("key", map[string][]byte{"key": []byte(strings.Repeat("a", 32))})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := repository.NewDataSourceRepository(db, vault)
	if err != nil {
		t.Fatal(err)
	}
	source := &model.DataSource{Name: "orders", Type: model.DatabaseTypeMySQL, OwnerID: "alice", TenantID: "a", Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{Host: "db", Port: 3306, Database: "orders", Username: "reader", Password: "password", AdditionalProps: map[string]interface{}{"access_token": "old"}}}
	if err = repo.Create(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	return repo, source
}
func TestUpdateMergesAgainstLatestLockedSnapshot(t *testing.T) {
	for _, kind := range []string{"rename", "config", "status"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			repo, source := transactionFixture(t)
			store := repo.(interface {
				CompareAndSwapAccessToken(context.Context, string, string, string, time.Time) (bool, error)
			})
			wrapper := &beforeLockedUpdate{DataSourceRepository: repo, before: func() {
				ok, err := store.CompareAndSwapAccessToken(ctx, source.ID, "old", "new", time.Now().Add(time.Hour))
				if err != nil || !ok {
					t.Fatalf("rotate: %v %v", ok, err)
				}
			}}
			svc := NewDataSourceService(wrapper, nil, &datasourceConfigValidatorStub{supported: true})
			req := &UpdateDataSourceRequest{}
			switch kind {
			case "rename":
				n := "renamed"
				req.Name = &n
			case "config":
				req.Config = &model.DataSourceConfig{Host: "new-host", Port: 3306, Database: "orders", Username: "reader"}
			case "status":
				s := model.DataSourceStatusInactive
				req.Status = &s
			}
			_, err := svc.UpdateDataSource(ctx, &security.Caller{UserID: "alice", TenantID: "a"}, source.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := repo.GetByID(ctx, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Config.AdditionalProps["access_token"] != "new" || stored.Config.Password != "password" {
				t.Fatal("update lost rotated credentials")
			}
			if kind == "rename" && stored.Name != "renamed" {
				t.Fatal("rename was not applied")
			}
			if kind == "config" && stored.Config.Host != "new-host" {
				t.Fatal("config was not applied")
			}
			if kind == "status" && stored.Status != model.DataSourceStatusInactive {
				t.Fatal("status was not applied")
			}
		})
	}
}
func TestUpdateAuthorizesLockedOwner(t *testing.T) {
	ctx := context.Background()
	repo, source := transactionFixture(t)
	wrapper := &beforeLockedUpdate{DataSourceRepository: repo, before: func() {
		updated, err := repo.GetByID(ctx, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		updated.OwnerID = "bob"
		if err = repo.Update(ctx, updated); err != nil {
			t.Fatal(err)
		}
	}}
	svc := NewDataSourceService(wrapper, nil, &datasourceConfigValidatorStub{supported: true})
	name := "renamed"
	_, err := svc.UpdateDataSource(ctx, &security.Caller{UserID: "alice", TenantID: "a"}, source.ID, &UpdateDataSourceRequest{Name: &name})
	if !errors.Is(err, security.ErrDataSourceAccessDenied) {
		t.Fatalf("owner changed: %v", err)
	}
	got, err := repo.GetByID(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerID != "bob" || got.Name != "orders" {
		t.Fatal("unauthorized change was persisted")
	}
}
