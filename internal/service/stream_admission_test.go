package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

func TestSourceInvalidationClosesMatchingStreamsAndReleasesCapacity(t *testing.T) {
	limiter := NewQueryAdmissionLimiter(2, 1)
	callerA := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	callerB := &security.Caller{UserID: "bob", TenantID: "tenant-b"}
	releaseA, ok := limiter.Acquire(callerA)
	if !ok {
		t.Fatal("could not reserve tenant-a capacity")
	}
	releaseB, ok := limiter.Acquire(callerB)
	if !ok {
		t.Fatal("could not reserve tenant-b capacity")
	}

	cancelled := false
	manager := NewStreamingSessionManager()
	manager.Store(&StreamingSession{QueryID: "query-a", StreamID: "stream-a"})
	streams := &streamingService{
		activeStreams: map[string]*ActiveStream{
			"stream-a": {
				ID:               "stream-a",
				DataSourceID:     "source-a",
				CancelFunc:       func() { cancelled = true },
				releaseAdmission: releaseA,
			},
			"stream-b": {
				ID:               "stream-b",
				DataSourceID:     "source-b",
				releaseAdmission: releaseB,
			},
		},
		streamingSessionManager: manager,
	}

	if closed := streams.CloseDataSourceStreams("source-a"); closed != 1 {
		t.Fatalf("closed %d streams, want 1", closed)
	}
	if !cancelled {
		t.Fatal("source invalidation did not cancel the matching stream")
	}
	if _, exists := streams.activeStreams["stream-a"]; exists {
		t.Fatal("matching stream is still active")
	}
	if _, exists := streams.activeStreams["stream-b"]; !exists {
		t.Fatal("stream for a different source was closed")
	}
	manager.mutex.RLock()
	_, sessionExists := manager.sessions["query-a"]
	manager.mutex.RUnlock()
	if sessionExists {
		t.Fatal("continuation session for the invalidated stream still exists")
	}

	releaseAgain, admitted := limiter.Acquire(callerA)
	if !admitted {
		t.Fatal("tenant-a admission capacity was not released")
	}
	releaseAgain()
	if err := streams.CloseStream("stream-b"); err != nil {
		t.Fatalf("could not close unrelated stream after verifying it remained active: %v", err)
	}
}

func TestQueryAdmissionLimiterUsesMeasuredProfileCapsAndReleasesCapacity(t *testing.T) {
	limiter := NewQueryAdmissionLimiter(0, 0)
	callers := []*security.Caller{
		{UserID: "alice", TenantID: "tenant-a"},
		{UserID: "bob", TenantID: "tenant-a"},
		{UserID: "carol", TenantID: "tenant-b"},
		{UserID: "dave", TenantID: "tenant-b"},
	}
	releases := make([]func(), 0, len(callers))
	for _, caller := range callers {
		release, admitted := limiter.Acquire(caller)
		if !admitted {
			t.Fatalf("measured four-way workload rejected caller %q", caller.UserID)
		}
		releases = append(releases, release)
	}
	if release, admitted := limiter.Acquire(&security.Caller{UserID: "erin", TenantID: "tenant-c"}); admitted {
		release()
		t.Fatal("fifth active task exceeded the process-wide default cap")
	}
	for _, release := range releases {
		release()
	}
	release, admitted := limiter.Acquire(&security.Caller{UserID: "erin", TenantID: "tenant-c"})
	if !admitted {
		t.Fatal("process capacity was not restored after release")
	}
	release()
}

func TestQueryAdmissionLimiterClampsExplicitCaps(t *testing.T) {
	limiter := NewQueryAdmissionLimiter(1000, 1000)
	if limiter.maxConcurrent != maxConcurrentQueryLimit || limiter.maxPerTenant != maxConcurrentQueryLimit {
		t.Fatalf("limits = %d total / %d per tenant, want hard cap %d",
			limiter.maxConcurrent, limiter.maxPerTenant, maxConcurrentQueryLimit)
	}
}

func TestQueryAdmissionLimiterEnforcesTenantAndProcessCaps(t *testing.T) {
	limiter := NewQueryAdmissionLimiter(2, 1)
	callerA := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	callerB := &security.Caller{UserID: "bob", TenantID: "tenant-b"}

	releaseA, ok := limiter.Acquire(callerA)
	if !ok {
		t.Fatal("first stream for tenant-a was rejected")
	}
	if release, ok := limiter.Acquire(&security.Caller{UserID: "carol", TenantID: "tenant-a"}); ok {
		release()
		t.Fatal("second stream for tenant-a was admitted above its limit")
	}
	releaseB, ok := limiter.Acquire(callerB)
	if !ok {
		t.Fatal("tenant-b was blocked by tenant-a's per-tenant limit")
	}
	if release, ok := limiter.Acquire(&security.Caller{UserID: "dave", TenantID: "tenant-c"}); ok {
		release()
		t.Fatal("third stream was admitted above the process-wide limit")
	}

	releaseA()
	releaseA() // release callbacks are safe when startup cleanup also runs.
	releaseAgain, ok := limiter.Acquire(callerA)
	if !ok {
		t.Fatal("tenant-a capacity was not restored after release")
	}
	releaseAgain()
	releaseB()
}

