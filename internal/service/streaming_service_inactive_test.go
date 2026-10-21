package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
)

// A data source whose status is not active must be rejected before SQL
// validation or connection setup when opening a stream (#118). The DELETE
// statement doubles as an ordering probe: if the status gate were missing, the
// validator would reject it first with a different error.
func TestExecuteStreamQueryRejectsInactiveDataSource(t *testing.T) {
	source := &model.DataSource{
		ID:     "ds-1",
		Type:   model.DatabaseTypeMySQL,
		Status: model.DataSourceStatusInactive,
	}
	ss := NewStreamingService(federationSourceRepository{source: source}, database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false), newTestSQLTranslationManager()).(*streamingService)

	_, _, err := ss.ExecuteStreamQuery(context.Background(), nil, &model.QueryRequest{
		DataSourceID: "ds-1",
		SQL:          "DELETE FROM orders",
	})
	if !errors.Is(err, ErrDataSourceInactive) {
		t.Fatalf("ExecuteStreamQuery() error = %v, want ErrDataSourceInactive", err)
	}
}

// Disabling a data source after a streaming session was created must stop
// continuation fetches and release the engine cursor (#118).
func TestFetchNextBatchWithStreamingRechecksDataSourceStatus(t *testing.T) {
	source := &model.DataSource{
		ID:     "ds-1",
		Type:   model.DatabaseTypeMySQL,
		Status: model.DataSourceStatusActive,
	}
	ss := NewStreamingService(federationSourceRepository{source: source}, database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false), newTestSQLTranslationManager()).(*streamingService)

	session := &StreamingSession{
		QueryID:      "q-1",
		Slug:         "slug-1",
		Token:        "token-1",
		DataSourceID: "ds-1",
		SQL:          "SELECT * FROM orders",
		Type:         2,
		TotalRows:    10,
		FetchedRows:  0,
		ExpiresAt:    time.Now().Add(time.Minute),
		StreamID:     "stream-1",
	}
	ss.streamingSessionManager.Store(session)

	source.Status = model.DataSourceStatusInactive

	_, err := ss.FetchNextBatchWithStreaming(context.Background(), nil, "q-1", "slug-1", "token-1", 10)
	if !errors.Is(err, ErrDataSourceInactive) {
		t.Fatalf("FetchNextBatchWithStreaming() error = %v, want ErrDataSourceInactive", err)
	}

	// The session cannot continue while its source is disabled, so it is
	// cleaned up instead of lingering until the TTL.
	if _, getErr := ss.streamingSessionManager.Get("q-1"); getErr == nil {
		t.Error("the streaming session is still stored after its data source was disabled")
	}
}

// Deleting a data source between batches surfaces the stable not-found error
// rather than a generic engine failure (#118).
func TestFetchNextBatchWithStreamingReportsDeletedDataSource(t *testing.T) {
	source := &model.DataSource{
		ID:     "ds-1",
		Type:   model.DatabaseTypeMySQL,
		Status: model.DataSourceStatusActive,
	}
	ss := NewStreamingService(federationSourceRepository{source: source}, database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false), newTestSQLTranslationManager()).(*streamingService)

	session := &StreamingSession{
		QueryID: "q-1",
		Slug:    "slug-1",
		Token:   "token-1",
		// federationSourceRepository only resolves the ID it was seeded with
		DataSourceID: "deleted-source",
		SQL:          "SELECT * FROM orders",
		Type:         2,
		TotalRows:    10,
		FetchedRows:  0,
		ExpiresAt:    time.Now().Add(time.Minute),
		StreamID:     "stream-1",
	}
	ss.streamingSessionManager.Store(session)

	_, err := ss.FetchNextBatchWithStreaming(context.Background(), nil, "q-1", "slug-1", "token-1", 10)
	if !errors.Is(err, repository.ErrDataSourceNotFound) {
		t.Fatalf("FetchNextBatchWithStreaming() error = %v, want ErrDataSourceNotFound", err)
	}
}
