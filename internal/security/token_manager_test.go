package security

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryTokenStore struct {
	mu       sync.Mutex
	token    string
	expires  time.Time
	casError error
	onCAS    func()
}

func (s *memoryTokenStore) LoadAccessToken(context.Context, string) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token, s.expires, nil
}

func (s *memoryTokenStore) CompareAndSwapAccessToken(_ context.Context, _, expected, replacement string, expiresAt time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.casError != nil {
		return false, s.casError
	}
	if s.onCAS != nil {
		s.onCAS()
		s.onCAS = nil
	}
	if s.token != expected {
		return false, nil
	}
	s.token, s.expires = replacement, expiresAt
	return true, nil
}

func TestTokenManagerPersistsProviderTokenBeforePublishing(t *testing.T) {
	ctx := context.Background()
	oldExpiry := time.Now().Add(time.Hour)
	store := &memoryTokenStore{token: "old-token", expires: oldExpiry}
	manager := NewTokenManager(nil)
	manager.SetStore(store)
	if err := manager.RegisterToken("source-1", func(context.Context) (string, time.Time, error) {
		return "replacement-token", time.Now().Add(2 * time.Hour), nil
	}); err != nil {
		t.Fatal(err)
	}

	token, expiry, err := manager.RotateTokenWithResult(ctx, "source-1")
	if err != nil {
		t.Fatalf("RotateTokenWithResult() error = %v", err)
	}
	if token != "replacement-token" || !expiry.After(oldExpiry) {
		t.Fatalf("rotated token = %q expiring %v", token, expiry)
	}
	if got, err := manager.GetToken(ctx, "source-1"); err != nil || got != token {
		t.Fatalf("GetToken() = %q, %v; want replacement token", got, err)
	}
	persisted, persistedExpiry, err := store.LoadAccessToken(ctx, "source-1")
	if err != nil || persisted != token || !persistedExpiry.Equal(expiry) {
		t.Fatalf("persisted token = %q, %v, %v; want atomic replacement", persisted, persistedExpiry, err)
	}
}

