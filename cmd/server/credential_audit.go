package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

// auditSourceCredentials reads stored source configurations without migrating
// or writing metadata. Its errors never contain database or credential data.
func auditSourceCredentials(cfg *config.Config, vault *security.CredentialVault, args []string) (int64, error) {
	if cfg == nil || vault == nil || len(args) != 0 {
		return 0, errors.New("source credential audit configuration is invalid")
	}
	// The current backup profile uses MySQL metadata. The SQLite opener can
	// create a missing database file, so it must not be used for this audit.
	driver := strings.ToLower(strings.TrimSpace(cfg.Database.Driver))
	if driver != "" && driver != "mysql" {
		return 0, errors.New("source credential audit requires MySQL metadata")
	}
	// The backup helper snapshots the fixed Compose metadata database. Refuse
	// to audit a different database and then claim that its dump is verified.
	if cfg.Database.Host != "metadata-db" || cfg.Database.Port != "3306" ||
		cfg.Database.Database != "data_fabric" || cfg.Database.Username != "data_fabric" {
		return 0, errors.New("source credential audit requires the reference metadata profile")
	}
	auditCfg := *cfg
	auditCfg.Logging.Level = "error"
	db, err := config.InitDatabase(&auditCfg)
	if err != nil {
		return 0, errors.New("source credential audit could not access metadata")
	}
	defer func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	count, err := repository.AuditStoredDataSourceCredentials(ctx, db, vault)
	if err != nil {
		return 0, errors.New("source credential audit failed; check retained keys and encrypted source settings")
	}
	return count, nil
}
