package service

import (
	"testing"
)

func addHistoryRecords(t *testing.T, history *QueryHistory, user string, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		history.Add(QueryRecord{User: user, DataSourceID: "ds-1", SQL: "SELECT 1"})
	}
}

// A user sees only their own records: other users' queries — and the shared
// anonymous bucket MCP traffic lands in — are never visible (#103).
func TestQueryHistoryScopesRecordsToRequestingUser(t *testing.T) {
	history := NewQueryHistory(100)
	addHistoryRecords(t, history, "alice", 3)
	addHistoryRecords(t, history, "bob", 2)
	history.Add(QueryRecord{DataSourceID: "ds-1", SQL: "SELECT 1"}) // no user -> anonymous

	page := history.List(QueryHistoryFilter{User: "alice"})
	if page.Total != 3 || len(page.Records) != 3 {
		t.Fatalf("alice's page = %d/%d, want 3/3", page.Total, len(page.Records))
	}

	if page := history.List(QueryHistoryFilter{User: "bob"}); page.Total != 2 {
		t.Errorf("bob's total = %d, want 2", page.Total)
	}

	if page := history.List(QueryHistoryFilter{User: "anonymous"}); page.Total != 1 {
		t.Errorf("anonymous total = %d, want 1", page.Total)
	}

	// A filter without a user matches nothing rather than everything.
	if page := history.List(QueryHistoryFilter{}); page.Total != 0 {
		t.Errorf("user-less filter total = %d, want 0", page.Total)
	}
}

// Retention is bounded and newest-first: once capacity is hit, the oldest
// records roll off (#103).
func TestQueryHistoryIsBoundedAndNewestFirst(t *testing.T) {
	history := NewQueryHistory(3)
	for i := 0; i < 5; i++ {
		history.Add(QueryRecord{User: "alice", SQL: string(rune('A' + i))})
	}

	page := history.List(QueryHistoryFilter{User: "alice"})
	if page.Total != 3 {
		t.Fatalf("total = %d, want capacity 3", page.Total)
	}
	want := []string{"E", "D", "C"}
	for i, record := range page.Records {
		if record.SQL != want[i] {
			t.Errorf("record[%d].SQL = %q, want %q (newest first)", i, record.SQL, want[i])
		}
	}
}

// Pagination and data-source filtering apply within the caller's scope.
func TestQueryHistoryPaginationAndFiltering(t *testing.T) {
	history := NewQueryHistory(100)
	for i := 0; i < 4; i++ {
		history.Add(QueryRecord{User: "alice", DataSourceID: "ds-1"})
	}
	for i := 0; i < 2; i++ {
		history.Add(QueryRecord{User: "alice", DataSourceID: "ds-2"})
	}

	filtered := history.List(QueryHistoryFilter{User: "alice", DataSourceID: "ds-2"})
	if filtered.Total != 2 || len(filtered.Records) != 2 {
		t.Fatalf("ds-2 page = %d/%d, want 2/2", filtered.Total, len(filtered.Records))
	}

	page := history.List(QueryHistoryFilter{User: "alice", Limit: 2, Offset: 2})
	if page.Total != 6 || len(page.Records) != 2 || page.Offset != 2 {
		t.Fatalf("paged = total %d, len %d, offset %d; want 6, 2, 2", page.Total, len(page.Records), page.Offset)
	}

	// Out-of-range offsets clamp instead of panicking.
	if page := history.List(QueryHistoryFilter{User: "alice", Offset: 100}); len(page.Records) != 0 || page.Total != 6 {
		t.Errorf("out-of-range offset returned %d records (total %d), want 0 (total 6)", len(page.Records), page.Total)
	}

	// Limit bounds are enforced even for absurd requests.
	if page := history.List(QueryHistoryFilter{User: "alice", Limit: 100000}); page.Limit != 1000 {
		t.Errorf("limit = %d, want clamped to 1000", page.Limit)
	}
	if page := history.List(QueryHistoryFilter{User: "alice", Limit: -5}); page.Limit != 100 {
		t.Errorf("limit = %d, want default 100", page.Limit)
	}
}
