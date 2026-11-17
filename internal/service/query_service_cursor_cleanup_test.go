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
func TestCleanupExpiredSessionsDefersCloseUntilContinuationFinishes(t *testing.T) {
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

	// Cleanup should remain responsive without closing an active cursor.
	select {
	case <-cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("cleanup blocked on an active continuation")
	}
	select {
	case <-cursor.closed:
		t.Fatal("cleanup closed the cursor while a continuation was still reading it")
	default:
	}

	session.continuationLock.Unlock()
	select {
	case <-cursor.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not close the cursor after the continuation finished")
	}
	manager.closing.Wait()
}