func TestQueryAdmissionLimiterUsesUserFallbackAndAnonymousBucket(t *testing.T) {
	limiter := NewQueryAdmissionLimiter(10, 1)
	firstUser, ok := limiter.Acquire(&security.Caller{UserID: "alice"})
	if !ok {
		t.Fatal("first tenantless user stream was rejected")
	}
	if release, ok := limiter.Acquire(&security.Caller{UserID: "alice"}); ok {
		release()
		t.Fatal("tenantless user's second stream exceeded the user fallback limit")
	}
	otherUser, ok := limiter.Acquire(&security.Caller{UserID: "bob"})
	if !ok {
		t.Fatal("one tenantless user blocked a different user")
	}
	firstUser()
	otherUser()

	anonymous, ok := limiter.Acquire(nil)
	if !ok {
		t.Fatal("first anonymous stream was rejected")
	}
	if release, ok := limiter.Acquire(nil); ok {
		release()
		t.Fatal("anonymous callers did not share the fallback bucket")
	}
	anonymous()
}

func TestQueryAdmissionLimiterConcurrentAdmissionIsAtomic(t *testing.T) {
	const (
		goroutines = 64
		perTenant  = 7
	)
	limiter := NewQueryAdmissionLimiter(100, perTenant)
	start := make(chan struct{})
	type result struct {
		release  func()
		admitted bool
	}
	results := make(chan result, goroutines)
	var workers sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			release, admitted := limiter.Acquire(&security.Caller{TenantID: "shared"})
			results <- result{release: release, admitted: admitted}
		}()
	}
	close(start)
	workers.Wait()

	admitted := 0
	var releases []func()
	for i := 0; i < goroutines; i++ {
		result := <-results
		if result.admitted {
			admitted++
			releases = append(releases, result.release)
		}
	}
	if admitted != perTenant {
		t.Fatalf("concurrent admissions = %d, want %d", admitted, perTenant)
	}
	for _, release := range releases {
		release()
	}
}

func TestExecuteStreamQueryRejectsTenantCapBeforeOpeningConnection(t *testing.T) {
	source := &model.DataSource{
		ID: "ds-1", Type: model.DatabaseTypePostgreSQL,
		OwnerID: "alice", Status: model.DataSourceStatusActive,
	}
	ss := NewStreamingServiceWithTableAuthorizerAndStatsAndLimits(
		federationSourceRepository{source: source}, nil, nil, nil, 1,
		newTestSQLTranslationManager(),
	).(*streamingService)
	caller := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	release, admitted := ss.acquireStreamAdmission(caller)
	if !admitted {
		t.Fatal("failed to occupy the configured tenant stream slot")
	}
	defer release()

	_, _, err := ss.ExecuteStreamQuery(context.Background(), caller, &model.QueryRequest{
		DataSourceID: source.ID,
		SQL:          "SELECT 1",
	})
	if !errors.Is(err, ErrMaxConcurrentStreams) {
		t.Fatalf("ExecuteStreamQuery() error = %v, want %v", err, ErrMaxConcurrentStreams)
	}
}

func TestClosingStreamReleasesTenantAdmission(t *testing.T) {
	ss := &streamingService{
		activeStreams:    make(map[string]*ActiveStream),
		admissionLimiter: NewQueryAdmissionLimiter(100, 1),
	}
	caller := &security.Caller{TenantID: "tenant-a"}
	release, admitted := ss.acquireStreamAdmission(caller)
	if !admitted {
		t.Fatal("failed to acquire initial stream slot")
	}
	stream := &ActiveStream{ID: "stream-1", releaseAdmission: release}
	ss.activeStreams[stream.ID] = stream

	if err := ss.CloseStream(stream.ID); err != nil {
		t.Fatalf("CloseStream() error = %v", err)
	}
	if err := ss.CloseStream(stream.ID); err != nil {
		t.Fatalf("second CloseStream() error = %v", err)
	}
	if releaseAgain, admitted := ss.acquireStreamAdmission(caller); !admitted {
		t.Fatal("closing the stream did not release the tenant slot")
	} else {
		releaseAgain()
	}
}
