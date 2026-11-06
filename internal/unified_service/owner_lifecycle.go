package unifiedservice

import (
	"context"
	"sync"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/service"
)

const (
	ownerReady    = "ready"
	ownerDraining = "draining"
	ownerRetired  = "retired"
)

// OwnerStatus is exposed through the replica's private admin routes.
type OwnerStatus struct {
	PolicyRevision  string     `json:"policyRevision,omitempty"`
	State           string     `json:"state"`
	DrainDeadline   *time.Time `json:"drainDeadline,omitempty"`
	PendingSessions int        `json:"pendingSessions"`
	InFlight        int        `json:"inFlight"`
	CleanupComplete bool       `json:"cleanupComplete"`
}

// OwnerLifecycle gates local query work while an operator drains a replica.
// Its mutex serializes admission, retirement, and the idle snapshot.
type OwnerLifecycle struct {
	mu       sync.Mutex
	state    string
	deadline time.Time
	inFlight int
	idle     chan struct{}
	lifetime context.Context
	cancel   context.CancelFunc
	cleaned  chan struct{}
	timeout  time.Duration
	pending  func(time.Time) int
	cleanup  func()
}

func NewOwnerLifecycle(timeout time.Duration, pending func(time.Time) int, cleanup func()) *OwnerLifecycle {
	lifetime, cancel := context.WithCancel(context.Background())
	idle := make(chan struct{})
	close(idle)
	return &OwnerLifecycle{
		state: ownerReady, idle: idle, lifetime: lifetime, cancel: cancel,
		cleaned: make(chan struct{}), timeout: timeout, pending: pending, cleanup: cleanup,
	}
}

func (o *OwnerLifecycle) Status() OwnerStatus {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.statusLocked()
}

// BeginInitialization reserves a new MCP transport session only while the owner is ready.
func (o *OwnerLifecycle) BeginInitialization(ctx context.Context) (context.Context, func(), error) {
	return o.enter(ctx, true)
}

// Drain closes admission for new queries and starts a single drain deadline.
func (o *OwnerLifecycle) Drain() OwnerStatus {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.state == ownerReady {
		o.state = ownerDraining
		o.deadline = time.Now().Add(o.timeout)
		go o.waitForDrain(o.deadline)
	}
	return o.statusLocked()
}

func (o *OwnerLifecycle) statusLocked() OwnerStatus {
	status := OwnerStatus{State: o.state, InFlight: o.inFlight, PendingSessions: o.pending(time.Now())}
	if !o.deadline.IsZero() {
		deadline := o.deadline
		status.DrainDeadline = &deadline
	}
	select {
	case <-o.cleaned:
		status.CleanupComplete = true
	default:
	}
	return status
}

func (o *OwnerLifecycle) enter(ctx context.Context, initial bool) (context.Context, func(), error) {
	o.mu.Lock()
	if o.state == ownerDraining && !time.Now().Before(o.deadline) {
		o.retireLocked()
	}
	if initial && o.state != ownerReady {
		o.mu.Unlock()
		return nil, nil, service.ErrQueryOwnerDraining
	}
	if o.state == ownerRetired {
		o.mu.Unlock()
		return nil, nil, service.ErrQuerySessionUnavailable
	}
	if o.inFlight == 0 {
		o.idle = make(chan struct{})
	}
	o.inFlight++
	o.mu.Unlock()

	workCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(o.lifetime, cancel)
	if o.lifetime.Err() != nil {
		cancel()
	}
	return workCtx, func() {
		stop()
		cancel()
		o.mu.Lock()
		o.inFlight--
		if o.inFlight == 0 {
			close(o.idle)
		}
		o.mu.Unlock()
	}, nil
}

func (o *OwnerLifecycle) waitForDrain(deadline time.Time) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	// Sessions can expire without a request, so also check for idle owners
	// between arrivals. The deadline timer remains independent of this poll.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		o.mu.Lock()
		now := time.Now()
		if o.state != ownerDraining {
			o.mu.Unlock()
			return
		}
		if !now.Before(deadline) || (o.inFlight == 0 && o.pending(now) == 0) {
			o.retireLocked()
			o.mu.Unlock()
			return
		}
		o.mu.Unlock()
		select {
		case <-o.lifetime.Done():
			return
		case <-timer.C:
		case <-ticker.C:
		}
	}
}

func (o *OwnerLifecycle) retireLocked() {
	if o.state == ownerRetired {
		return
	}
	o.state = ownerRetired
	o.cancel()
	idle := o.idle
	go func() {
		// Native cursor Close may wait on an in-flight page. Cancel the page
		// and wait for query work before closing session resources.
		<-idle
		o.cleanup()
		close(o.cleaned)
	}()
}

// Shutdown retires the owner before its server closes connection pools.
func (o *OwnerLifecycle) Shutdown() {
	o.mu.Lock()
	o.retireLocked()
	o.mu.Unlock()
	<-o.cleaned
}
