package service

import (
	"context"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
)

const poolInvalidationTestID = "11111111-1111-1111-1111-111111111111"

// poolInvalidationRepository persists every mutation in memory; the pool
// invalidation behavior under test happens after the repository call.
type poolInvalidationRepository struct {
	repository.DataSourceRepository
	source *model.DataSource
}

func (r *poolInvalidationRepository) GetByID(_ context.Context, _ string) (*model.DataSource, error) {
	return r.source, nil
}

func (r *poolInvalidationRepository) Update(_ context.Context, ds *model.DataSource) error {
	r.source = ds
	return nil
}

func (r *poolInvalidationRepository) Delete(_ context.Context, _ string) error { return nil }

func (r *poolInvalidationRepository) Activate(_ context.Context, _ string) error { return nil }

func (r *poolInvalidationRepository) Deactivate(_ context.Context, _ string) error { return nil }

type recordingInvalidator struct {
	ids  []string
	errs error
}

func (ri *recordingInvalidator) InvalidateDataSource(id string) error {
	ri.ids = append(ri.ids, id)
	return ri.errs
}

func newInvalidationTestService() (*dataSourceService, *recordingInvalidator) {
	invalidator := &recordingInvalidator{}
	repo := &poolInvalidationRepository{source: &model.DataSource{
		ID:     poolInvalidationTestID,
		Name:   "lakehouse",
		Type:   model.DatabaseTypeMySQL,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{Host: "db", Port: 3306, Database: "demo", Username: "u"},
	}}
	svc := NewDataSourceService(repo, invalidator, database.NewHealthChecker(nil)).(*dataSourceService)
	return svc, invalidator
}

// Configuration or status updates, deletion and activation changes must evict
// the source's pooled connections; a name-only update must not (#117).
func TestDataSourceMutationsInvalidatePool(t *testing.T) {
	ctx := context.Background()

	t.Run("config update evicts", func(t *testing.T) {
		svc, inv := newInvalidationTestService()
		if _, err := svc.UpdateDataSource(ctx, nil, poolInvalidationTestID, &UpdateDataSourceRequest{
			Config: &model.DataSourceConfig{Host: "new-host", Port: 3306, Database: "demo", Username: "u"},
		}); err != nil {
			t.Fatalf("UpdateDataSource failed: %v", err)
		}
		if len(inv.ids) != 1 || inv.ids[0] != poolInvalidationTestID {
			t.Errorf("invalidator calls = %v, want one call for %s", inv.ids, poolInvalidationTestID)
		}
	})

	t.Run("status update evicts", func(t *testing.T) {
		svc, inv := newInvalidationTestService()
		inactive := model.DataSourceStatusInactive
		if _, err := svc.UpdateDataSource(ctx, nil, poolInvalidationTestID, &UpdateDataSourceRequest{Status: &inactive}); err != nil {
			t.Fatalf("UpdateDataSource failed: %v", err)
		}
		if len(inv.ids) != 1 {
			t.Errorf("invalidator calls = %v, want one call", inv.ids)
		}
	})

	t.Run("name-only update keeps the pool", func(t *testing.T) {
		svc, inv := newInvalidationTestService()
		newName := "renamed"
		if _, err := svc.UpdateDataSource(ctx, nil, poolInvalidationTestID, &UpdateDataSourceRequest{Name: &newName}); err != nil {
			t.Fatalf("UpdateDataSource failed: %v", err)
		}
		if len(inv.ids) != 0 {
			t.Errorf("invalidator calls = %v, want none for a name-only update", inv.ids)
		}
	})

	t.Run("delete evicts", func(t *testing.T) {
		svc, inv := newInvalidationTestService()
		if err := svc.DeleteDataSource(ctx, nil, poolInvalidationTestID); err != nil {
			t.Fatalf("DeleteDataSource failed: %v", err)
		}
		if len(inv.ids) != 1 {
			t.Errorf("invalidator calls = %v, want one call", inv.ids)
		}
	})

	t.Run("deactivate evicts", func(t *testing.T) {
		svc, inv := newInvalidationTestService()
		if err := svc.DeactivateDataSource(ctx, nil, poolInvalidationTestID); err != nil {
			t.Fatalf("DeactivateDataSource failed: %v", err)
		}
		if len(inv.ids) != 1 {
			t.Errorf("invalidator calls = %v, want one call", inv.ids)
		}
	})

	t.Run("activate evicts", func(t *testing.T) {
		svc, inv := newInvalidationTestService()
		if err := svc.ActivateDataSource(ctx, nil, poolInvalidationTestID); err != nil {
			t.Fatalf("ActivateDataSource failed: %v", err)
		}
		if len(inv.ids) != 1 {
			t.Errorf("invalidator calls = %v, want one call", inv.ids)
		}
	})
}
