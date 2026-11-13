package repository

import (
	"context"
	"errors"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"testing"
)

func TestScopedDeleteMatchesExactOwnerTenantAndConfirmedName(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepository(t, vaultForTest(t))
	ds := &model.DataSource{Name: "orders", OwnerID: "alice", TenantID: "tenant", Type: model.DatabaseTypeMySQL, Config: model.DataSourceConfig{Host: "db", Port: 3306}}
	if err := repo.Create(ctx, ds); err != nil {
		t.Fatal(err)
	}
	m := repo.(AtomicSourceManagement)
	for _, attempt := range []struct {
		scope SourceWriteScope
		name  string
	}{
		{SourceWriteScope{OwnerID: "ALICE", TenantID: "tenant"}, "orders"},
		{SourceWriteScope{OwnerID: "bob", TenantID: "tenant", Admin: true}, "orders"},
		{SourceWriteScope{OwnerID: "alice", TenantID: "TENANT"}, "orders"},
		{SourceWriteScope{OwnerID: "alice", TenantID: "tenant"}, "ORDERS"},
		{SourceWriteScope{OwnerID: "alice", TenantID: "tenant"}, "orders "},
	} {
		if err := m.DeleteSourceScoped(ctx, attempt.scope, ds.ID, attempt.name); !errors.Is(err, ErrDataSourceNotFound) {
			t.Fatalf("unauthorized delete: %v", err)
		}
	}
	scope := SourceWriteScope{OwnerID: "alice", TenantID: "tenant"}
	if _, err := m.RenameSourceScoped(ctx, scope, ds.ID, "new-orders"); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteSourceScoped(ctx, scope, ds.ID, "orders"); !errors.Is(err, ErrDataSourceNotFound) {
		t.Fatal("stale confirmation deleted renamed source")
	}
	if _, err := repo.GetByID(ctx, ds.ID); err != nil {
		t.Fatal("source missing after rejected delete")
	}
	if err := m.DeleteSourceScoped(ctx, scope, ds.ID, "new-orders"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByID(ctx, ds.ID); !errors.Is(err, ErrDataSourceNotFound) {
		t.Fatalf("source survived delete: %v", err)
	}
}
