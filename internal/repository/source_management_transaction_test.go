package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"gorm.io/gorm"
)

func TestScopedSourceMutationsUseBoundTransaction(t *testing.T) {
	for _, op := range []string{"rename", "status", "delete"} {
		t.Run(op, func(t *testing.T) {
			db, repo := testRepository(t, vaultForTest(t))
			source := &model.DataSource{ID: "source-1", Name: "orders", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, OwnerID: "alice", TenantID: "tenant", Config: model.DataSourceConfig{Host: "db", Port: 3306}}
			if err := repo.Create(context.Background(), source); err != nil {
				t.Fatal(err)
			}
			atomic := repo.(AtomicSourceManagement)
			rollback := errors.New("rollback")
			err := db.Transaction(func(tx *gorm.DB) error {
				ctx, _ := WithSourceManagementTransaction(context.Background(), tx)
				scope := SourceWriteScope{OwnerID: "alice", TenantID: "tenant"}
				switch op {
				case "rename":
					_, err := atomic.RenameSourceScoped(ctx, scope, source.ID, "changed")
					if err != nil {
						return err
					}
				case "status":
					if err := atomic.SetSourceStatusScoped(ctx, scope, source.ID, model.DataSourceStatusInactive); err != nil {
						return err
					}
				case "delete":
					if err := atomic.DeleteSourceScoped(ctx, scope, source.ID, "orders"); err != nil {
						return err
					}
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			got, err := repo.GetByID(context.Background(), source.ID)
			if err != nil || got.Name != "orders" || got.Status != model.DataSourceStatusActive {
				t.Fatalf("mutation survived rollback: %+v %v", got, err)
			}
		})
	}
}
