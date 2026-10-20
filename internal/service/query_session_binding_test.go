package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

// #9 query-path authorization. Fetch endpoints are always authenticated, so
// the caller gate must fire before status checks and SQL validation, and
// sessions must stay bound to the identity that opened them.

func ownedActiveSource() *model.DataSource {
	return &model.DataSource{
		ID: "ds-1", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive,
		OwnerID: "alice", TenantID: "acme",
	}
}

func fetchRequest(dataSourceID, sql string) *model.FetchQueryRequest {
	return &model.FetchQueryRequest{DataSourceID: dataSourceID, SQL: sql, Type: 1, BatchSize: 10}
}

// A caller without read access is rejected before anything else can leak
// state; the DELETE statement doubles as an ordering probe against SQL
// validation. A tenant member passes the gate and reaches the status check —
// read sharing within a tenant is the documented #9 semantics.
func TestFetchQueryRejectsCallerWithoutAccess(t *testing.T) {
	source := ownedActiveSource()
	source.Status = model.DataSourceStatusInactive
	qs := NewQueryService(federationSourceRepository{source: source}, database.NewConnectionPool(nil, nil, false), false)

	foreign := &security.Caller{UserID: "eve", TenantID: "contoso"}
	_, err := qs.FetchQuery(context.Background(), foreign, fetchRequest("ds-1", "DELETE FROM orders"))
	if !errors.Is(err, security.ErrDataSourceAccessDenied) {
		t.Fatalf("FetchQuery() error = %v, want ErrDataSourceAccessDenied", err)
	}

	tenantless := &security.Caller{UserID: "mallory"}
	if _, err := qs.FetchQuery(context.Background(), tenantless, fetchRequest("ds-1", "DELETE FROM orders")); !errors.Is(err, security.ErrDataSourceAccessDenied) {
		t.Fatalf("FetchQuery() error = %v, want ErrDataSourceAccessDenied before the inactive check", err)
	}

	tenantMember := &security.Caller{UserID: "bob", TenantID: "acme"}
	if _, err := qs.FetchQuery(context.Background(), tenantMember, fetchRequest("ds-1", "DELETE FROM orders")); !errors.Is(err, ErrDataSourceInactive) {
		t.Fatalf("FetchQuery() tenant member error = %v, want ErrDataSourceInactive (gate passed)", err)
	}
}

func TestExecuteStreamQueryRejectsCallerWithoutAccess(t *testing.T) {
	source := ownedActiveSource()
	source.Status = model.DataSourceStatusInactive
	ss := NewStreamingService(federationSourceRepository{source: source}, database.NewConnectionPool(nil, nil, false)).(*streamingService)

	foreign := &security.Caller{UserID: "eve", TenantID: "contoso"}
	_, _, err := ss.ExecuteStreamQuery(context.Background(), foreign, &model.QueryRequest{
		DataSourceID: "ds-1",
		SQL:          "DELETE FROM orders",
	})
	if !errors.Is(err, security.ErrDataSourceAccessDenied) {
		t.Fatalf("ExecuteStreamQuery() error = %v, want ErrDataSourceAccessDenied", err)
	}
}

// Sessions are private to their creator: another user's valid Bearer token
// cannot drain a stream it did not open, and the response matches an unknown
// session so session existence is not disclosed.
func TestFetchNextBatchRejectsForeignCreator(t *testing.T) {
	tests := []struct {
		name            string
		sessionOwner    string
		caller          *security.Caller
		wantUnavailable bool
	}{
		{"foreign creator", "alice", &security.Caller{UserID: "eve", TenantID: "contoso"}, true},
		{"same user id different tenant", "alice", &security.Caller{UserID: "alice", TenantID: "contoso"}, false},
		{"creator continues", "alice", &security.Caller{UserID: "alice"}, false},
		{"legacy session with nil caller", "", nil, false},
		{"named session with nil caller", "alice", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := ownedActiveSource()
			qs := NewQueryService(federationSourceRepository{source: source}, database.NewConnectionPool(nil, nil, false), false).(*queryService)
			qs.sessionManager.Store(&QuerySession{
				QueryID: "q-1", Slug: "slug-1", Token: "token-1", CreatorID: tt.sessionOwner,
				DataSourceID: "ds-1", SQL: "SELECT * FROM orders", Type: 2,
				TotalRows: 10, FetchedRows: 0, ExpiresAt: time.Now().Add(time.Minute),
			})

			_, err := qs.FetchNextBatch(context.Background(), tt.caller, "q-1", "slug-1", "token-1", 10)
			if tt.wantUnavailable && !errors.Is(err, ErrQuerySessionUnavailable) {
				t.Fatalf("FetchNextBatch() error = %v, want ErrQuerySessionUnavailable", err)
			}
			if !tt.wantUnavailable && errors.Is(err, ErrQuerySessionUnavailable) {
				t.Fatalf("FetchNextBatch() rejected the session's own creator: %v", err)
			}
		})
	}
}

// The streaming counterpart uses a session whose data source no longer
// resolves: the creator gate runs before the source lookup, so the creator
// proceeds to the not-found error while a foreign caller is still answered
// with the unknown-session response.
func TestFetchNextBatchWithStreamingRejectsForeignCreator(t *testing.T) {
	source := ownedActiveSource()
	newSession := func() *StreamingSession {
		return &StreamingSession{
			QueryID: "q-1", Slug: "slug-1", Token: "token-1", CreatorID: "alice",
			// federationSourceRepository only resolves the ID it was seeded with
			DataSourceID: "deleted-source", SQL: "SELECT * FROM orders", Type: 2,
			TotalRows: 10, FetchedRows: 0, ExpiresAt: time.Now().Add(time.Minute),
			StreamID: "stream-1",
		}
	}

	t.Run("foreign creator gets the unknown-session response", func(t *testing.T) {
		ss := NewStreamingService(federationSourceRepository{source: source}, database.NewConnectionPool(nil, nil, false)).(*streamingService)
		ss.streamingSessionManager.Store(newSession())

		_, err := ss.FetchNextBatchWithStreaming(context.Background(), &security.Caller{UserID: "eve", TenantID: "contoso"}, "q-1", "slug-1", "token-1", 10)
		if !errors.Is(err, ErrQuerySessionUnavailable) {
			t.Fatalf("FetchNextBatchWithStreaming() error = %v, want ErrQuerySessionUnavailable", err)
		}
	})

	t.Run("creator passes the binding gate", func(t *testing.T) {
		ss := NewStreamingService(federationSourceRepository{source: source}, database.NewConnectionPool(nil, nil, false)).(*streamingService)
		ss.streamingSessionManager.Store(newSession())

		_, err := ss.FetchNextBatchWithStreaming(context.Background(), &security.Caller{UserID: "alice"}, "q-1", "slug-1", "token-1", 10)
		if !errors.Is(err, repository.ErrDataSourceNotFound) {
			t.Fatalf("FetchNextBatchWithStreaming() error = %v, want ErrDataSourceNotFound past the creator gate", err)
		}
	})
}
