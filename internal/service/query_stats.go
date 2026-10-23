package service

import (
	"sync"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// QueryStatsTracker aggregates initial query attempts across execution modes.
// Continuation batches do not represent a new user query and are not recorded.
type QueryStatsTracker struct {
	mutex               sync.RWMutex
	totalQueries        int64
	successfulQueries   int64
	failedQueries       int64
	totalExecutionTime  time.Duration
	lastQueryTime       time.Time
	queriesByType       map[string]int64
	queriesByDataSource map[string]int64
}

func NewQueryStatsTracker() *QueryStatsTracker {
	return &QueryStatsTracker{
		queriesByType:       make(map[string]int64),
		queriesByDataSource: make(map[string]int64),
	}
}

// Record stores the duration of one initial query. A failed request may not
// have a resolved database type, but its submitted data-source ID remains
// useful for diagnostics and is recorded when present.
func (t *QueryStatsTracker) Record(dataSourceID string, databaseType model.DatabaseType, success bool, duration time.Duration) {
	if t == nil {
		return
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()

	t.totalQueries++
	t.totalExecutionTime += duration
	t.lastQueryTime = time.Now()
	if success {
		t.successfulQueries++
	} else {
		t.failedQueries++
	}
	if dataSourceID != "" {
		t.queriesByDataSource[dataSourceID]++
	}
	if databaseType != "" {
		t.queriesByType[string(databaseType)]++
	}
}

// Snapshot returns a detached public view, so API callers cannot mutate the
// tracker's internal aggregation maps.
func (t *QueryStatsTracker) Snapshot() *model.QueryStats {
	if t == nil {
		return &model.QueryStats{
			QueriesByType:       make(map[string]int64),
			QueriesByDataSource: make(map[string]int64),
		}
	}
	t.mutex.RLock()
	defer t.mutex.RUnlock()

	byType := make(map[string]int64, len(t.queriesByType))
	for key, count := range t.queriesByType {
		byType[key] = count
	}
	byDataSource := make(map[string]int64, len(t.queriesByDataSource))
	for key, count := range t.queriesByDataSource {
		byDataSource[key] = count
	}

	averageSeconds := 0.0
	if t.totalQueries > 0 {
		averageSeconds = t.totalExecutionTime.Seconds() / float64(t.totalQueries)
	}
	return &model.QueryStats{
		TotalQueries:        t.totalQueries,
		SuccessfulQueries:   t.successfulQueries,
		FailedQueries:       t.failedQueries,
		AvgExecutionTime:    averageSeconds,
		LastQueryTime:       t.lastQueryTime,
		QueriesByType:       byType,
		QueriesByDataSource: byDataSource,
	}
}
