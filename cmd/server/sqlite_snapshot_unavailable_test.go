//go:build !datafabric_sqlite || !cgo

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteSnapshotUnavailableInStandardBuild(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.sqlite")
	output := filepath.Join(dir, "backup.sqlite")
	if err := runSQLiteSnapshot([]string{"backup", "--source", source, "--output", output}); err == nil || !strings.Contains(err.Error(), "unavailable in this build") {
		t.Fatalf("standard build should reject SQLite snapshots, got: %v", err)
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("unexpected snapshot file: %v", err)
	}
}
