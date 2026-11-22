package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

func TestCredentialAuditRefusesNonReferenceOrSQLiteMetadata(t *testing.T) {
	vault, err := security.NewCredentialVault([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Database = config.DatabaseConfig{Driver: "sqlite", SQLitePath: t.TempDir() + "/must-not-create.db"}
	if _, err := auditSourceCredentials(cfg, vault, nil); err == nil {
		t.Fatal("SQLite metadata was accepted")
	}
	if _, err := os.Stat(cfg.Database.SQLitePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("SQLite audit created or modified a metadata path")
	}
	cfg.Database = config.DatabaseConfig{Driver: "mysql", Host: "wrong-host", Port: "3306", Database: "data_fabric", Username: "data_fabric"}
	if _, err := auditSourceCredentials(cfg, vault, nil); err == nil {
		t.Fatal("non-reference metadata was accepted")
	}
	if _, err := auditSourceCredentials(cfg, vault, []string{"unexpected"}); err == nil {
		t.Fatal("unexpected arguments were accepted")
	}
}
