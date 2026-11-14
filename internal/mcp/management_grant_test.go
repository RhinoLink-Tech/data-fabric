package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

type testManagementGrant struct {
	enabled    bool
	fail       bool
	auditFail  bool
	auditCount int
}

func (g *testManagementGrant) WithManagementGrant(ctx context.Context, issuer, subject, owner, tenant string, run func(context.Context) error) error {
	if g.fail || !g.enabled || issuer != "https://issuer.example" || subject != "alice" || owner != "alice" || tenant != "tenant" {
		return errors.New("unavailable")
	}
	return run(ctx)
}
func (g *testManagementGrant) RecordManagementSuccess(context.Context, string, string, string, string) error {
	g.auditCount++
	if g.auditFail {
		return errors.New("audit failed")
	}
	return nil
}
func TestManagementGrantCoversRegisteredToolsAndReplicas(t *testing.T) {
	grant := &testManagementGrant{enabled: true}
	var servers []*MCPServer
	for i := 0; i < 2; i++ {
		stub := &deletionStub{managementStub: &managementStub{source: &model.DataSource{ID: "id", OwnerID: "alice", TenantID: "tenant", Type: model.DatabaseTypePostgreSQL}}}
		srv := NewMCPServer(nil, nil, nil)
		srv.SetDataSourceManagementService(stub)
		srv.SetSourceConnectivityTester(&probeStub{})
		if err := srv.SetSourceManagementGrant(grant); err != nil {
			t.Fatal(err)
		}
		servers = append(servers, srv)
	}
	ctx := context.WithValue(context.Background(), callerContextKey{}, &security.Caller{UserID: "alice", TenantID: "tenant", Scopes: []string{sourceManagementScope}})
	ctx = context.WithValue(ctx, verifiedIdentityContextKey{}, verifiedOnboardingIdentity{Issuer: "https://issuer.example", Subject: "alice", Tenant: "tenant"})
	calls := map[string]map[string]any{
		"rename_data_source":          {"datasource_id": "id", "name": "renamed"},
		"set_data_source_status":      {"datasource_id": "id", "status": "inactive"},
		"delete_data_source":          {"datasource_id": "id", "confirmation_name": "orders"},
		"test_data_source_connection": {"datasource_id": "id"},
	}
	for _, enabled := range []bool{true, false, true} {
		grant.enabled = enabled
		for _, srv := range servers {
			for name, args := range calls {
				result, err := srv.server.ListTools()[name].Handler(ctx, callToolRequest(name, args))
				if err != nil || result == nil || result.IsError == enabled {
					t.Fatalf("%s enabled=%v result=%+v err=%v", name, enabled, result, err)
				}
			}
		}
	}
	grant.fail = true
	result, err := servers[0].server.ListTools()["rename_data_source"].Handler(ctx, callToolRequest("rename_data_source", calls["rename_data_source"]))
	if err != nil || !result.IsError {
		t.Fatal("grant store errors must fail closed")
	}
	grant.fail = false
	for _, bad := range []context.Context{
		context.WithValue(context.Background(), callerContextKey{}, &security.Caller{UserID: "alice", TenantID: "tenant", Scopes: []string{sourceManagementScope}}),
		context.WithValue(ctx, verifiedIdentityContextKey{}, verifiedOnboardingIdentity{Issuer: "https://issuer.example", Subject: "other", Tenant: "tenant"}),
		context.WithValue(ctx, callerContextKey{}, &security.Caller{UserID: "alice", TenantID: "tenant"}),
	} {
		result, err = servers[0].server.ListTools()["rename_data_source"].Handler(bad, callToolRequest("rename_data_source", calls["rename_data_source"]))
		if err != nil || !result.IsError {
			t.Fatal("missing or mismatched identity/scope must be denied")
		}
	}
}

func TestManagementAuditFailureDeniesSuccess(t *testing.T) {
	grant := &testManagementGrant{enabled: true, auditFail: true}
	srv := NewMCPServer(nil, nil, nil)
	srv.SetDataSourceManagementService(&managementStub{source: &model.DataSource{ID: "id", OwnerID: "alice", TenantID: "tenant"}})
	if err := srv.SetSourceManagementGrant(grant); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), callerContextKey{}, &security.Caller{UserID: "alice", TenantID: "tenant", Scopes: []string{sourceManagementScope}})
	ctx = context.WithValue(ctx, verifiedIdentityContextKey{}, verifiedOnboardingIdentity{Issuer: "https://issuer.example", Subject: "alice", Tenant: "tenant"})
	result, err := srv.server.ListTools()["rename_data_source"].Handler(ctx, callToolRequest("rename_data_source", map[string]any{"datasource_id": "id", "name": "renamed"}))
	if err != nil || result == nil || !result.IsError || grant.auditCount != 1 {
		t.Fatal("audit failure did not reject operation")
	}
}
