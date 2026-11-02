package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

func TestStreamAdmissionLimiterEnforcesTenantAndProcessCaps(t *testing.T) {
	limiter := newStreamAdmissionLimiter()
	callerA := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	callerB := &security.Caller{UserID: "bob", TenantID: "tenant-b"}

	releaseA, ok := limiter.acquire(callerA, 2, 1)
	if !ok {
		t.Fatal("first stream for tenant-a was rejected")
	}
	if release, ok := limiter.acquire(&security.Caller{UserID: "carol", TenantID: "tenant-a"}, 2, 1); ok {
		release()
		t.Fatal("second stream for tenant-a was admitted above its limit")
	}
	releaseB, ok := limiter.acquire(callerB, 2, 1)
	if !ok {
		t.Fatal("tenant-b was blocked by tenant-a's per-tenant limit")
	}
	if release, ok := limiter.acquire(&security.Caller{UserID: "dave", TenantID: "tenant-c"}, 2, 1); ok {
		release()
		t.Fatal("third stream was admitted above the process-wide limit")
	}

	releaseA()
	releaseA() // release callbacks are safe when startup cleanup also runs.
	releaseAgain, ok := limiter.acquire(callerA, 2, 1)
	if !ok {
		t.Fatal("tenant-a capacity was not restored after release")
	}
	releaseAgain()
	releaseB()
}

func TestStreamAdmissionLimiterUsesUserFallbackAndAnonymousBucket(t *testing.T) {
	limiter := newStreamAdmissionLimiter()
	firstUser, ok := limiter.acquire(&security.Caller{UserID: "alice"}, 10, 1)
	if !ok {
		t.Fatal("first tenantless user stream was rejected")
	}
	if release, ok := limiter.acquire(&security.Caller{UserID: "alice"}, 10, 1); ok {
		release()
		t.Fatal("tenantless user's second stream exceeded the user fallback limit")
	}
	otherUser, ok := limiter.acquire(&security.Caller{UserID: "bob"}, 10, 1)
	if !ok {
		t.Fatal("one tenantless user blocked a different user")
	}
	firstUser()
	otherUser()

	anonymous, ok := limiter.acquire(nil, 10, 1)
	if !ok {
		t.Fatal("first anonymous stream was rejected")
	}
	if release, ok := limiter.acquire(nil, 10, 1); ok {
		release()
		t.Fatal("anonymous callers did not share the fallback bucket")
	}
	anonymous()
}

func TestStreamAdmissionLimiterConcurrentAdmissionIsAtomic(t *testing.T) {
	const (
		goroutines = 64
		perTenant  = 7
	)
	limiter := newStreamAdmissionLimiter()
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
			release, admitted := limiter.acquire(&security.Caller{TenantID: "shared"}, 100, perTenant)
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
		activeStreams:          make(map[string]*ActiveStream),
		maxStreams:             defaultMaxConcurrentStreams,
		maxStreamsPerTenant:    1,
		streamAdmissionLimiter: newStreamAdmissionLimiter(),
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
