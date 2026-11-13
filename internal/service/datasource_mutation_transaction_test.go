package service

import (
	"context"
	"errors"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"testing"
)

type beforeLockedMutation struct {
	repository.DataSourceRepository
	before func()
}

func (r *beforeLockedMutation) MutateSourceLocked(ctx context.Context, id string, status *model.DataSourceStatus, auth func(*model.DataSource) error) error {
	r.before()
	return r.DataSourceRepository.(repository.LockedSourceMutator).MutateSourceLocked(ctx, id, status, auth)
}
func TestMutationAuthorizesOwnerAtWriteTime(t *testing.T) {
	for _, action := range []string{"activate", "deactivate", "delete"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			repo, source := transactionFixture(t)
			wrapper := &beforeLockedMutation{DataSourceRepository: repo, before: func() {
				ds, err := repo.GetByID(ctx, source.ID)
				if err != nil {
					t.Fatal(err)
				}
				ds.OwnerID = "bob"
				if err = repo.Update(ctx, ds); err != nil {
					t.Fatal(err)
				}
			}}
			svc := NewDataSourceService(wrapper, nil, &datasourceConfigValidatorStub{supported: true})
			caller := &security.Caller{UserID: "alice", TenantID: "a"}
			var err error
			switch action {
			case "activate":
				err = svc.ActivateDataSource(ctx, caller, source.ID)
			case "deactivate":
				err = svc.DeactivateDataSource(ctx, caller, source.ID)
			case "delete":
				err = svc.DeleteDataSource(ctx, caller, source.ID)
			}
			if !errors.Is(err, security.ErrDataSourceAccessDenied) {
				t.Fatalf("expected denial, got %v", err)
			}
			got, err := repo.GetByID(ctx, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.OwnerID != "bob" || got.Status != model.DataSourceStatusActive {
				t.Fatal("denied operation changed source")
			}
		})
	}
}
func TestLockedMutationHandlesSameStatusAndMissingRow(t *testing.T) {
	ctx := context.Background()
	repo, source := transactionFixture(t)
	svc := NewDataSourceService(repo, nil, &datasourceConfigValidatorStub{supported: true})
	owner := &security.Caller{UserID: "alice", TenantID: "a"}
	if err := svc.ActivateDataSource(ctx, owner, source.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteDataSource(ctx, owner, source.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteDataSource(ctx, owner, source.ID); !errors.Is(err, repository.ErrDataSourceNotFound) {
		t.Fatalf("expected missing: %v", err)
	}
}
