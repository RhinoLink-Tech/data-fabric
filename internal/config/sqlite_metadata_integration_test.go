//go:build datafabric_sqlite && cgo

package config_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"
	"gorm.io/gorm"
)

func openMetadata(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := config.InitDatabase(&config.Config{Database: config.DatabaseConfig{Driver: "sqlite", SQLitePath: path}})
	if err != nil {
		t.Fatal(err)
	}
	return db
}
func closeMetadata(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err = sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
}
func fixtureVault(t *testing.T) *security.CredentialVault {
	t.Helper()
	v, e := security.NewCredentialVaultWithKeyring("test", map[string][]byte{"test": []byte(strings.Repeat("k", 32))})
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func policy(sourceID string, allow bool) security.TableAuthorizationConfig {
	cfg := security.TableAuthorizationConfig{Enabled: true, DataSources: map[string]security.DataSourceTablePolicy{}}
	if allow {
		cfg.DataSources[sourceID] = security.DataSourceTablePolicy{Rules: []security.TableAccessRule{{UserID: "alice", TenantID: "team", Read: []security.TableScope{{Schema: "public", Table: "orders"}}}}}
	}
	return cfg
}
func TestSQLiteMetadataSourcePolicyAuditSurviveRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "metadata.sqlite")
	db := openMetadata(t, path)
	if err := repository.MigrateDataSourceSchema(db); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.NewDataSourceRepository(db, fixtureVault(t))
	if err != nil {
		t.Fatal(err)
	}
	svc := service.NewDataSourceService(repo, nil, database.NewHealthChecker(nil))
	caller := &security.Caller{UserID: "alice", TenantID: "team"}
	source, err := svc.CreateDataSource(ctx, caller, &service.CreateDataSourceRequest{Name: "orders", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Host: "source.invalid", Port: 5432, Database: "orders", Username: "reader", Password: "synthetic-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("UPDATE data_sources SET status = ? WHERE id = ?", "unrecognized-status", source.ID).Error; err == nil {
		t.Fatal("SQLite accepted an invalid status")
	}
	if err = db.Exec("UPDATE data_sources SET type = ? WHERE id = ?", "unrecognized-type", source.ID).Error; err == nil {
		t.Fatal("SQLite accepted an invalid source type")
	}
	if err = repository.MigrateDataSourceSchema(db); err != nil {
		t.Fatalf("repeat data-source migration: %v", err)
	}
	var stored string
	if err = db.Raw("SELECT config FROM data_sources WHERE id = ?", source.ID).Scan(&stored).Error; err != nil || strings.Contains(stored, "synthetic-secret") {
		t.Fatalf("stored config leaked or unavailable: %v", err)
	}
	auth := security.NewTableAuthorizer(policy(source.ID, true))
	manager, err := security.NewTablePolicyManager(db, auth, policy(source.ID, true), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	ref := []sqlanalysis.TableReference{{Schema: "public", Table: "orders"}}
	if err = auth.AuthorizeReferences(ctx, caller, source, ref, security.TablePermissionRead); err != nil {
		t.Fatalf("initial grant: %v", err)
	}
	if _, err = manager.Publish(ctx, 1, "admin", policy(source.ID, false)); err != nil {
		t.Fatal(err)
	}
	if err = auth.AuthorizeReferences(ctx, caller, source, ref, security.TablePermissionRead); !errors.Is(err, security.ErrTableAccessDenied) {
		t.Fatalf("revocation not enforced: %v", err)
	}
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
	manager.Stop()
	closeMetadata(t, db)
	db = openMetadata(t, path)
	defer closeMetadata(t, db)
	repo, err = repository.NewDataSourceRepository(db, fixtureVault(t))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := repo.GetByID(ctx, source.ID)
	if err != nil || restored.ID != source.ID || restored.Config.Password != "synthetic-secret" {
		t.Fatalf("source not restored: %v", err)
	}
	auth = security.NewTableAuthorizer(policy(source.ID, true))
	manager, err = security.NewTablePolicyManager(db, auth, policy(source.ID, true), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	if manager.Status().Revision != "2" {
		t.Fatalf("policy revision after restart: %+v", manager.Status())
	}
	if err = auth.AuthorizeReferences(ctx, caller, restored, ref, security.TablePermissionRead); !errors.Is(err, security.ErrTableAccessDenied) {
		t.Fatalf("revocation not restored: %v", err)
	}
	audit, err = auditlog.NewStore(db, "synthetic-key", 1)
	if err != nil {
		t.Fatal(err)
	}
	events, err := audit.List(ctx, auditlog.Filter{TenantID: "team", Limit: 10})
	if err != nil || events.Total != 1 {
		t.Fatalf("audit not restored: %+v %v", events, err)
	}
}

func TestSQLiteMetadataCompetingPolicyPublications(t *testing.T) {
	ctx := context.Background()
	if os.Getenv("DATA_FABRIC_SQLITE_POLICY_WORKER") == "1" {
		db := openMetadata(t, os.Getenv("DATA_FABRIC_SQLITE_POLICY_PATH"))
		defer closeMetadata(t, db)
		initial := policy("source", true)
		auth := security.NewTableAuthorizer(initial)
		manager, err := security.NewTablePolicyManager(db, auth, initial, "worker")
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Stop()
		result, err := manager.Publish(ctx, 1, "operator", policy("source", false))
		if errors.Is(err, security.ErrTablePolicyRevisionConflict) {
			os.Exit(12)
		}
		if err != nil || result.Revision != "2" {
			t.Fatalf("publication failed: %+v %v", result, err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "metadata.sqlite")
	db := openMetadata(t, path)
	initial := policy("source", true)
	manager, err := security.NewTablePolicyManager(db, security.NewTableAuthorizer(initial), initial, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	manager.Stop()
	closeMetadata(t, db)
	cmds := make([]*exec.Cmd, 2)
	for i := range cmds {
		cmds[i] = exec.Command(os.Args[0], "-test.run=^TestSQLiteMetadataCompetingPolicyPublications$", "-test.count=1")
		cmds[i].Env = append(os.Environ(), "DATA_FABRIC_SQLITE_POLICY_WORKER=1", "DATA_FABRIC_SQLITE_POLICY_PATH="+path)
		if err := cmds[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	success, conflict := 0, 0
	for _, cmd := range cmds {
		e := cmd.Wait()
		if e == nil {
			success++
		} else if exit, ok := e.(*exec.ExitError); ok && exit.ExitCode() == 12 {
			conflict++
		} else {
			t.Fatalf("worker failed: %v", e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}
