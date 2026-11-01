package service

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

const defaultQuerySessionTTL = 30 * time.Minute

// newContinuationToken creates an opaque random UUIDv4. Continuation tokens
// are credentials: a successful fetch rotates the token, making earlier
// continuation links unusable.
func newContinuationToken() string {
	return uuid.New().String()
}

// sessionContinuationLock serializes state changes for one query session while
// allowing a waiting request to stop when its context is cancelled.
type sessionContinuationLock struct {
	once sync.Once
	gate chan struct{}
}

func (lock *sessionContinuationLock) Lock(ctx context.Context) error {
	lock.once.Do(func() {
		lock.gate = make(chan struct{}, 1)
	})
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case lock.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (lock *sessionContinuationLock) Unlock() {
	lock.once.Do(func() {
		lock.gate = make(chan struct{}, 1)
	})
	<-lock.gate
}

func validateContinuationToken(sessionSlug, sessionToken, slug, token string) error {
	if sessionSlug != slug || sessionToken != token {
		return ErrInvalidQuerySession
	}
	return nil
}
