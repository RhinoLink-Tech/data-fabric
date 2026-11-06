package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	defaultMaxQueryResultRows  int64 = 100000
	defaultMaxQueryResultBytes int64 = 64 << 20
	defaultMaxQueryDuration          = 5 * time.Minute
)

// QueryBudgetLimits bounds the cumulative result returned by one logical query.
type QueryBudgetLimits struct {
	MaxRows     int64
	MaxBytes    int64
	MaxDuration time.Duration
}

func DefaultQueryBudgetLimits() QueryBudgetLimits {
	return QueryBudgetLimits{
		MaxRows:     defaultMaxQueryResultRows,
		MaxBytes:    defaultMaxQueryResultBytes,
		MaxDuration: defaultMaxQueryDuration,
	}
}

func (limits QueryBudgetLimits) normalized() QueryBudgetLimits {
	defaults := DefaultQueryBudgetLimits()
	if limits.MaxRows <= 0 {
		limits.MaxRows = defaults.MaxRows
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = defaults.MaxBytes
	}
	if limits.MaxDuration <= 0 {
		limits.MaxDuration = defaults.MaxDuration
	}
	return limits
}

type queryBudgetState struct {
	limits   QueryBudgetLimits
	rows     int64
	bytes    int64
	deadline time.Time
}

func newQueryBudgetState(started time.Time, limits QueryBudgetLimits) queryBudgetState {
	if started.IsZero() {
		started = time.Now()
	}
	limits = limits.normalized()
	return queryBudgetState{
		limits:   limits,
		deadline: started.Add(limits.MaxDuration),
	}
}

func (budget queryBudgetState) withDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if budget.deadline.IsZero() {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, budget.deadline)
}

func (budget queryBudgetState) expired(now time.Time) bool {
	return !budget.deadline.IsZero() && !now.Before(budget.deadline)
}

func (budget *queryBudgetState) ensure(started time.Time, limits QueryBudgetLimits, fetchedRows int64) {
	if !budget.deadline.IsZero() {
		return
	}
	rows, bytes := budget.rows, budget.bytes
	*budget = newQueryBudgetState(started, limits)
	if rows == 0 {
		rows = fetchedRows
	}
	budget.rows = rows
	budget.bytes = bytes
}

func (qs *queryService) discardBudgetSession(session *QuerySession) {
	if session == nil {
		return
	}
	if qs.sessionManager != nil {
		qs.sessionManager.Delete(session.QueryID)
	} else {
		closeQueryCursor(session)
	}
	qs.stats.RecordBudgetExceeded()
}

func (ss *streamingService) discardBudgetSession(session *StreamingSession) {
	if session == nil {
		return
	}
	ss.invalidateSessionStream(session)
	ss.stats.RecordBudgetExceeded()
}

func (budget *queryBudgetState) accountPage(columns []model.ColumnInfo, entries []*[]interface{}) error {
	payload, err := json.Marshal(struct {
		Columns []model.ColumnInfo `json:"columns"`
		Entries []*[]interface{}   `json:"entries"`
	}{
		Columns: columns,
		Entries: entries,
	})
	if err != nil {
		return err
	}

	nextRows := budget.rows + int64(len(entries))
	nextBytes := budget.bytes + int64(len(payload))
	if nextRows > budget.limits.MaxRows || nextBytes > budget.limits.MaxBytes {
		return ErrQueryResultLimitExceeded
	}

	budget.rows = nextRows
	budget.bytes = nextBytes
	return nil
}
