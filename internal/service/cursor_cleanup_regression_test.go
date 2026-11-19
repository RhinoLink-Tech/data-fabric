package service

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestReviewCleanupDoesNotWaitForEmptyCursor(t *testing.T) {
	session := &QuerySession{QueryID: "q", ExpiresAt: time.Now().Add(-time.Second)}
	if err := session.continuationLock.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer session.continuationLock.Unlock()
	sm := &SessionManager{sessions: map[string]*QuerySession{"q": session}}
	done := make(chan struct{})
	go func() { sm.cleanupExpiredSessions(time.Now()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup waited for a continuation with no cursor")
	}
}

func TestReviewCleanupDoesNotCloseSlowActiveCursor(t *testing.T) {
	cursor := newBlockingCursor()
	session := &QuerySession{QueryID: "q", ExpiresAt: time.Now().Add(-time.Second), Cursor: cursor}
	if err := session.continuationLock.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			session.continuationLock.Unlock()
		}
	}()
	sm := &SessionManager{sessions: map[string]*QuerySession{"q": session}}
	done := make(chan struct{})
	go func() { sm.cleanupExpiredSessions(time.Now()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup blocked on the active continuation")
	}
	select {
	case <-cursor.closed:
		t.Fatal("cleanup closed the active cursor")
	case <-time.After(2300 * time.Millisecond):
	}
	session.continuationLock.Unlock()
	locked = false
	select {
	case <-cursor.closed:
	case <-time.After(time.Second):
		t.Fatal("deferred cursor close did not finish")
	}
	sm.closing.Wait()
}

func TestReviewShutdownWaitsForDeferredClose(t *testing.T) {
	cursor := newBlockingCursor()
	session := &QuerySession{QueryID: "q", ExpiresAt: time.Now().Add(-time.Second), Cursor: cursor}
	if err := session.continuationLock.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			session.continuationLock.Unlock()
		}
	}()
	sm := &SessionManager{sessions: map[string]*QuerySession{"q": session}}
	sm.cleanupExpiredSessions(time.Now())
	done := make(chan struct{})
	go func() { sm.shutdown(); close(done) }()
	select {
	case <-done:
		t.Fatal("shutdown returned while a deferred cursor remained open")
	case <-time.After(100 * time.Millisecond):
	}
	session.continuationLock.Unlock()
	locked = false
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after the continuation")
	}
	select {
	case <-cursor.closed:
	default:
		t.Fatal("shutdown returned before the cursor closed")
	}
}

func TestReviewTransportCleanupWaitsForDeferredClose(t *testing.T) {
	cursor := newBlockingCursor()
	session := &QuerySession{QueryID: "q", ExpiresAt: time.Now().Add(time.Minute), Cursor: cursor}
	session.creator.transportSessionID = "synthetic-owner"
	if err := session.continuationLock.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			session.continuationLock.Unlock()
		}
	}()
	sm := &SessionManager{sessions: map[string]*QuerySession{"q": session}}
	done := make(chan struct{})
	go func() { sm.deleteTransportSession("synthetic-owner"); close(done) }()
	select {
	case <-done:
		t.Fatal("transport cleanup returned while the cursor was still active")
	case <-time.After(100 * time.Millisecond):
	}
	session.continuationLock.Unlock()
	locked = false
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("transport cleanup did not finish")
	}
	select {
	case <-cursor.closed:
	default:
		t.Fatal("transport slot could be released before cursor close")
	}
	sm.closing.Wait()
}

func TestReviewConcurrentShutdownAndExpiryCloseExactlyOnce(t *testing.T) {
	for i := 0; i < 100; i++ {
		cursor := newBlockingCursor()
		sm := &SessionManager{sessions: map[string]*QuerySession{"q": {QueryID: "q", ExpiresAt: time.Now().Add(-time.Second), Cursor: cursor}}}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); sm.cleanupExpiredSessions(time.Now()) }()
		go func() { defer wg.Done(); sm.shutdown() }()
		wg.Wait()
		select {
		case <-cursor.closed:
		default:
			t.Fatal("concurrent cleanup and shutdown left cursor open")
		}
	}
}
