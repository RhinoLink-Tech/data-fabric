package main

import "testing"

func TestSQLiteSnapshotArgumentsRejectInvalidCalls(t *testing.T) {
	for _, args := range [][]string{nil, {"other"}, {"backup"}, {"restore", "--source", "only"}, {"backup", "--source", "/a", "--output", "/b", "unexpected"}} {
		if err := runSQLiteSnapshot(args); err == nil {
			t.Fatalf("accepted invalid arguments: %q", args)
		}
	}
}
