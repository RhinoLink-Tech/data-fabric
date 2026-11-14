package onboardingruntime

import (
	"context"
	"errors"
	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/mcp"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/gorm"
)

// updateCoordinator is dormant until a complete, independently reviewed
// singleton lease, browser route and deployment configuration are available.
type updateCoordinator struct {
	g    grants
	flow *onboarding.Store
}

var _ mcp.SourceUpdateCoordinator = (*updateCoordinator)(nil)

func (c *updateCoordinator) validate(ctx context.Context, p onboarding.Principal, caller *security.Caller, id string) error {
	if c == nil || c.flow == nil || caller == nil || caller.UserID != p.Subject || caller.TenantID != p.Tenant {
		return onboarding.ErrUnavailable
	}
	scoped := false
	for _, s := range caller.Scopes {
		if s == "data-fabric.sources.manage" {
			scoped = true
		}
	}
	if !scoped {
		return onboarding.ErrUnavailable
	}
	user, tenant, ok, e := c.g.Resolve(ctx, p.Issuer, p.Subject)
	if e != nil || !ok || user != p.Subject || tenant != p.Tenant {
		return onboarding.ErrUnavailable
	}
	return c.g.AuthorizeOnboarding(ctx, p, caller, id)
}
func (c *updateCoordinator) BeginUpdate(ctx context.Context, p onboarding.Principal, caller *security.Caller, id string) (string, error) {
	if e := c.validate(ctx, p, caller, id); e != nil {
		return "", e
	}
	return c.flow.BeginBoundedUpdate(ctx, p, id, 4, 64, 4096)
}
func (c *updateCoordinator) UpdateStatus(ctx context.Context, p onboarding.Principal, caller *security.Caller, id, target string) (string, error) {
	if e := c.validate(ctx, p, caller, target); e != nil {
		return "", e
	}
	var state string
	err := c.g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := onboarding.LockAndVerifyGrant(ctx, tx, p.Issuer, p.Subject, caller.UserID, caller.TenantID); e != nil {
			return e
		}
		repo, e := repository.NewDataSourceRepository(tx, c.g.vault)
		if e != nil {
			return e
		}
		source, e := repo.GetByID(ctx, target)
		if e != nil || source.OwnerID != caller.UserID || source.TenantID != caller.TenantID {
			return errors.New("source unavailable")
		}
		var actual string
		state, actual, e = c.flow.InspectUpdateTx(ctx, tx, id, p, target)
		if e != nil || (state == "completed" && actual != target) {
			return onboarding.ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return "", onboarding.ErrUnavailable
	}
	return state, nil
}

type auditedUpdate struct {
	delegate mcp.SourceUpdateCoordinator
	store    *auditlog.Store
}

func (a auditedUpdate) record(ctx context.Context, c *security.Caller, operation, target string, ok bool) error {
	actor, tenant := "", ""
	if c != nil {
		actor, tenant = c.UserID, c.TenantID
	}
	decision, outcome := auditlog.DecisionDeny, "failure"
	if ok {
		decision, outcome = auditlog.DecisionAllow, "success"
	}
	return a.store.Record(ctx, auditlog.Event{ActorID: actor, TenantID: tenant, Transport: "mcp", Operation: operation, DataSourceID: target, Decision: decision, Outcome: outcome})
}
func (a auditedUpdate) BeginUpdate(ctx context.Context, p onboarding.Principal, c *security.Caller, id string) (string, error) {
	v, e := a.delegate.BeginUpdate(ctx, p, c, id)
	if a.record(ctx, c, "onboarding.update.begin", id, e == nil) != nil {
		return "", onboarding.ErrUnavailable
	}
	return v, e
}
func (a auditedUpdate) UpdateStatus(ctx context.Context, p onboarding.Principal, c *security.Caller, id, target string) (string, error) {
	v, e := a.delegate.UpdateStatus(ctx, p, c, id, target)
	if a.record(ctx, c, "onboarding.update.status", target, e == nil) != nil {
		return "", onboarding.ErrUnavailable
	}
	return v, e
}
