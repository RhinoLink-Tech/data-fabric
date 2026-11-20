//go:build !datafabric_sqlite || !cgo

package config

import (
	"context"
	"errors"
)

func SnapshotSQLiteMetadata(_ context.Context, _, _ string) error {
	return errors.New("SQLite metadata is unavailable in this build; the experimental datafabric_sqlite tag and CGO are required")
}
