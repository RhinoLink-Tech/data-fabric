package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestContinuationTokensAreRandomUUIDv4Values(t *testing.T) {
	seen := make(map[string]struct{}, 256)
	for i := 0; i < 256; i++ {
		token := newContinuationToken()
		parsed, err := uuid.Parse(token)
		if err != nil {
			t.Fatalf("newContinuationToken() = %q, parse error: %v", token, err)
		}
		if parsed.Version() != uuid.Version(4) {
			t.Errorf("token UUID version = %d, want version 4", parsed.Version())
		}
		if parsed.Variant() != uuid.RFC4122 {
			t.Errorf("token UUID variant = %v, want RFC 4122", parsed.Variant())
		}
		if _, duplicate := seen[token]; duplicate {
			t.Fatalf("newContinuationToken() repeated token %q", token)
		}
		seen[token] = struct{}{}
	}
}

type sessionCleanupTestCursor struct {
	closed bool
}

func (cursor *sessionCleanupTestCursor) Close() error {
	cursor.closed = true
	return nil
}

func TestQuerySessionExpiryRejectsAndCleansExpiredCursor(t *testing.T) {
	manager := &SessionManager{sessions: make(map[string]*QuerySession)}
	now := time.Now()
	cursor := &sessionCleanupTestCursor{}
	manager.Store(&QuerySession{
		QueryID:   "expired",
		ExpiresAt: now.Add(-time.Second),
		Cursor:    cursor,
	})
	manager.Store(&QuerySession{
		QueryID:   "active",
		ExpiresAt: now.Add(time.Minute),
	})

	if _, err := manager.Get("expired"); err == nil {
		t.Fatal("Get() accepted an expired session")
	}
	if removed := manager.cleanupExpiredSessions(now); removed != 1 {
		t.Fatalf("cleanupExpiredSessions() removed %d sessions, want 1", removed)
	}
	if !cursor.closed {
		t.Fatal("cleanup did not close the expired query cursor")
	}
	if _, err := manager.Get("expired"); err == nil {
		t.Fatal("expired session remains addressable after cleanup")
	}
	if _, err := manager.Get("active"); err != nil {
		t.Fatalf("cleanup removed a live session: %v", err)
	}
}
