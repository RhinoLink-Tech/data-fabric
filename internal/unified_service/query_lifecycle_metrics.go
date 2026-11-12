package unifiedservice

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/middleware"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
)

const (
	maxTrackedLogicalQueries = 16384
	logicalQueryFallbackTTL  = 30 * time.Minute
)

type logicalQueryLifecycle struct {
	executionMode string
	startedAt     time.Time
	expiresAt     time.Time
	rowsReturned  int64
	batches       int64
	expiryTimer   *time.Timer
}

type logicalQueryTracker struct {
	mu      sync.Mutex
	queries map[string]*logicalQueryLifecycle
}

func newLogicalQueryTracker() *logicalQueryTracker {
	return &logicalQueryTracker{queries: make(map[string]*logicalQueryLifecycle)}
}

type queryLifecycleSessionLookup interface {
	HasLiveQuerySession(queryID string) bool
	QuerySessionExpiry(queryID string) (time.Time, bool)
}

func (uqs *unifiedQueryService) recordInitialLogicalQuery(startedAt time.Time, response *model.FetchQueryResponse, executionMode string, err error) {
	if err != nil {
		middleware.RecordLogicalQueryMetrics(executionMode, initialLogicalQueryOutcome(err), time.Since(startedAt), 0, 0)
		return
	}
	if response == nil {
		middleware.RecordLogicalQueryMetrics(executionMode, "success", time.Since(startedAt), 0, 0)
		return
	}
	rows := int64(len(response.Entries))
	if response.NextURI == "" {
		middleware.RecordLogicalQueryMetrics(executionMode, "success", time.Since(startedAt), rows, 1)
		return
	}
	expiresAt, ok := uqs.querySessionExpiry(executionMode, response.QueryID)
	if !ok {
		expiresAt = time.Now().Add(logicalQueryFallbackTTL)
	}
	uqs.lifecycle.begin(response.QueryID, executionMode, startedAt, expiresAt, rows)
}

func (uqs *unifiedQueryService) hasLiveQuerySession(queryID string) bool {
	if uqs == nil {
		return false
	}
	for _, candidate := range []interface{}{uqs.paginationService, uqs.streamingService} {
		if lookup, ok := candidate.(queryLifecycleSessionLookup); ok && lookup.HasLiveQuerySession(queryID) {
			return true
		}
	}
	return false
}

func (uqs *unifiedQueryService) querySessionExpiry(executionMode, queryID string) (time.Time, bool) {
	if uqs == nil {
		return time.Time{}, false
	}
	var candidates []interface{}
	switch executionMode {
	case executionModeStreaming:
		candidates = []interface{}{uqs.streamingService, uqs.paginationService}
	case executionModePagination:
		candidates = []interface{}{uqs.paginationService, uqs.streamingService}
	default:
		candidates = []interface{}{uqs.streamingService, uqs.paginationService}
	}
	for _, candidate := range candidates {
		if lookup, ok := candidate.(queryLifecycleSessionLookup); ok {
			if expiresAt, found := lookup.QuerySessionExpiry(queryID); found {
				return expiresAt, true
			}
		}
	}
	return time.Time{}, false
}

func initialLogicalQueryOutcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, service.ErrQueryDurationLimitExceeded):
		return "budget_duration"
	case errors.Is(err, service.ErrQueryResultLimitExceeded):
		return "budget_result"
	case errors.Is(err, context.Canceled), service.SafeQueryErrorMessage(err) == "The query was cancelled before completion.":
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded), service.SafeQueryErrorMessage(err) == "The query timed out. Reduce the query scope or increase the request timeout.":
		return "timeout"
	case errors.Is(err, security.ErrDataSourceAccessDenied), errors.Is(err, security.ErrTableAccessDenied),
		errors.Is(err, security.ErrTableAuthorizationUnavailable):
		return "authorization_denied"
	case errors.Is(err, service.ErrQueryAdmissionLimit), errors.Is(err, service.ErrMaxConcurrentStreams),
		errors.Is(err, service.ErrQueryRateLimitExceeded), errors.Is(err, service.ErrQueryOwnerDraining):
		return "admission_rejected"
	case errors.Is(err, service.ErrQuerySessionUnavailable):
		return "owner_unavailable"
	case errors.Is(err, security.ErrNotSelectQuery), errors.Is(err, security.ErrSQLInjection),
		errors.Is(err, security.ErrDangerousKeyword), errors.Is(err, security.ErrSQLSyntaxError),
		errors.Is(err, security.ErrEmptyQuery), errors.Is(err, security.ErrQueryTooLong),
		errors.Is(err, service.ErrDataSourceInactive), errors.Is(err, service.ErrInvalidQuerySession),
		errors.Is(err, service.ErrExactCountUnavailable):
		return "request_error"
	default:
		return "backend_error"
	}
}

