//go:build datafabric_sqlite && cgo

package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// initSQLiteMetadataDB is an experimental, single-host metadata backend. All
// transaction beginnings use IMMEDIATE to serialize writers across processes;
// callers must still enforce authorization and fail closed on contention.
func initSQLiteMetadataDB(cfg *Config, logMode logger.Interface) (*gorm.DB, error) {
	path := cfg.Database.SQLitePath
	if err := validateSQLiteMetadataPath(path); err != nil {
		return nil, err
	}
	// The parent is a service-controlled local directory, not a shared
	// writable directory where another account could replace the DB or WAL.
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0022 != 0 {
		return nil, errors.New("SQLite metadata parent must exist and not be writable by group or others")
	}
	// A fresh file receives restrictive permissions independently of the process
	// umask. Existing paths must be regular, private files, never symlinks.
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		file, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if createErr != nil && !errors.Is(createErr, os.ErrExist) {
			return nil, fmt.Errorf("cannot create SQLite metadata file: %w", createErr)
		}
		if createErr == nil {
			if err = file.Close(); err != nil {
				return nil, fmt.Errorf("cannot close new SQLite metadata file: %w", err)
			}
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot inspect SQLite metadata file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("SQLite metadata path must be a regular file with owner-only permissions")
	}
	dsn := url.URL{Scheme: "file", Path: path}
	dsn.RawQuery = url.Values{"_busy_timeout": {"5000"}, "_foreign_keys": {"on"}, "_journal_mode": {"WAL"}, "_synchronous": {"FULL"}, "_txlock": {"immediate"}}.Encode()
	db, err := gorm.Open(sqlite.Open(dsn.String()), &gorm.Config{Logger: logMode})
	if err != nil {
		return nil, fmt.Errorf("cannot open SQLite metadata: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("cannot access SQLite metadata connection: %w", err)
	}
	// PRAGMAs apply to every connection through the DSN. A single local
	// connection also avoids a per-process pool defeating transaction ordering.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if err = sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("cannot ping SQLite metadata: %w", err)
	}
	return db, nil
}
