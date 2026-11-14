package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
)

type fakeOnboarding struct {
	count         int
	actor         onboarding.Principal
	state, source string
	err           error
}

func (f *fakeOnboarding) Begin(_ context.Context, p onboarding.Principal, c *security.Caller) (string, error) {
	f.count++
	f.actor = p
	return strings.Repeat("A", 43), f.err
}
func (f *fakeOnboarding) Status(_ context.Context, p onboarding.Principal, c *security.Caller, id string) (string, string, error) {
	f.count++
	f.actor = p
	return f.state, f.source, f.err
}
func onboardingContext(scopes []string, identity verifiedOnboardingIdentity) context.Context {
	ctx := context.WithValue(context.Background(), callerContextKey{}, &security.Caller{UserID: "alice", TenantID: "tenant", Scopes: scopes})
	return context.WithValue(ctx, verifiedIdentityContextKey{}, identity)
}
func TestOnboardingIsOptInAndStrictlyBound(t *testing.T) {
	srv := NewMCPServer(nil, nil, nil)
	if _, ok := srv.server.ListTools()["begin_data_source_onboarding"]; ok {
		t.Fatal("onboarding must be opt-in")
	}
	fake := &fakeOnboarding{}
	for _, base := range []string{"http://example.test", "https://example.test/path", "https://user@example.test", "https://example.test?x=y", "https://example.test/#x"} {
		if srv.SetOnboardingCoordinator(fake, base) == nil {
			t.Fatalf("accepted unsafe origin %q", base)
		}
	}
	if err := srv.SetOnboardingCoordinator(fake, "https://example.test"); err != nil {
		t.Fatal(err)
	}
	valid := verifiedOnboardingIdentity{Issuer: "https://issuer.example", Subject: "alice", Tenant: "tenant"}
	cases := []struct {
		ctx  context.Context
		args map[string]any
	}{
		{context.Background(), nil}, {onboardingContext(nil, valid), nil},
		{onboardingContext([]string{sourceManagementScope}, verifiedOnboardingIdentity{Issuer: "https://issuer.example", Subject: "alice"}), nil},
		{onboardingContext([]string{sourceManagementScope}, verifiedOnboardingIdentity{Issuer: "https://issuer.example", Subject: "other", Tenant: "tenant"}), nil},
		{onboardingContext([]string{sourceManagementScope}, valid), map[string]any{"password": "must-not-be-accepted"}},
	}
	for _, tc := range cases {
		r, e := srv.handleBeginOnboarding(tc.ctx, callToolRequest("begin_data_source_onboarding", tc.args))
		if e != nil || !r.IsError {
			t.Fatal("unauthorized onboarding accepted")
		}
	}
	if fake.count != 0 {
		t.Fatal("backend called for denied requests")
	}
	r, e := srv.handleBeginOnboarding(onboardingContext([]string{sourceManagementScope}, valid), callToolRequest("begin_data_source_onboarding", nil))
	if e != nil || r.IsError {
		t.Fatal("authorized begin rejected")
	}
	if fake.actor.Issuer != valid.Issuer || fake.actor.Subject != valid.Subject || fake.actor.Tenant != valid.Tenant {
		t.Fatal("incorrect principal")
	}
	if !strings.Contains(resultText(t, r), "https://example.test/onboarding/flows/") {
		t.Fatal("missing configured HTTPS URL")
	}
}
func TestOnboardingStatusFailsClosed(t *testing.T) {
	srv := NewMCPServer(nil, nil, nil)
	f := &fakeOnboarding{state: "completed", source: "not-a-uuid"}
	if e := srv.SetOnboardingCoordinator(f, "https://example.test"); e != nil {
		t.Fatal(e)
	}
	ctx := onboardingContext([]string{sourceManagementScope}, verifiedOnboardingIdentity{Issuer: "https://issuer.example", Subject: "alice", Tenant: "tenant"})
	id := strings.Repeat("A", 43)
	for _, args := range []map[string]any{{"flow_id": "bad"}, {"flow_id": id, "password": "secret"}} {
		r, _ := srv.handleOnboardingStatus(ctx, callToolRequest("get_data_source_onboarding_status", args))
		if !r.IsError {
			t.Fatal("invalid arguments accepted")
		}
	}
	r, _ := srv.handleOnboardingStatus(ctx, callToolRequest("get_data_source_onboarding_status", map[string]any{"flow_id": id}))
	if !r.IsError {
		t.Fatal("invalid source returned")
	}
	f.source = "34ee7a59-e839-4de8-b278-afc920c54136"
	f.err = errors.New("sensitive backend error")
	r, _ = srv.handleOnboardingStatus(ctx, callToolRequest("get_data_source_onboarding_status", map[string]any{"flow_id": id}))
	if !r.IsError || strings.Contains(resultText(t, r), "sensitive") {
		t.Fatal("backend error leaked")
	}
	f.err = nil
	r, _ = srv.handleOnboardingStatus(ctx, callToolRequest("get_data_source_onboarding_status", map[string]any{"flow_id": id}))
	if r.IsError || !strings.Contains(resultText(t, r), f.source) {
		t.Fatal("valid result missing")
	}
}
