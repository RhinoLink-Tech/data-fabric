package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

func TestQueryRequestRateLimitSharesTenantBucketAndUserFallback(t *testing.T) {
	limiter := NewQueryAdmissionLimiterWithRateLimit(100, 25, 60, 1)
	firstTenantUser := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	secondTenantUser := &security.Caller{UserID: "bob", TenantID: "tenant-a"}
	otherTenant := &security.Caller{UserID: "carol", TenantID: "tenant-b"}

	if !limiter.AllowQueryRequest(firstTenantUser) {
		t.Fatal("first query request for tenant-a was rejected")
	}
	if limiter.AllowQueryRequest(secondTenantUser) {
		t.Fatal("second user bypassed tenant-a's shared request burst")
	}
	if !limiter.AllowQueryRequest(otherTenant) {
		t.Fatal("tenant-a exhausted tenant-b's independent request budget")
	}

	if !limiter.AllowQueryRequest(&security.Caller{UserID: "tenantless"}) {
		t.Fatal("first tenant-less user request was rejected")
	}
	if limiter.AllowQueryRequest(&security.Caller{UserID: "tenantless"}) {
		t.Fatal("tenant-less user did not share its user fallback bucket")
	}
	if !limiter.AllowQueryRequest(&security.Caller{UserID: "another-user"}) {
		t.Fatal("one tenant-less user blocked a different user's fallback bucket")
	}
}

func TestQueryRequestRateLimitBucketCardinalityIsBounded(t *testing.T) {
	limiter := NewQueryAdmissionLimiterWithRateLimit(100, 25, 60000, 10)
	limiter.maxRequestBuckets = 1
	tenantA := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	tenantB := &security.Caller{UserID: "bob", TenantID: "tenant-b"}

	if !limiter.AllowQueryRequest(tenantA) {
		t.Fatal("first tenant request was rejected")
	}
	bucketA := limiter.requestBuckets["tenant:tenant-a"]
	if bucketA == nil {
		t.Fatal("tenant-a bucket was not created")
	}
	if limiter.AllowQueryRequest(tenantB) {
		t.Fatal("new tenant was admitted after the bucket table reached capacity")
	}
	if limiter.requestBuckets["tenant:tenant-a"] != bucketA {
		t.Fatal("existing tenant bucket was evicted")
	}
	if !limiter.AllowQueryRequest(tenantA) {
		t.Fatal("existing tenant was rejected while its own budget remained available")
	}
	if got := len(limiter.requestBuckets); got != 1 {
		t.Fatalf("bucket count = %d, want hard cap 1", got)
	}
}

func TestQueryRequestRateLimitReclaimsStaleBucketAtCapacity(t *testing.T) {
	limiter := NewQueryAdmissionLimiterWithRateLimit(100, 25, 60000, 10)
	limiter.maxRequestBuckets = 1
	tenantA := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	tenantB := &security.Caller{UserID: "bob", TenantID: "tenant-b"}

	if !limiter.AllowQueryRequest(tenantA) {
		t.Fatal("first tenant request was rejected")
	}
	limiter.requestBuckets["tenant:tenant-a"].lastSeen = time.Now().Add(-11 * time.Minute)
	limiter.lastRequestBucketCleanupAt = time.Now().Add(-time.Minute)

	if !limiter.AllowQueryRequest(tenantB) {
		t.Fatal("new tenant was rejected after stale bucket cleanup freed capacity")
	}
	if _, exists := limiter.requestBuckets["tenant:tenant-a"]; exists {
		t.Fatal("stale tenant bucket was not removed")
	}
	if _, exists := limiter.requestBuckets["tenant:tenant-b"]; !exists {
		t.Fatal("new tenant bucket was not created")
	}
	if got := len(limiter.requestBuckets); got != 1 {
		t.Fatalf("bucket count = %d, want hard cap 1", got)
	}
}

func TestQueryRequestRateLimitRejectsPaginationBeforeSourceConnection(t *testing.T) {
	source := &model.DataSource{
		ID: "ds-1", Type: model.DatabaseTypePostgreSQL,
		OwnerID: "alice", Status: model.DataSourceStatusActive,
	}
	caller := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	limiter := NewQueryAdmissionLimiterWithRateLimit(100, 25, 60, 1)
	if !limiter.AllowQueryRequest(caller) {
		t.Fatal("failed to consume the tenant request burst")
	}
	query := NewQueryServiceWithTableAuthorizerAndStatsAndAdmissionLimiter(
		federationSourceRepository{source: source}, nil, false, nil, nil, limiter,
		newTestSQLTranslationManager(),
	).(*queryService)

	_, err := query.FetchQuery(context.Background(), caller, &model.FetchQueryRequest{
		DataSourceID: source.ID,
		SQL:          "SELECT 1",
		Type:         1,
		BatchSize:    10,
	})
	if !errors.Is(err, ErrQueryRateLimitExceeded) {
		t.Fatalf("FetchQuery() error = %v, want %v", err, ErrQueryRateLimitExceeded)
	}
	release, admitted := limiter.Acquire(caller)
	if !admitted {
		t.Fatal("rate-limited query leaked its concurrency reservation")
	}
	release()
}

