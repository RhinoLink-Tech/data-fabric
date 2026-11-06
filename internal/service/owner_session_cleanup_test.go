package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

func TestQueryServiceShutdownStopsCleanupAndClosesNativeCursors(t *testing.T) {
	service := NewQueryService(nil, nil, false, newTestSQLTranslationManager()).(*queryService)
	t.Cleanup(service.Shutdown)
	now := time.Now()
	live, expired := &nativeAPIQueryCursor{}, &nativeAPIQueryCursor{}
	manager := service.sessionManager
	manager.Store(&QuerySession{QueryID: "live", ExpiresAt: now.Add(time.Minute), Cursor: live})
	manager.Store(&QuerySession{QueryID: "expired", ExpiresAt: now.Add(-time.Second), Cursor: expired})
	manager.Store(&QuerySession{QueryID: "pagination", ExpiresAt: now.Add(time.Minute)})

	if got := service.LiveSessionCount(now); got != 2 {
		t.Fatalf("live session count = %d, want 2", got)
	}
	service.Shutdown()
	select {
	case <-manager.cleanupDone:
	default:
		t.Fatal("pagination cleanup goroutine survived shutdown")
	}
	if !live.closed || !expired.closed {
		t.Fatal("shutdown left native cursor state open")
	}
	if _, err := manager.Get("pagination"); err == nil {
		t.Fatal("shutdown left a plain pagination session")
	}
	late := &nativeAPIQueryCursor{}
	if manager.Store(&QuerySession{QueryID: "late", Cursor: late}) || !late.closed {
		t.Fatal("shutdown accepted a new native cursor")
	}
}

func TestInvalidatedNativeContinuationClosesCursor(t *testing.T) {
	tests := []struct {
		name   string
		change func(*model.DataSource)
		want   error
	}{
		{"source removed", func(ds *model.DataSource) { ds.ID = "replacement" }, repository.ErrDataSourceNotFound},
		{"source inactive", func(ds *model.DataSource) { ds.Status = model.DataSourceStatusInactive }, ErrDataSourceInactive},
		{"source grant removed", func(ds *model.DataSource) { ds.OwnerID, ds.TenantID = "bob", "other" }, security.ErrDataSourceAccessDenied},
	}
	caller := &security.Caller{UserID: "alice", TenantID: "acme"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := ownedActiveSource()
			qs := NewQueryService(federationSourceRepository{source: source}, nil, false, newTestSQLTranslationManager()).(*queryService)
			t.Cleanup(qs.Shutdown)
			cursor := &nativeAPIQueryCursor{}
			qs.sessionManager.Store(&QuerySession{
				QueryID: "q", Slug: "s", Token: "t", DataSourceID: source.ID,
				creator: querySessionPrincipalFor(caller), ExpiresAt: time.Now().Add(time.Minute),
				Cursor: cursor, TotalRows: 1, FetchedRows: 1,
			})
			tt.change(source)
			result, err := qs.FetchNextBatch(context.Background(), caller, "q", "s", "t", 1)
			if result != nil || !errors.Is(err, tt.want) || !cursor.closed {
				t.Fatalf("result=%+v error=%v closed=%t, want no result, %v, and closed cursor", result, err, cursor.closed, tt.want)
			}
			if _, err := qs.FetchNextBatch(context.Background(), caller, "q", "s", "t", 1); !errors.Is(err, ErrQuerySessionUnavailable) {
				t.Fatalf("invalidated continuation remained usable: %v", err)
			}
		})
	}
}

func TestInvalidatedStreamingContinuationClosesRowsAndReleasesSlot(t *testing.T) {
	svc, manager, session, rows, cancelled := newExpiredStreamingFixture(t, nil)
	session.ExpiresAt = time.Now().Add(time.Minute)
	session.Slug, session.Token = "s", "t"
	session.DataSourceID = "removed-source"
	caller := &security.Caller{UserID: "alice", TenantID: "acme"}
	session.creator = querySessionPrincipalFor(caller)
	svc.datasourceRepo = federationSourceRepository{source: ownedActiveSource()}
	released := false
	svc.activeStreams[session.StreamID].releaseAdmission = func() { released = true }

	if _, err := svc.FetchNextBatchWithStreaming(context.Background(), caller, session.QueryID, "s", "t", 1); !errors.Is(err, repository.ErrDataSourceNotFound) {
		t.Fatalf("continuation after deletion error = %v", err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("source deletion did not cancel the stream")
	}
	if manager.isCurrent(session) || len(svc.GetAllStreams()) != 0 || !rows.closed || !released {
		t.Fatal("source deletion left a streaming session, rows, or admission slot open")
	}
}

func TestStreamingShutdownReleasesSlotAfterRowsCloseError(t *testing.T) {
	closeErr := errors.New("driver rows close failed")
	svc, manager, session, _, _ := newExpiredStreamingFixture(t, closeErr)
	session.ExpiresAt = time.Now().Add(time.Minute)
	releases := 0
	svc.activeStreams[session.StreamID].releaseAdmission = func() { releases++ }

	if err := svc.Shutdown(); !errors.Is(err, closeErr) {
		t.Fatalf("shutdown error = %v, want driver close error", err)
	}
	if manager.isCurrent(session) || len(svc.GetAllStreams()) != 0 || releases != 1 {
		t.Fatalf("failed shutdown left resources registered, admission releases = %d", releases)
	}
}
