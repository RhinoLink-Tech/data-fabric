package mcp

import (
	"context"
	"errors"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"testing"
	"time"
)

type testGrant struct {
	deny   bool
	source string
}

func (g *testGrant) Resolve(_ context.Context, issuer, subject string) (string, string, bool, error) {
	if g.deny {
		return "", "", false, nil
	}
	return "alice", "tenant", true, nil
}
func (g *testGrant) AuthorizeOnboarding(_ context.Context, _ onboarding.Principal, _ *security.Caller, id string) error {
	if g.deny || (id != "" && id != g.source) {
		return errors.New("not allowed")
	}
	return nil
}
func (g *testGrant) AuthorizedStatus(ctx context.Context, p onboarding.Principal, c *security.Caller, id string, s *onboarding.Store) (string, string, error) {
	state, source, err := s.Inspect(ctx, id, p)
	if err != nil {
		return "", "", err
	}
	if err = g.AuthorizeOnboarding(ctx, p, c, source); err != nil {
		return "", "", err
	}
	return state, source, nil
}
func TestCoordinatorChecksRevocationAndSourceAccess(t *testing.T) {
	db, e := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	if e = onboarding.Migrate(db); e != nil {
		t.Fatal(e)
	}
	if e = onboarding.MigrateCompletions(db); e != nil {
		t.Fatal(e)
	}
	if e = onboarding.MigrateQuota(db); e != nil {
		t.Fatal(e)
	}
	store, _ := onboarding.New(db, time.Minute)
	g := &testGrant{source: "34ee7a59-e839-4de8-b278-afc920c54136"}
	co, e := NewBoundedOnboardingCoordinator(store, g, 2, 3, 4)
	if e != nil {
		t.Fatal(e)
	}
	p := onboarding.Principal{Issuer: "https://issuer", Subject: "alice", Tenant: "tenant"}
	c := &security.Caller{UserID: "alice", TenantID: "tenant", Scopes: []string{sourceManagementScope}}
	id, e := co.Begin(context.Background(), p, c)
	if e != nil {
		t.Fatal(e)
	}
	g.deny = true
	if _, _, e = co.Status(context.Background(), p, c, id); e == nil {
		t.Fatal("revoked grant accepted")
	}
	g.deny = false
	_, e = store.ApplyResult(context.Background(), id, p, func(*gorm.DB) (string, error) { return g.source, nil })
	if e != nil {
		t.Fatal(e)
	}
	g.source = ""
	if _, _, e = co.Status(context.Background(), p, c, id); e == nil {
		t.Fatal("missing source authorization accepted")
	}
	g.source = "34ee7a59-e839-4de8-b278-afc920c54136"
	state, source, e := co.Status(context.Background(), p, c, id)
	if e != nil || state != "completed" || source != g.source {
		t.Fatal("authorized result missing")
	}
}
