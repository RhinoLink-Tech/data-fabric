package mcp

import (
	"context"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"github.com/google/uuid"
	"strings"
	"testing"
)

type fakeUpdate struct{ count int }

func (f *fakeUpdate) BeginUpdate(context.Context, onboarding.Principal, *security.Caller, string) (string, error) {
	f.count++
	return strings.Repeat("A", 43), nil
}
func (f *fakeUpdate) UpdateStatus(context.Context, onboarding.Principal, *security.Caller, string, string) (string, error) {
	f.count++
	return "completed", nil
}
func TestUpdateToolsOptInAndRejectSecrets(t *testing.T) {
	srv := NewMCPServer(nil, nil, nil)
	if _, ok := srv.server.ListTools()["begin_data_source_update"]; ok {
		t.Fatal("update exposed by default")
	}
	f := &fakeUpdate{}
	if srv.SetSourceUpdateCoordinator(f) == nil {
		t.Fatal("must require configured onboarding")
	}
	if err := srv.SetOnboardingCoordinator(&fakeOnboarding{}, "https://example.test"); err != nil {
		t.Fatal(err)
	}
	if err := srv.SetSourceUpdateCoordinator(f); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	ctx := onboardingContext([]string{sourceManagementScope}, verifiedOnboardingIdentity{Issuer: "issuer", Subject: "alice", Tenant: "tenant"})
	for _, tc := range []struct {
		ctx  context.Context
		args map[string]any
	}{
		{context.Background(), map[string]any{"datasource_id": id}},
		{ctx, map[string]any{"datasource_id": id, "password": "secret"}},
		{ctx, map[string]any{"datasource_id": "not-a-uuid"}},
	} {
		r, e := srv.handleBeginSourceUpdate(tc.ctx, callToolRequest("begin_data_source_update", tc.args))
		if e != nil || !r.IsError {
			t.Fatal("unauthorized update accepted")
		}
	}
	if f.count != 0 {
		t.Fatal("backend invoked for denied update")
	}
	r, e := srv.handleBeginSourceUpdate(ctx, callToolRequest("begin_data_source_update", map[string]any{"datasource_id": id}))
	if e != nil || r.IsError || !strings.Contains(resultText(t, r), "/update/"+id) {
		t.Fatal("valid flow rejected")
	}
	r, e = srv.handleSourceUpdateStatus(ctx, callToolRequest("get_data_source_update_status", map[string]any{"flow_id": strings.Repeat("A", 43), "datasource_id": id}))
	if e != nil || r.IsError || !strings.Contains(resultText(t, r), "completed") {
		t.Fatal("status rejected")
	}
}
