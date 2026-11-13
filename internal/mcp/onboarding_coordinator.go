package mcp

import (
	"context"
	"errors"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
)

// OnboardingGrantAuthorizer must independently consult current server-side
// identity mapping and authorization, and for nonempty sourceID must verify
// that source exists and the actor can manage it under strict tenant matching.
// A previously issued access-token scope or completed flow alone is insufficient.
type OnboardingGrantAuthorizer interface {
	Resolve(context.Context, string, string) (userID, tenant string, allowed bool, err error)
	AuthorizeOnboarding(context.Context, onboarding.Principal, *security.Caller, string) error
	AuthorizedStatus(context.Context, onboarding.Principal, *security.Caller, string, *onboarding.Store) (string, string, error)
}
type boundedOnboardingCoordinator struct {
	store                           *onboarding.Store
	grants                          OnboardingGrantAuthorizer
	perPrincipal, perTenant, global int
}

// NewBoundedOnboardingCoordinator requires durable shared MySQL quota and
// completion schemas to be provisioned before use. Caller must also schedule
// bounded cleanup and enable browser endpoints only after independent review.
func NewBoundedOnboardingCoordinator(store *onboarding.Store, grants OnboardingGrantAuthorizer, perPrincipal, perTenant, global int) (OnboardingCoordinator, error) {
	if store == nil || grants == nil || perPrincipal < 1 || perTenant < perPrincipal || global < perTenant || global > 1000000 {
		return nil, errors.New("onboarding requires an authorized bounded coordinator")
	}
	return &boundedOnboardingCoordinator{store: store, grants: grants, perPrincipal: perPrincipal, perTenant: perTenant, global: global}, nil
}
func (c *boundedOnboardingCoordinator) Begin(ctx context.Context, actor onboarding.Principal, caller *security.Caller) (string, error) {
	// Transport validated scope; current server-side grant must still be checked.
	if caller == nil || caller.UserID != actor.Subject || caller.TenantID != actor.Tenant || !callerHasOnboardingScope(caller) {
		return "", onboarding.ErrUnavailable
	}
	canonical, e := c.canonicalCaller(ctx, actor, caller)
	if e != nil {
		return "", e
	}
	if e := c.grants.AuthorizeOnboarding(ctx, actor, canonical, ""); e != nil {
		return "", e
	}
	return c.store.BeginBounded(ctx, actor, c.perPrincipal, c.perTenant, c.global)
}
func (c *boundedOnboardingCoordinator) Status(ctx context.Context, actor onboarding.Principal, caller *security.Caller, id string) (string, string, error) {
	if caller == nil || caller.UserID != actor.Subject || caller.TenantID != actor.Tenant || !callerHasOnboardingScope(caller) {
		return "", "", onboarding.ErrUnavailable
	}
	canonical, e := c.canonicalCaller(ctx, actor, caller)
	if e != nil {
		return "", "", e
	}
	if e := c.grants.AuthorizeOnboarding(ctx, actor, canonical, ""); e != nil {
		return "", "", e
	}
	return c.grants.AuthorizedStatus(ctx, actor, canonical, id, c.store)
}

// canonicalCaller resolves both issuer and subject server-side on every request.
func (c *boundedOnboardingCoordinator) canonicalCaller(ctx context.Context, actor onboarding.Principal, caller *security.Caller) (*security.Caller, error) {
	user, tenant, allowed, e := c.grants.Resolve(ctx, actor.Issuer, actor.Subject)
	if e != nil || !allowed || user == "" || user != actor.Subject || tenant == "" || tenant != actor.Tenant {
		return nil, onboarding.ErrUnavailable
	}
	return &security.Caller{UserID: user, TenantID: tenant, Scopes: append([]string(nil), caller.Scopes...)}, nil
}

func callerHasOnboardingScope(caller *security.Caller) bool {
	if caller == nil {
		return false
	}
	for _, scope := range caller.Scopes {
		if scope == sourceManagementScope {
			return true
		}
	}
	return false
}
