package table_formats

import (
	"testing"
	"time"
)

// Delta commit history is sourced from "DESCRIBE HISTORY", which Databricks
// returns newest-first. The index-based helpers must honour that ordering
// rather than assuming ascending version order.
func TestDeltaGetLatestVersionDescendingHistory(t *testing.T) {
	parser := &DeltaLogParser{}
	commits := []DeltaCommitInfo{
		{Version: 9, Timestamp: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)},
		{Version: 4, Timestamp: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
		{Version: 1, Timestamp: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
	}

	latest, err := parser.GetLatestVersion(commits)
	if err != nil {
		t.Fatalf("GetLatestVersion returned error: %v", err)
	}
	// Feed is newest-first, so the latest version is the FIRST element (v9),
	// matching the version the engine reports for DESCRIBE HISTORY.
	if latest != 9 {
		t.Fatalf("GetLatestVersion = %d, want 9 (newest-first history)", latest)
	}
}

func TestDeltaGetLatestVersionEmpty(t *testing.T) {
	parser := &DeltaLogParser{}
	if _, err := parser.GetLatestVersion(nil); err == nil {
		t.Fatal("expected error for empty commits")
	}
}
