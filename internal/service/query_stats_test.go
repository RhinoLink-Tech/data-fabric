package service

import (
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestQueryStatsTrackerAggregatesDurationAndDimensions(t *testing.T) {
	tracker := NewQueryStatsTracker()
	tracker.Record("source-postgres", model.DatabaseTypePostgreSQL, true, 1500*time.Millisecond)
	tracker.Record("source-postgres", model.DatabaseTypePostgreSQL, false, 500*time.Millisecond)
	tracker.Record("source-mysql", model.DatabaseTypeMySQL, true, time.Second)

	stats := tracker.Snapshot()
	if stats.TotalQueries != 3 || stats.SuccessfulQueries != 2 || stats.FailedQueries != 1 {
		t.Fatalf("unexpected counters: %+v", stats)
	}
	if stats.AvgExecutionTime != 1 {
		t.Fatalf("average execution time = %v, want 1 second", stats.AvgExecutionTime)
	}
	if stats.QueriesByType[string(model.DatabaseTypePostgreSQL)] != 2 || stats.QueriesByType[string(model.DatabaseTypeMySQL)] != 1 {
		t.Fatalf("unexpected database-type aggregation: %#v", stats.QueriesByType)
	}
	if stats.QueriesByDataSource["source-postgres"] != 2 || stats.QueriesByDataSource["source-mysql"] != 1 {
		t.Fatalf("unexpected data-source aggregation: %#v", stats.QueriesByDataSource)
	}

	stats.QueriesByType["mutated"] = 99
	stats.QueriesByDataSource["mutated"] = 99
	fresh := tracker.Snapshot()
	if fresh.QueriesByType["mutated"] != 0 || fresh.QueriesByDataSource["mutated"] != 0 {
		t.Fatalf("snapshot maps must not mutate tracker state: %+v", fresh)
	}
}

func TestQueryServicesShareSuppliedStatsTracker(t *testing.T) {
	tracker := NewQueryStatsTracker()
	pagination := NewQueryServiceWithTableAuthorizerAndStats(nil, nil, false, nil, tracker).(*queryService)
	streaming := NewStreamingServiceWithTableAuthorizerAndStats(nil, nil, nil, tracker).(*streamingService)

	if pagination.stats != tracker || streaming.stats != tracker {
		t.Fatal("pagination and streaming services must share the supplied statistics tracker")
	}
}