func TestTokenManagerKeepsPreviousTokenWhenProviderFails(t *testing.T) {
	ctx := context.Background()
	expiry := time.Now().Add(time.Hour)
	store := &memoryTokenStore{token: "old-token", expires: expiry}
	manager := NewTokenManager(nil)
	manager.SetStore(store)
	if err := manager.RegisterToken("source-1", func(context.Context) (string, time.Time, error) {
		return "", time.Time{}, errors.New("provider unavailable")
	}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := manager.RotateTokenWithResult(ctx, "source-1"); err == nil {
		t.Fatal("RotateTokenWithResult() returned nil error")
	}
	if got, err := manager.GetToken(ctx, "source-1"); err != nil || got != "old-token" {
		t.Fatalf("GetToken() = %q, %v; want old token after refresh failure", got, err)
	}
	persisted, persistedExpiry, _ := store.LoadAccessToken(ctx, "source-1")
	if persisted != "old-token" || !persistedExpiry.Equal(expiry) {
		t.Fatalf("failed provider changed persisted token: %q %v", persisted, persistedExpiry)
	}
}

func TestTokenManagerKeepsPreviousTokenWhenPersistenceFails(t *testing.T) {
	ctx := context.Background()
	expiry := time.Now().Add(time.Hour)
	store := &memoryTokenStore{
		token:    "old-token",
		expires:  expiry,
		casError: errors.New("database unavailable"),
	}
	manager := NewTokenManager(nil)
	manager.SetStore(store)
	if err := manager.RegisterToken("source-1", func(context.Context) (string, time.Time, error) {
		return "replacement-token", time.Now().Add(2 * time.Hour), nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := manager.RotateTokenWithResult(ctx, "source-1"); err == nil {
		t.Fatal("RotateTokenWithResult() returned nil error")
	}
	if got, err := manager.GetToken(ctx, "source-1"); err != nil || got != "old-token" {
		t.Fatalf("GetToken() = %q, %v; want old token after persistence failure", got, err)
	}
}

func TestTokenManagerAdoptsConcurrentPersistedReplacement(t *testing.T) {
	ctx := context.Background()
	expiry := time.Now().Add(time.Hour)
	store := &memoryTokenStore{token: "old-token", expires: expiry}
	store.onCAS = func() {
		store.token = "concurrent-token"
		store.expires = time.Now().Add(90 * time.Minute)
	}
	manager := NewTokenManager(nil)
	manager.SetStore(store)
	if err := manager.RegisterToken("source-1", func(context.Context) (string, time.Time, error) {
		return "stale-refresh-token", time.Now().Add(2 * time.Hour), nil
	}); err != nil {
		t.Fatal(err)
	}

	token, _, err := manager.RotateTokenWithResult(ctx, "source-1")
	if err != nil {
		t.Fatalf("RotateTokenWithResult() error = %v", err)
	}
	if token != "concurrent-token" {
		t.Fatalf("rotated token = %q, want concurrently persisted token", token)
	}
}

func TestTokenManagerRejectsEmptyOrExpiredProviderTokens(t *testing.T) {
	for _, tt := range []struct {
		name   string
		token  string
		expiry time.Time
	}{
		{name: "empty token", expiry: time.Now().Add(time.Hour)},
		{name: "expired token", token: "expired", expiry: time.Now().Add(-time.Second)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewTokenManager(nil)
			if err := manager.RegisterToken("source-1", func(context.Context) (string, time.Time, error) {
				return tt.token, tt.expiry, nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := manager.RotateTokenWithResult(context.Background(), "source-1"); err == nil {
				t.Fatal("RotateTokenWithResult() returned nil error")
			}
			if _, err := manager.GetToken(context.Background(), "source-1"); !errors.Is(err, ErrTokenUnavailable) {
				t.Fatalf("GetToken() error = %v, want ErrTokenUnavailable", err)
			}
		})
	}
}

func TestTokenManagerReportsUnsupportedRotationAndStopsIdempotently(t *testing.T) {
	manager := NewTokenManager(nil)
	if _, _, err := manager.RotateTokenWithResult(context.Background(), "missing"); !errors.Is(err, ErrTokenRotationUnsupported) {
		t.Fatalf("RotateTokenWithResult() error = %v, want ErrTokenRotationUnsupported", err)
	}
	manager.Start(context.Background())
	manager.Stop()
	manager.Stop()
	manager.Start(context.Background())
}

func TestTokenManagerEnsureTokenRefreshesWithinBuffer(t *testing.T) {
	ctx := context.Background()
	store := &memoryTokenStore{token: "soon-to-expire", expires: time.Now().Add(time.Minute)}
	manager := NewTokenManager(nil)
	manager.SetStore(store)
	if err := manager.RegisterToken("source-1", func(context.Context) (string, time.Time, error) {
		return "fresh-token", time.Now().Add(time.Hour), nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := manager.EnsureToken(ctx, "source-1"); err != nil {
		t.Fatalf("EnsureToken() error = %v", err)
	}
	if token, err := manager.GetToken(ctx, "source-1"); err != nil || token != "fresh-token" {
		t.Fatalf("GetToken() = %q, %v; want fresh-token", token, err)
	}
}

func TestTokenManagerStopCancelsInFlightProvider(t *testing.T) {
	manager := NewTokenManager(nil)
	started := make(chan struct{})
	canceled := make(chan struct{})
	if err := manager.RegisterToken("source-1", func(ctx context.Context) (string, time.Time, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return "", time.Time{}, ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	manager.Start(context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("rotation provider did not start")
	}
	manager.Stop()
	select {
	case <-canceled:
	default:
		t.Fatal("Stop() returned before canceling the in-flight provider")
	}
}
