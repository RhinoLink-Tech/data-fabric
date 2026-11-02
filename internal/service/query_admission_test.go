package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/execution"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

type blockingNativeAdmissionExecutor struct {
	started chan struct{}
}

func (e blockingNativeAdmissionExecutor) Capabilities() execution.Capabilities {
	return execution.Capabilities{}
}

func (e blockingNativeAdmissionExecutor) ExecuteNativeAPI(ctx context.Context, _ execution.NativeAPIRequest) (execution.ResultStream, error) {
	close(e.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestStreamingAndPaginationShareAdmissionCapacity(t *testing.T) {
	limiter := NewQueryAdmissionLimiterWithRateLimit(2, 1, 60, 1)
	streaming := NewStreamingServiceWithTableAuthorizerAndStatsAndAdmissionLimiter(
		nil, nil, nil, nil, limiter, newTestSQLTranslationManager(),
	).(*streamingService)
	pagination := NewQueryServiceWithTableAuthorizerAndStatsAndAdmissionLimiter(
		nil, nil, false, nil, nil, limiter, newTestSQLTranslationManager(),
	).(*queryService)
	tenantA := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	if !streaming.admissionLimiter.AllowQueryRequest(tenantA) {
		t.Fatal("first streaming request for tenant-a was rejected")
	}
	if pagination.admissionLimiter.AllowQueryRequest(&security.Caller{UserID: "bob", TenantID: "tenant-a"}) {
		t.Fatal("pagination did not share the streaming tenant request budget")
	}

	releaseStream, admitted := streaming.acquireStreamAdmission(tenantA)
	if !admitted {
		t.Fatal("streaming service could not reserve tenant-a's slot")
	}
	if release, admitted := pagination.acquireQueryAdmission(&security.Caller{UserID: "bob", TenantID: "tenant-a"}); admitted {
		release()
		t.Fatal("pagination service bypassed the active streaming tenant limit")
	}
	releasePage, admitted := pagination.acquireQueryAdmission(&security.Caller{UserID: "carol", TenantID: "tenant-b"})
	if !admitted {
		t.Fatal("tenant-a's active stream blocked tenant-b pagination")
	}
	if release, admitted := streaming.acquireStreamAdmission(&security.Caller{UserID: "dave", TenantID: "tenant-c"}); admitted {
		release()
		t.Fatal("mixed streaming and pagination exceeded the shared process cap")
	}

	releaseStream()
	releasePage()
	releaseAgain, admitted := pagination.acquireQueryAdmission(tenantA)
	if !admitted {
		t.Fatal("released shared capacity was not available to pagination")
	}
	releaseAgain()
}

func TestPaginatedQueryAdmissionRejectsBeforeOpeningSourceConnection(t *testing.T) {
	source := &model.DataSource{
		ID: "ds-1", Type: model.DatabaseTypePostgreSQL,
		OwnerID: "alice", Status: model.DataSourceStatusActive,
	}
	limiter := NewQueryAdmissionLimiter(100, 1)
	caller := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	release, admitted := limiter.Acquire(caller)
	if !admitted {
		t.Fatal("failed to occupy tenant-a's query slot")
	}
	defer release()
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
	if !errors.Is(err, ErrQueryAdmissionLimit) {
		t.Fatalf("FetchQuery() error = %v, want %v", err, ErrQueryAdmissionLimit)
	}
}

func TestPaginatedContinuationAdmissionRejectsBeforeOpeningSourceConnection(t *testing.T) {
	source := &model.DataSource{
		ID: "ds-1", Type: model.DatabaseTypePostgreSQL,
		OwnerID: "alice", Status: model.DataSourceStatusActive,
	}
	limiter := NewQueryAdmissionLimiter(100, 1)
	caller := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	release, admitted := limiter.Acquire(caller)
	if !admitted {
		t.Fatal("failed to occupy tenant-a's query slot")
	}
	defer release()
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
	if !errors.Is(err, ErrQueryAdmissionLimit) {
		t.Fatalf("FetchNextBatch() error = %v, want %v", err, ErrQueryAdmissionLimit)
	}
}

func TestPaginatedContinuationCancellationReleasesAdmission(t *testing.T) {
	source := &model.DataSource{
		ID: "ds-1", Type: model.DatabaseTypePostgreSQL,
		OwnerID: "alice", Status: model.DataSourceStatusActive,
	}
	caller := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
	limiter := NewQueryAdmissionLimiter(100, 1)
	query := NewQueryServiceWithTableAuthorizerAndStatsAndAdmissionLimiter(
		federationSourceRepository{source: source}, nil, false, nil, nil, limiter,
		newTestSQLTranslationManager(),
	).(*queryService)
	executor := blockingNativeAdmissionExecutor{started: make(chan struct{})}
	query.sessionManager.Store(&QuerySession{
		QueryID: "query-1", Slug: "slug-1", Token: "token-1", CreatorID: caller.UserID,
		DataSourceID: source.ID, SQL: "SELECT 1", TotalRows: 10, FetchedRows: 0,
		Cursor:    &nativeAPIQueryCursor{executor: executor, query: "SELECT 1"},
		ExpiresAt: time.Now().Add(time.Minute),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := query.FetchNextBatch(ctx, caller, "query-1", "slug-1", "token-1", 5)
		result <- err
	}()
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("native continuation did not start")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("FetchNextBatch() error = %v, want context cancellation", err)
	}

	release, admitted := limiter.Acquire(caller)
	if !admitted {
		t.Fatal("canceled pagination batch leaked its admission slot")
	}
	release()
}
