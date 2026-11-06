package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/replicarouting"
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
	qs := NewQueryService(federationSourceRepository{source: source}, database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false), false, newTestSQLTranslationManager())

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
	ss := NewStreamingService(federationSourceRepository{source: source}, database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false), newTestSQLTranslationManager()).(*streamingService)

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
		{"same user id different tenant", "alice", &security.Caller{UserID: "alice", TenantID: "contoso"}, true},
		{"creator continues", "alice", &security.Caller{UserID: "alice"}, false},
		{"legacy session with nil caller", "", nil, false},
		{"named session with nil caller", "alice", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := ownedActiveSource()
			qs := NewQueryService(federationSourceRepository{source: source}, database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false), false, newTestSQLTranslationManager()).(*queryService)
			qs.sessionManager.Store(&QuerySession{
				QueryID: "q-1", Slug: "slug-1", Token: "token-1", creator: querySessionPrincipal{userID: tt.sessionOwner},
				DataSourceID: "ds-1", SQL: "SELECT * FROM orders", Type: 2,
				TotalRows: 10, FetchedRows: 10, ExpiresAt: time.Now().Add(time.Minute),
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
			QueryID: "q-1", Slug: "slug-1", Token: "token-1", creator: querySessionPrincipal{userID: "alice"},
			// federationSourceRepository only resolves the ID it was seeded with
			DataSourceID: "deleted-source", SQL: "SELECT * FROM orders", Type: 2,
			TotalRows: 10, FetchedRows: 0, ExpiresAt: time.Now().Add(time.Minute),
			StreamID: "stream-1",
		}
	}

	t.Run("foreign creator gets the unknown-session response", func(t *testing.T) {
		ss := NewStreamingService(federationSourceRepository{source: source}, database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false), newTestSQLTranslationManager()).(*streamingService)
		ss.streamingSessionManager.Store(newSession())

		_, err := ss.FetchNextBatchWithStreaming(context.Background(), &security.Caller{UserID: "eve", TenantID: "contoso"}, "q-1", "slug-1", "token-1", 10)
		if !errors.Is(err, ErrQuerySessionUnavailable) {
			t.Fatalf("FetchNextBatchWithStreaming() error = %v, want ErrQuerySessionUnavailable", err)
		}
	})

	t.Run("same user with another tenant cannot use a session", func(t *testing.T) {
		ss := NewStreamingService(federationSourceRepository{source: source}, nil, newTestSQLTranslationManager()).(*streamingService)
		session := newSession()
		session.creator.tenantID = "acme"
		session.DataSourceID = source.ID
		ss.streamingSessionManager.Store(session)
		caller := &security.Caller{UserID: "alice", TenantID: "contoso"}
		if !caller.CanRead(source) {
			t.Fatal("fixture should retain the data-source owner's cross-claim access")
		}
		_, err := ss.FetchNextBatchWithStreaming(context.Background(), caller, "q-1", "slug-1", "token-1", 10)
		if !errors.Is(err, ErrQuerySessionUnavailable) || !ss.streamingSessionManager.isCurrent(session) {
			t.Fatalf("foreign tenant advanced or removed the session: %v", err)
		}
	})

	t.Run("creator passes the binding gate", func(t *testing.T) {
		ss := NewStreamingService(federationSourceRepository{source: source}, database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false), newTestSQLTranslationManager()).(*streamingService)
		ss.streamingSessionManager.Store(newSession())

		_, err := ss.FetchNextBatchWithStreaming(context.Background(), &security.Caller{UserID: "alice"}, "q-1", "slug-1", "token-1", 10)
		if !errors.Is(err, repository.ErrDataSourceNotFound) {
			t.Fatalf("FetchNextBatchWithStreaming() error = %v, want ErrDataSourceNotFound past the creator gate", err)
		}
	})

	t.Run("creator who lost tenant access is denied", func(t *testing.T) {
		tenantSource := &model.DataSource{
			ID: "ds-1", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive,
			OwnerID: "bob", TenantID: "acme",
		}
		ss := NewStreamingService(federationSourceRepository{source: tenantSource}, database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false), newTestSQLTranslationManager()).(*streamingService)
		session := newSession()
		session.DataSourceID = "ds-1"
		session.creator.tenantID = "contoso"
		ss.streamingSessionManager.Store(session)

		caller := &security.Caller{UserID: "alice", TenantID: "contoso"}
		_, err := ss.FetchNextBatchWithStreaming(context.Background(), caller, "q-1", "slug-1", "token-1", 10)
		if !errors.Is(err, security.ErrDataSourceAccessDenied) {
			t.Fatalf("FetchNextBatchWithStreaming() error = %v, want ErrDataSourceAccessDenied", err)
		}
		if _, err := ss.streamingSessionManager.Get("q-1"); err == nil {
			t.Fatal("unauthorized streaming session was not cleaned up")
		}
	})
}

