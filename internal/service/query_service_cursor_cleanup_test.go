package service

import (
	"context"
	"sync"
	"testing"
	"time"
)

// blockingCursor records the moment Close() runs so a test can detect whether
// the cursor was closed while a continuation was still reading it.
type blockingCursor struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingCursor() *blockingCursor {
	return &blockingCursor{closed: make(chan struct{})}
}

func (c *blockingCursor) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

// closeQueryCursor closes session.Cursor without honoring continuationLock, so
// an in-flight FetchNextBatch that holds the lock can be reading the cursor
// (streaming rows out of a *sql.Rows or an object-storage iterator) when the
// TTL cleanup goroutine closes it. That concurrent use is a driver-level error
// (and, on some cursors, a panic). This test pins the required behavior: the
// expiry cleanup must wait for the active continuation to release the lock
// before closing the cursor.
func TestCleanupExpiredSessionsWaitsForInFlightContinuation(t *testing.T) {
	cursor := newBlockingCursor()
	session := &QuerySession{
		QueryID:   "q",
		ExpiresAt: time.Now().Add(-time.Second),
		Cursor:    cursor,
	}
	manager := &SessionManager{sessions: map[string]*QuerySession{"q": session}}

	// Simulate a FetchNextBatch that is mid-read: it holds the continuation lock.
	if err := session.continuationLock.Lock(context.Background()); err != nil {
		t.Fatalf("continuationLock.Lock() error = %v", err)
	}

	cleanupDone := make(chan struct{})
	go func() {
		manager.cleanupExpiredSessions(time.Now())
		close(cleanupDone)
	}()

	// The cleanup must not close the cursor while the continuation is reading it.
	select {
	case <-cursor.closed:
		t.Fatal("cleanup closed the cursor while a continuation was still reading it")
	case <-time.After(150 * time.Millisecond):
	}

	// When the continuation finishes, the cleanup may close the cursor.
	session.continuationLock.Unlock()
	select {
	case <-cursor.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not close the cursor after the continuation finished")
	}
	select {
	case <-cleanupDone:
	default:
		t.Fatal("cleanup did not finish")
	}
}
