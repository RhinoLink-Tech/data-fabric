package repository

import (
	"context"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"testing"
)

func TestStrictScopeTenantUsesExactBytes(t *testing.T) {
	_, repo := testRepository(t, vaultForTest(t))
	ctx := context.Background()
	for _, d := range []*model.DataSource{
		{ID: "a", Name: "alpha", Type: model.DatabaseTypeMySQL, OwnerID: "alice", TenantID: "Team", Config: model.DataSourceConfig{Host: "db", Port: 3306}},
		{ID: "b", Name: "beta", Type: model.DatabaseTypeMySQL, OwnerID: "alice", TenantID: "team", Config: model.DataSourceConfig{Host: "db", Port: 3306}},
		{ID: "c", Name: "gamma", Type: model.DatabaseTypeMySQL, OwnerID: "bob", TenantID: "Team", Config: model.DataSourceConfig{Host: "db", Port: 3306}},
	} {
		if e := repo.Create(ctx, d); e != nil {
			t.Fatal(e)
		}
	}
	caller := &security.Caller{UserID: "alice", TenantID: "Team", StrictTenantIsolation: true}
	got, n, e := repo.GetAll(ctx, ScopeForCaller(caller), "", 100, 0)
	if e != nil {
		t.Fatal(e)
	}
	ids := idsOf(got)
	if n != 2 || !ids["a"] || !ids["c"] || ids["b"] {
		t.Fatalf("scope mismatch: %v count %d", ids, n)
	}
	caller.TenantID = ""
	_, n, e = repo.GetAll(ctx, ScopeForCaller(caller), "", 100, 0)
	if e != nil || n != 0 {
		t.Fatalf("tenantless result %d %v", n, e)
	}
}
