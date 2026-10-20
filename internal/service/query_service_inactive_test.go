package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// A data source whose status is not active must be rejected before SQL
// validation or connection setup, on both new queries and continuation
// fetches (#118). The DELETE statement doubles as an ordering probe: if the
// status gate were missing, the validator would reject it first with a
// different error.
func TestFetchQueryRejectsInactiveDataSource(t *testing.T) {
	tests := []struct {
		name   string
		status model.DataSourceStatus
	}{
		{"inactive", model.DataSourceStatusInactive},
		{"error", model.DataSourceStatusError},
		// An unexpected empty status fails closed rather than defaulting to active
		{"empty fails closed", model.DataSourceStatus("")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &model.DataSource{
				ID:     "ds-1",
				Type:   model.DatabaseTypeMySQL,
				Status: tt.status,
			}
			qs := NewQueryService(federationSourceRepository{source: source}, database.NewConnectionPool(nil, nil, false), false)

			_, err := qs.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
				DataSourceID: "ds-1",
				SQL:          "DELETE FROM orders",
				Type:         1,
				BatchSize:    10,
			})
			if !errors.Is(err, ErrDataSourceInactive) {
				t.Fatalf("FetchQuery() error = %v, want ErrDataSourceInactive", err)
			}
		})
	}
}

// An active data source passes the gate and proceeds to normal validation.
func TestFetchQueryAllowsActiveDataSource(t *testing.T) {
	source := &model.DataSource{
		ID:     "ds-1",
		Type:   model.DatabaseTypeMySQL,
		Status: model.DataSourceStatusActive,
	}
	qs := NewQueryService(federationSourceRepository{source: source}, database.NewConnectionPool(nil, nil, false), false)

	_, err := qs.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: "ds-1",
		SQL:          "SELECT * FROM orders",
		Type:         1,
		BatchSize:    10,
	})
	if errors.Is(err, ErrDataSourceInactive) {
		t.Fatalf("FetchQuery() rejected an active data source: %v", err)
	}
}

// Disabling a data source after a session was created must stop continuation
// fetches, not just new queries (#118).
func TestFetchNextBatchRechecksDataSourceStatus(t *testing.T) {
	source := &model.DataSource{
		ID:     "ds-1",
		Type:   model.DatabaseTypeMySQL,
		Status: model.DataSourceStatusActive,
	}
	qs := NewQueryService(federationSourceRepository{source: source}, database.NewConnectionPool(nil, nil, false), false).(*queryService)

	session := &QuerySession{
		QueryID:      "q-1",
		Slug:         "slug-1",
		Token:        "token-1",
		DataSourceID: "ds-1",
		SQL:          "SELECT * FROM orders",
		Type:         2,
		TotalRows:    10,
		FetchedRows:  0,
		ExpiresAt:    time.Now().Add(time.Minute),
	}
	qs.sessionManager.Store(session)

	source.Status = model.DataSourceStatusInactive

	_, err := qs.FetchNextBatch(context.Background(), nil, "q-1", "slug-1", "token-1", 10)
	if !errors.Is(err, ErrDataSourceInactive) {
		t.Fatalf("FetchNextBatch() error = %v, want ErrDataSourceInactive", err)
	}
}
