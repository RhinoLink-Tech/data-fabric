package replicarouting

import (
	"context"
	"time"
)

type ownerContextKey struct{}

type transportSessionBinding struct {
	id        string
	expiresAt time.Time
}

// WithOwner makes the current process signer available to the execution mode
// that creates a new query session.
func WithOwner(ctx context.Context, owner *Owner) context.Context {
	return context.WithValue(ctx, ownerContextKey{}, owner)
}

func OwnerFromContext(ctx context.Context) *Owner {
	if ctx == nil {
		return nil
	}
	owner, _ := ctx.Value(ownerContextKey{}).(*Owner)
	return owner
}

func WithTransportSession(ctx context.Context, id string, expiresAt time.Time) context.Context {
	return context.WithValue(ctx, transportSessionBinding{}, transportSessionBinding{id: id, expiresAt: expiresAt})
}

func TransportSession(ctx context.Context) (string, time.Time) {
	if ctx == nil {
		return "", time.Time{}
	}
	binding, _ := ctx.Value(transportSessionBinding{}).(transportSessionBinding)
	return binding.id, binding.expiresAt
}

func QuerySessionExpiry(ctx context.Context, queryExpiry time.Time) time.Time {
	_, transportExpiry := TransportSession(ctx)
	if !transportExpiry.IsZero() && transportExpiry.Before(queryExpiry) {
		return transportExpiry
	}
	return queryExpiry
}