func (t *logicalQueryTracker) begin(queryID, executionMode string, startedAt, expiresAt time.Time, rows int64) {
	if t == nil {
		return
	}
	if queryID == "" {
		middleware.RecordLogicalQueryMetrics(executionMode, "tracking_overflow", time.Since(startedAt), rows, 1)
		return
	}
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(logicalQueryFallbackTTL)
	}
	if rows < 0 {
		rows = 0
	}
	state := &logicalQueryLifecycle{
		executionMode: executionMode,
		startedAt:     startedAt,
		expiresAt:     expiresAt,
		rowsReturned:  rows,
		batches:       1,
	}
	t.mu.Lock()
	if t.queries == nil {
		t.queries = make(map[string]*logicalQueryLifecycle)
	}
	if len(t.queries) >= maxTrackedLogicalQueries {
		t.mu.Unlock()
		middleware.RecordLogicalQueryMetrics(executionMode, "tracking_overflow", time.Since(startedAt), rows, 1)
		return
	}
	t.queries[queryID] = state
	delay := time.Until(expiresAt)
	if delay < 0 {
		delay = 0
	}
	state.expiryTimer = time.AfterFunc(delay, func() {
		t.finish(queryID, "session_expired", time.Now())
	})
	t.mu.Unlock()
}

func (t *logicalQueryTracker) recordPage(queryID string, rows int64, hasMore bool, at time.Time) {
	if t == nil || queryID == "" {
		return
	}
	t.mu.Lock()
	state := t.queries[queryID]
	if state == nil {
		t.mu.Unlock()
		return
	}
	if rows > 0 {
		state.rowsReturned += rows
	}
	state.batches++
	if hasMore {
		t.mu.Unlock()
		return
	}
	delete(t.queries, queryID)
	if state.expiryTimer != nil {
		state.expiryTimer.Stop()
	}
	t.mu.Unlock()
	t.recordTerminal(state, "success", at)
}

func (t *logicalQueryTracker) recordContinuationError(queryID string, err error, sessionActive bool, at time.Time) {
	if t == nil || queryID == "" || err == nil {
		return
	}
	if errors.Is(err, service.ErrQueryAdmissionLimit) || errors.Is(err, service.ErrMaxConcurrentStreams) ||
		errors.Is(err, service.ErrQueryRateLimitExceeded) || errors.Is(err, service.ErrQueryOwnerDraining) ||
		errors.Is(err, service.ErrInvalidQuerySession) {
		return
	}
	outcome := ""
	switch {
	case errors.Is(err, service.ErrQueryDurationLimitExceeded):
		outcome = "budget_duration"
	case errors.Is(err, service.ErrQueryResultLimitExceeded):
		outcome = "budget_result"
	case errors.Is(err, security.ErrDataSourceAccessDenied), errors.Is(err, security.ErrTableAccessDenied),
		errors.Is(err, security.ErrTableAuthorizationUnavailable):
		outcome = "authorization_denied"
	case errors.Is(err, context.Canceled):
		if !sessionActive {
			outcome = "canceled"
		}
	case errors.Is(err, context.DeadlineExceeded):
		if !sessionActive {
			outcome = "timeout"
		}
	case errors.Is(err, service.ErrQuerySessionUnavailable):
		if !sessionActive {
			outcome = t.unavailableOutcome(queryID, at)
		}
	default:
		if !sessionActive {
			outcome = "backend_error"
		}
	}
	if outcome != "" {
		t.finish(queryID, outcome, at)
	}
}

func (t *logicalQueryTracker) unavailableOutcome(queryID string, at time.Time) string {
	t.mu.Lock()
	state := t.queries[queryID]
	if state == nil {
		t.mu.Unlock()
		return ""
	}
	expired := !at.Before(state.expiresAt)
	t.mu.Unlock()
	if expired {
		return "session_expired"
	}
	return "owner_unavailable"
}

func (t *logicalQueryTracker) finish(queryID, outcome string, at time.Time) {
	if t == nil || queryID == "" {
		return
	}
	t.mu.Lock()
	state := t.queries[queryID]
	if state == nil {
		t.mu.Unlock()
		return
	}
	delete(t.queries, queryID)
	if state.expiryTimer != nil {
		state.expiryTimer.Stop()
	}
	t.mu.Unlock()
	t.recordTerminal(state, outcome, at)
}

func (t *logicalQueryTracker) recordTerminal(state *logicalQueryLifecycle, outcome string, at time.Time) {
	if at.Before(state.startedAt) {
		at = state.startedAt
	}
	middleware.RecordLogicalQueryMetrics(state.executionMode, outcome, at.Sub(state.startedAt), state.rowsReturned, state.batches)
}
