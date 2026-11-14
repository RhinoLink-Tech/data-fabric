package onboardinghttp

import (
	"context"
	"errors"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/browserauth"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/gorm"
	"net/http"
)

// BrowserAuth connects the handler to the server-side browser session. The
// configured origin must be the actual public HTTPS origin, never request data.
type BrowserAuth struct {
	Handler *browserauth.Handler
	Origin  string
}

func (a BrowserAuth) Current(r *http.Request) (Actor, error) {
	if a.Handler == nil {
		return Actor{}, errors.New("session unavailable")
	}
	id, user, tenant, csrf, err := a.Handler.Principal(r)
	return Actor{Issuer: id.Issuer, Subject: id.Subject, Tenant: tenant, UserID: user, CSRF: csrf}, err
}
func (a BrowserAuth) CheckMutation(r *http.Request, csrf string) bool {
	return browserauth.CheckMutation(r, a.Origin, csrf)
}

func (a BrowserAuth) LockAuthorization(ctx context.Context, tx *gorm.DB, actor Actor) error {
	return onboarding.LockAndVerifyGrant(ctx, tx, actor.Issuer, actor.Subject, actor.UserID, actor.Tenant)
}
