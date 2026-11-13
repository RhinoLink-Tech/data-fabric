package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestDataSourceRenameCollisionRespectsTenant(t *testing.T) {
	ctx := context.Background()
	_, repo := testRepository(t, vaultForTest(t))
	source := func(name, tenant string) *model.DataSource {
		return &model.DataSource{Name: name, TenantID: tenant, OwnerID: "owner", Type: model.DatabaseTypeMySQL,
			Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{Host: "example", Port: 3306, Database: "sample", Username: "reader"}}
	}
	existing := source("orders", "tenant-a")
	renamed := source("reports", "tenant-a")
	otherTenant := source("reports", "tenant-b")
	for _, ds := range []*model.DataSource{existing, renamed, otherTenant} {
		if err := repo.Create(ctx, ds); err != nil {
			t.Fatal(err)
		}
	}
	renamed.Name = "orders"
	if err := repo.Update(ctx, renamed); !errors.Is(err, ErrDataSourceExists) {
		t.Fatalf("same-tenant rename error = %v, want ErrDataSourceExists", err)
	}
	stored, err := repo.GetByID(ctx, renamed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "reports" {
		t.Fatalf("failed rename changed stored name to %q", stored.Name)
	}
	otherTenant.Name = "orders"
	if err := repo.Update(ctx, otherTenant); err != nil {
		t.Fatalf("cross-tenant rename failed: %v", err)
	}
}
