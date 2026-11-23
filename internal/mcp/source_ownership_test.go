package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

func TestListDataSourcesOwnershipIsInformationalAndExact(t *testing.T) {
	sources := []*model.DataSource{
		{ID: "own", OwnerID: "alice", TenantID: "team"},
		{ID: "shared", OwnerID: "bob", TenantID: "team"},
		{ID: "other-tenant", OwnerID: "alice", TenantID: "other"},
		{ID: "case", OwnerID: "ALICE", TenantID: "team"},
		{ID: "tenantless", OwnerID: "alice", TenantID: ""},
		{ID: "legacy", OwnerID: "", TenantID: ""},
	}
	srv := NewMCPServer(nil, listingRepoStub{sources: sources}, &stubTableDescriber{})

	for _, tc := range []struct {
		name   string
		caller *security.Caller
		want   map[string]*bool
	}{
		{name: "identified", caller: &security.Caller{UserID: "alice", TenantID: "team"}, want: map[string]*bool{
			"own": boolPointer(true), "shared": boolPointer(false), "other-tenant": boolPointer(false), "case": boolPointer(false), "tenantless": boolPointer(false), "legacy": boolPointer(false),
		}},
		{name: "tenantless", caller: &security.Caller{UserID: "alice"}, want: map[string]*bool{
			"own": boolPointer(false), "shared": boolPointer(false), "other-tenant": boolPointer(false), "case": boolPointer(false), "tenantless": boolPointer(true), "legacy": boolPointer(false),
		}},
		{name: "administrator", caller: &security.Caller{UserID: "alice", TenantID: "team", Roles: []string{security.AdminRole}}, want: map[string]*bool{
			"own": boolPointer(true), "shared": boolPointer(false), "other-tenant": boolPointer(false), "case": boolPointer(false), "tenantless": boolPointer(false), "legacy": boolPointer(false),
		}},
		{name: "no caller"},
		{name: "empty identity", caller: &security.Caller{UserID: "   ", TenantID: "team"}},
		{name: "local system", caller: security.SystemCaller()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.caller != nil {
				ctx = context.WithValue(ctx, callerContextKey{}, tc.caller)
			}
			result, err := srv.handleListDataSources(ctx, callToolRequest("list_data_sources", nil))
			if err != nil || result == nil || result.IsError {
				t.Fatalf("list sources: result=%+v err=%v", result, err)
			}
			text := resultText(t, result)
			if strings.Contains(text, "owner_id") || strings.Contains(text, "tenant_id") {
				t.Fatalf("list exposes an owner or tenant identifier: %s", text)
			}
			var payload struct {
				DataSources []struct {
					ID            string `json:"id"`
					OwnedByCaller *bool  `json:"owned_by_caller"`
				} `json:"data_sources"`
			}
			if err := json.Unmarshal([]byte(text), &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.DataSources) != len(sources) {
				t.Fatalf("got %d sources, want %d", len(payload.DataSources), len(sources))
			}
			for _, source := range payload.DataSources {
				want := tc.want[source.ID]
				if (source.OwnedByCaller == nil) != (want == nil) || (want != nil && *source.OwnedByCaller != *want) {
					t.Errorf("source %s ownership = %v, want %v", source.ID, source.OwnedByCaller, want)
				}
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }
