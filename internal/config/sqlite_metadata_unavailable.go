//go:build !datafabric_sqlite || !cgo

package config

import (
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// MySQL-only builds reject the opt-in backend rather than silently opening a
// driver stub or falling back to a different store.
func initSQLiteMetadataDB(_ *Config, _ logger.Interface) (*gorm.DB, error) {
	return nil, errors.New("SQLite metadata is unavailable in this build; the experimental datafabric_sqlite tag and CGO are required")
}
