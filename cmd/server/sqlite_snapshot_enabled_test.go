//go:build datafabric_sqlite && cgo

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
)

func TestSQLiteSnapshotCLIBackupRestore(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.sqlite")
	backup := filepath.Join(dir, "backup.sqlite")
	restored := filepath.Join(dir, "restored.sqlite")
	db, err := config.InitDatabase(&config.Config{Database: config.DatabaseConfig{Driver: "sqlite", SQLitePath: source}})
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.MigrateDataSourceSchema(db); err != nil {
		t.Fatal(err)
	}
	raw, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if err = runSQLiteSnapshot([]string{"backup", "--source", source, "--output", backup}); err != nil {
		t.Fatal(err)
	}
	if err = runSQLiteSnapshot([]string{"restore", "--source", backup, "--output", restored}); err != nil {
		t.Fatal(err)
	}
	if err = runSQLiteSnapshot([]string{"restore", "--source", backup, "--output", restored}); err == nil {
		t.Fatal("overwrote restore target")
	}
	info, err := os.Stat(restored)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("restore file permissions: %v %v", info, err)
	}
}