func TestQueryRequestRateLimitRejectsPaginationContinuationBeforeSourceConnection(t *testing.T) {
	source := &model.DataSource{
		ID: "ds-1", Type: model.DatabaseTypePostgreSQL,
		OwnerID: "alice", Status: model.DataSourceStatusActive,
	}
	caller := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	limiter := NewQueryAdmissionLimiterWithRateLimit(100, 25, 60, 1)
	if !limiter.AllowQueryRequest(caller) {
		t.Fatal("failed to consume the tenant request burst")
	}
	query := NewQueryServiceWithTableAuthorizerAndStatsAndAdmissionLimiter(
		federationSourceRepository{source: source}, nil, false, nil, nil, limiter,
		newTestSQLTranslationManager(),
	).(*queryService)
	query.sessionManager.Store(&QuerySession{
		QueryID: "query-1", Slug: "slug-1", Token: "token-1", CreatorID: caller.UserID,
		DataSourceID: source.ID, SQL: "SELECT 1", TotalRows: 10, FetchedRows: 0,
		ExpiresAt: time.Now().Add(time.Minute),
	})

	_, err := query.FetchNextBatch(context.Background(), caller, "query-1", "slug-1", "token-1", 5)
	if !errors.Is(err, ErrQueryRateLimitExceeded) {
		t.Fatalf("FetchNextBatch() error = %v, want %v", err, ErrQueryRateLimitExceeded)
	}
	release, admitted := limiter.Acquire(caller)
	if !admitted {
		t.Fatal("rate-limited continuation leaked its concurrency reservation")
	}
	release()
}

func TestQueryRequestRateLimitRejectsStreamBeforeOpeningSourceConnection(t *testing.T) {
	source := &model.DataSource{
		ID: "ds-1", Type: model.DatabaseTypePostgreSQL,
		OwnerID: "alice", Status: model.DataSourceStatusActive,
	}
	caller := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	limiter := NewQueryAdmissionLimiterWithRateLimit(100, 25, 60, 1)
	if !limiter.AllowQueryRequest(caller) {
		t.Fatal("failed to consume the tenant request burst")
	}
	streaming := NewStreamingServiceWithTableAuthorizerAndStatsAndAdmissionLimiter(
		federationSourceRepository{source: source}, nil, nil, nil, limiter,
		newTestSQLTranslationManager(),
	).(*streamingService)

	_, _, err := streaming.ExecuteStreamQuery(context.Background(), caller, &model.QueryRequest{
		DataSourceID: source.ID,
		SQL:          "SELECT 1",
	})
	if !errors.Is(err, ErrQueryRateLimitExceeded) {
		t.Fatalf("ExecuteStreamQuery() error = %v, want %v", err, ErrQueryRateLimitExceeded)
	}
	release, admitted := limiter.Acquire(caller)
	if !admitted {
		t.Fatal("rate-limited stream leaked its concurrency reservation")
	}
	release()
}

func TestQueryRequestRateLimitRejectsStreamingContinuationBeforeReadingRows(t *testing.T) {
	source := &model.DataSource{
		ID: "ds-1", Type: model.DatabaseTypePostgreSQL,
		OwnerID: "alice", Status: model.DataSourceStatusActive,
	}
	caller := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	limiter := NewQueryAdmissionLimiterWithRateLimit(100, 25, 60, 1)
	if !limiter.AllowQueryRequest(caller) {
		t.Fatal("failed to consume the tenant request burst")
	}
	streaming := NewStreamingServiceWithTableAuthorizerAndStatsAndAdmissionLimiter(
		federationSourceRepository{source: source}, nil, nil, nil, limiter,
		newTestSQLTranslationManager(),
	).(*streamingService)
	streaming.streamingSessionManager.Store(&StreamingSession{
		QueryID: "query-1", Slug: "slug-1", Token: "token-1", CreatorID: caller.UserID,
		DataSourceID: source.ID, SQL: "SELECT 1", Tables: nil, StreamID: "stream-1",
		TotalRows: 10, FetchedRows: 0, ExpiresAt: time.Now().Add(time.Minute),
	})
	streaming.activeStreams["stream-1"] = &ActiveStream{ID: "stream-1"}

	_, err := streaming.FetchNextBatchWithStreaming(context.Background(), caller, "query-1", "slug-1", "token-1", 5)
	if !errors.Is(err, ErrQueryRateLimitExceeded) {
		t.Fatalf("FetchNextBatchWithStreaming() error = %v, want %v", err, ErrQueryRateLimitExceeded)
	}
}
