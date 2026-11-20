//go:build datafabric_sqlite && cgo

package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"
)

func TestSQLiteSnapshotOnlineBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.sqlite")
	backupPath := filepath.Join(dir, "backup.sqlite")
	restorePath := filepath.Join(dir, "restore.sqlite")
	db := openMetadata(t, sourcePath)
	defer closeMetadata(t, db)
	if err := repository.MigrateDataSourceSchema(db); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.NewDataSourceRepository(db, fixtureVault(t))
	if err != nil {
		t.Fatal(err)
	}
	svc := service.NewDataSourceService(repo, nil, database.NewHealthChecker(nil))
	caller := &security.Caller{UserID: "alice", TenantID: "team"}
	src, err := svc.CreateDataSource(ctx, caller, &service.CreateDataSourceRequest{Name: "orders", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Host: "source.invalid", Port: 5432, Database: "orders", Username: "reader", Password: "synthetic-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	auth := security.NewTableAuthorizer(policy(src.ID, true))
	manager, err := security.NewTablePolicyManager(db, auth, policy(src.ID, true), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.Publish(ctx, 1, "admin", policy(src.ID, false)); err != nil {
		t.Fatal(err)
	}
	manager.Stop()
	if err = auditlog.Migrate(db); err != nil {
		t.Fatal(err)
	}
	audit, err := auditlog.NewStore(db, "synthetic-key", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = audit.Record(ctx, auditlog.Event{ActorID: "alice", TenantID: "team", Transport: "mcp-http", Operation: "query", Decision: auditlog.DecisionDeny, Outcome: "denied"}); err != nil {
		t.Fatal(err)
	}

	// A writer is active while backup runs. The snapshot must reflect the last
	// committed state, never the uncommitted change.
	writer := openMetadata(t, sourcePath)
	defer closeMetadata(t, writer)
	tx := writer.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	if err = tx.Exec("UPDATE data_sources SET name = ? WHERE id = ?", "uncommitted-name", src.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err = config.SnapshotSQLiteMetadata(ctx, sourcePath, backupPath); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(backupPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("backup mode: %v %v", info, err)
	}
	if err = config.SnapshotSQLiteMetadata(ctx, backupPath, restorePath); err != nil {
		t.Fatal(err)
	}
	restoredDB := openMetadata(t, restorePath)
	defer closeMetadata(t, restoredDB)
	restoredRepo, err := repository.NewDataSourceRepository(restoredDB, fixtureVault(t))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restoredRepo.GetByID(ctx, src.ID)
	if err != nil || restored.Name != "orders" || restored.Config.Password != "synthetic-secret" {
		t.Fatalf("restored source invalid: %v", err)
	}
	restoredAuth := security.NewTableAuthorizer(policy(src.ID, true))
	restoredPolicy, err := security.NewTablePolicyManager(restoredDB, restoredAuth, policy(src.ID, true), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer restoredPolicy.Stop()
	if restoredPolicy.Status().Revision != "2" {
		t.Fatalf("restored revision %s", restoredPolicy.Status().Revision)
	}
	if err = restoredAuth.AuthorizeReferences(ctx, caller, restored, []sqlanalysis.TableReference{{Schema: "public", Table: "orders"}}, security.TablePermissionRead); !errors.Is(err, security.ErrTableAccessDenied) {
		t.Fatalf("revocation lost: %v", err)
	}
	restoredAudit, err := auditlog.NewStore(restoredDB, "synthetic-key", 1)
	if err != nil {
		t.Fatal(err)
	}
	events, err := restoredAudit.List(ctx, auditlog.Filter{TenantID: "team", Limit: 10})
	if err != nil || events.Total != 1 {
		t.Fatalf("audit events not restored: %v", err)
	}
}

func TestSQLiteSnapshotRejectsInvalidOrExistingPaths(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.sqlite")
	db := openMetadata(t, source)
	defer closeMetadata(t, db)
	if err := repository.MigrateDataSourceSchema(db); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(dir, "invalid.sqlite")
	if err := os.WriteFile(invalid, []byte("not sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "existing.sqlite")
	if err := os.WriteFile(existing, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	withJournal := filepath.Join(dir, "with-journal.sqlite")
	if err := os.WriteFile(withJournal+"-wal", []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.sqlite")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ src, dst string }{
		{filepath.Join(dir, "missing.sqlite"), filepath.Join(dir, "a.sqlite")},
		{invalid, filepath.Join(dir, "b.sqlite")},
		{link, filepath.Join(dir, "c.sqlite")},
		{source, existing},
		{source, withJournal},
		{source, source},
		{source, "relative.sqlite"},
	} {
		if err := config.SnapshotSQLiteMetadata(ctx, tc.src, tc.dst); err == nil {
			t.Fatalf("accepted %q -> %q", tc.src, tc.dst)
		}
	}
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing file changed: %v", err)
	}
}