func TestFetchNextBatchRechecksCurrentTenantAccess(t *testing.T) {
	source := &model.DataSource{
		ID: "ds-1", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive,
		OwnerID: "bob", TenantID: "acme",
	}
	qs := NewQueryService(federationSourceRepository{source: source}, database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false), false, newTestSQLTranslationManager()).(*queryService)
	qs.sessionManager.Store(&QuerySession{
		QueryID: "q-tenant", Slug: "slug", Token: "token", creator: querySessionPrincipal{userID: "alice", tenantID: "contoso"},
		DataSourceID: "ds-1", SQL: "SELECT * FROM orders", Type: 2,
		TotalRows: 10, ExpiresAt: time.Now().Add(time.Minute),
	})

	caller := &security.Caller{UserID: "alice", TenantID: "contoso"}
	_, err := qs.FetchNextBatch(context.Background(), caller, "q-tenant", "slug", "token", 10)
	if !errors.Is(err, security.ErrDataSourceAccessDenied) {
		t.Fatalf("FetchNextBatch() error = %v, want ErrDataSourceAccessDenied", err)
	}
}

func TestQuerySessionRejectsAnotherMCPTransportForTheSameCaller(t *testing.T) {
	caller := &security.Caller{UserID: "alice", TenantID: "team-a"}
	creatorContext := replicarouting.WithTransportSession(
		context.Background(), "owner/session-a", time.Now().Add(time.Minute),
	)
	service, ok := NewQueryServiceWithTableAuthorizer(nil, nil, false, nil).(*queryService)
	if !ok {
		t.Fatal("unexpected query service implementation")
	}
	session := &QuerySession{
		QueryID: "query-one",
		Slug:    "signed-query-marker",
		Token:   "valid-current-token",
		ExpiresAt: time.Now().Add(time.Minute),
		creator: querySessionPrincipalFromContext(creatorContext, caller),
	}
	if !service.sessionManager.Store(session) {
		t.Fatal("failed to store test session")
	}

	otherTransport := replicarouting.WithTransportSession(
		context.Background(), "owner/session-b", time.Now().Add(time.Minute),
	)
	_, err := service.FetchNextBatch(otherTransport, caller, session.QueryID, session.Slug, session.Token, 10)
	if !errors.Is(err, ErrQuerySessionUnavailable) {
		t.Fatalf("FetchNextBatch() error = %v, want unavailable for a different MCP session", err)
	}
	if got := service.LiveSessionCount(time.Now()); got != 1 {
		t.Fatalf("session count after unauthorized fetch = %d, want 1", got)
	}
}

func TestEndTransportSessionClosesBoundQueryCursor(t *testing.T) {
	service, ok := NewQueryService(nil, nil, false).(*queryService)
	if !ok {
		t.Fatal("unexpected query service implementation")
	}
	var closes atomic.Int32
	session := &QuerySession{
		QueryID:   "query-one",
		creator:   querySessionPrincipal{transportSessionID: "owner/session-a"},
		Cursor:    closingQueryCursor{closes: &closes},
		ExpiresAt: time.Now().Add(time.Minute),
	}
	if !service.sessionManager.Store(session) {
		t.Fatal("failed to store test session")
	}
	service.EndTransportSession("owner/session-a")
	if got := service.LiveSessionCount(time.Now()); got != 0 {
		t.Fatalf("live session count = %d, want 0", got)
	}
	if closes.Load() != 1 {
		t.Fatalf("cursor Close calls = %d, want 1", closes.Load())
	}
}

type closingQueryCursor struct {
	closes *atomic.Int32
}

func (c closingQueryCursor) Close() error {
	c.closes.Add(1)
	return nil
}
