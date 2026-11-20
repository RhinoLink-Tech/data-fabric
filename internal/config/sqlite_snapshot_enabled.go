//go:build datafabric_sqlite && cgo

package config

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// SnapshotSQLiteMetadata creates a consistent SQLite copy, including committed
// WAL content, without replacing an existing file. It also serves as the
// restore primitive: restore only into a new, unused path with services stopped.
func SnapshotSQLiteMetadata(ctx context.Context, source, destination string) error {
	for _, path := range []string{source, destination} {
		if err := validateSQLiteMetadataPath(path); err != nil {
			return err
		}
		parent, err := os.Lstat(filepath.Dir(path))
		if err != nil || !parent.IsDir() || parent.Mode().Perm()&0022 != 0 {
			return errors.New("SQLite snapshot directories must exist and not be writable by group or others")
		}
	}
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("SQLite snapshot source must be an existing owner-only regular file")
	}
	// Existing destinations, including stale journals, must never be reused.
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_, err := os.Lstat(destination + suffix)
		if err == nil || !errors.Is(err, os.ErrNotExist) {
			return errors.New("SQLite snapshot destination and its journal files must not exist")
		}
	}
	dsn := url.URL{Scheme: "file", Path: source}
	dsn.RawQuery = url.Values{"mode": {"ro"}, "_busy_timeout": {"5000"}}.Encode()
	db, err := sql.Open("sqlite3", dsn.String())
	if err != nil {
		return fmt.Errorf("open SQLite snapshot source: %w", err)
	}
	defer db.Close()
	var check string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		return errors.New("SQLite snapshot source failed integrity check")
	}
	// Reject a valid but unrelated or uninitialized SQLite database.
	var found int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'data_sources'").Scan(&found); err != nil || found != 1 {
		return errors.New("SQLite snapshot source does not contain Data Fabric metadata")
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".data-fabric-snapshot-*")
	if err != nil {
		return fmt.Errorf("create private SQLite snapshot: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close private SQLite snapshot: %w", err)
	}
	// VACUUM INTO reads a transactionally consistent view, including WAL, and
	// never overwrites the existing empty temporary file.
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", tmpPath); err != nil {
		return fmt.Errorf("create SQLite snapshot: %w", err)
	}
	snapshot, err := os.OpenFile(tmpPath, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open SQLite snapshot: %w", err)
	}
	err = snapshot.Sync()
	closeErr := snapshot.Close()
	if err != nil {
		return fmt.Errorf("sync SQLite snapshot: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close SQLite snapshot: %w", closeErr)
	}
	// Link is atomic and fails if a competing process creates the destination.
	if err := os.Link(tmpPath, destination); err != nil {
		return fmt.Errorf("publish SQLite snapshot without overwrite: %w", err)
	}
	// Persist the directory entry as well as the file contents. If syncing
	// fails, leave the published file in place and report the uncertain result.
	dir, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return fmt.Errorf("open SQLite snapshot directory after publication: %w", err)
	}
	err = dir.Sync()
	closeErr = dir.Close()
	if err != nil {
		return fmt.Errorf("sync SQLite snapshot directory after publication: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close SQLite snapshot directory after publication: %w", closeErr)
	}
	return nil
}
