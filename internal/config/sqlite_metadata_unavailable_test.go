//go:build !datafabric_sqlite || !cgo

package config

import (
	"strings"
	"testing"
)

func TestSQLiteMetadataRequiresOptInBuild(t *testing.T) {
	_, err := InitDatabase(&Config{Database: DatabaseConfig{Driver: "sqlite", SQLitePath: "/tmp/test-unused.sqlite"}})
	if err == nil || !strings.Contains(err.Error(), "unavailable in this build") {
		t.Fatalf("expected explicit unavailable error, got %v", err)
	}
}
