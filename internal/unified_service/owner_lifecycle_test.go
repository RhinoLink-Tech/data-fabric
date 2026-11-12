package unifiedservice

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/service"
)

func waitForOwnerCleanup(t *testing.T, owner *OwnerLifecycle) {
	t.Helper()
	select {
	case <-owner.cleaned:
	case <-time.After(3 * time.Second):
		t.Fatal("owner did not finish retirement")
	}
}

func TestOwnerDrainServesContinuationsAndRetiresWhenIdle(t *testing.T) {
	var pending atomic.Int32
	pending.Store(1)
	owner := NewOwnerLifecycle(time.Hour, func(time.Time) int {
		return int(pending.Load())
	}, func() {})
	t.Cleanup(owner.Shutdown)

	first := owner.Drain()
	again := owner.Drain()
	if first.State != ownerDraining || first.DrainDeadline == nil ||
		again.DrainDeadline == nil || !first.DrainDeadline.Equal(*again.DrainDeadline) {
		t.Fatalf("repeated drain changed the deadline: first=%+v again=%+v", first, again)
	}
	if _, _, err := owner.enter(context.Background(), true); !errors.Is(err, service.ErrQueryOwnerDraining) {
		t.Fatalf("new query after drain got %v", err)
	}
	ctx, release, err := owner.enter(context.Background(), false)
	if err != nil {
		t.Fatalf("continuation during drain: %v", err)
	}
	finish := sync.OnceFunc(release)
	defer finish()
	pending.Store(0)
	if ctx.Err() != nil {
		t.Fatalf("drain canceled live continuation: %v", ctx.Err())
	}
	finish()
	waitForOwnerCleanup(t, owner)
	if got := owner.Status(); got.State != ownerRetired || !got.CleanupComplete || got.InFlight != 0 {
		t.Fatalf("retired owner = %+v", got)
	}
	if _, _, err := owner.enter(context.Background(), false); !errors.Is(err, service.ErrQuerySessionUnavailable) {
		t.Fatalf("continuation after retirement got %v", err)
	}
}

func TestOwnerDrainDeadlineCancelsWorkBeforeClosingResources(t *testing.T) {
	var closes atomic.Int32
	owner := NewOwnerLifecycle(25*time.Millisecond, func(time.Time) int { return 1 }, func() {
		closes.Add(1)
	})
	t.Cleanup(owner.Shutdown)
	ctx, release, err := owner.enter(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	finish := sync.OnceFunc(release)
	defer finish()
	owner.Drain()

	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("drain deadline did not cancel active work")
	}
	if got := owner.Status(); got.State != ownerRetired || got.InFlight != 1 || got.CleanupComplete {
		t.Fatalf("owner retired with work still unwinding = %+v", got)
	}
	if closes.Load() != 0 {
		t.Fatal("session resources closed while the query was still active")
	}
	finish()
	waitForOwnerCleanup(t, owner)
	owner.Shutdown()
	if got := closes.Load(); got != 1 {
		t.Fatalf("resource cleanup count = %d, want 1", got)
	}
}
