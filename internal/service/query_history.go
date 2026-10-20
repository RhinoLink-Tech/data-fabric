package service

import (
	"context"
	"sync"
	"time"
)

// QueryRecord is an internally retained fetch query. Its SQL remains available
// to internal history consumers but is omitted from serialized API responses.
type QueryRecord struct {
	QueryID      string    `json:"queryId"`
	DataSourceID string    `json:"dataSourceId"`
	SQL          string    `json:"-"`
	Status       string    `json:"status"` // "success" or "error"
	DurationMs   int64     `json:"durationMs"`
	User         string    `json:"user"`
	ExecutedAt   time.Time `json:"executedAt"`
}

// QueryHistoryFilter scopes a history listing to one user (#103: a caller
// must never see another tenant's queries) with optional data-source and
// pagination constraints.
type QueryHistoryFilter struct {
	User         string
	DataSourceID string
	Limit        int
	Offset       int
}

// QueryHistoryPage is one bounded page of history plus the filtered total.
type QueryHistoryPage struct {
	Records []QueryRecord `json:"queries"`
	Total   int           `json:"total"`
	Limit   int           `json:"limit"`
	Offset  int           `json:"offset"`
}

// QueryHistory is a process-local, bounded, most-recent-first query history.
// It follows the same in-memory, bounded-retention pattern as the metrics
// collector: entries survive until the capacity is rolled over or the process
// restarts. Durable cross-restart storage is deliberately out of scope here;
// the endpoint contract (shape, scoping, pagination) is what stays stable.
type QueryHistory struct {
	mutex    sync.RWMutex
	records  []QueryRecord // newest first
	capacity int
}

// NewQueryHistory creates a history retaining at most capacity records.
// capacity <= 0 falls back to a sane default.
func NewQueryHistory(capacity int) *QueryHistory {
	if capacity <= 0 {
		capacity = 1000
	}
	return &QueryHistory{
		records:  make([]QueryRecord, 0, capacity),
		capacity: capacity,
	}
}

// Add records one query execution. An empty user is normalized to "anonymous"
// (MCP stdio traffic carries no JWT identity) so every record belongs to a
// scoping bucket and is never handed to an unrelated HTTP caller.
func (h *QueryHistory) Add(record QueryRecord) {
	if record.User == "" {
		record.User = "anonymous"
	}
	if record.ExecutedAt.IsZero() {
		record.ExecutedAt = time.Now()
	}

	h.mutex.Lock()
	defer h.mutex.Unlock()

	h.records = append([]QueryRecord{record}, h.records...)
	if len(h.records) > h.capacity {
		h.records = h.records[:h.capacity]
	}
}

// List returns the bounded page of records visible to filter.User. A filter
// without a user matches nothing: callers must identify themselves (#103).
func (h *QueryHistory) List(filter QueryHistoryFilter) *QueryHistoryPage {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	h.mutex.RLock()
	defer h.mutex.RUnlock()

	visible := make([]QueryRecord, 0, len(h.records))
	for _, record := range h.records {
		if filter.User == "" || record.User != filter.User {
			continue
		}
		if filter.DataSourceID != "" && record.DataSourceID != filter.DataSourceID {
			continue
		}
		visible = append(visible, record)
	}

	total := len(visible)
	end := offset + limit
	if offset > total {
		offset = total
	}
	if end > total {
		end = total
	}

	return &QueryHistoryPage{
		Records: visible[offset:end],
		Total:   total,
		Limit:   limit,
		Offset:  offset,
	}
}

// queryUserKey carries the authenticated identity from the HTTP or MCP
// boundary into the service layer, where records are scoped by it (#103).
type queryUserKey struct{}

// WithQueryUser attaches the executing user to a context.
func WithQueryUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, queryUserKey{}, user)
}

// QueryUserFromContext extracts the executing user, or "" when the caller
// carries no identity (for example MCP stdio tools).
func QueryUserFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	user, _ := ctx.Value(queryUserKey{}).(string)
	return user
}
