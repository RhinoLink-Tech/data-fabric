package onboarding

import "context"

// IdentityResolver is a trusted, revocable server-side mapping. It must map
// issuer and subject to exactly one existing canonical owner and tenant, reject
// ambiguous/colliding mappings, and check current source-management permission.
// It must never derive authorization from a browser-supplied owner or tenant.
type IdentityResolver interface {
	Resolve(ctx context.Context, issuer, subject string) (canonicalUserID, tenant string, allowed bool, err error)
}
