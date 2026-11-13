package onboardingruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestManagementMutationAndAuditAtomic(t *testing.T) {
	for _, op := range []string{"rename", "status", "delete"} {
		for _, failAudit := range []bool{false, true} {
			t.Run(op+map[bool]string{true: "_audit_failure", false: "_success"}[failAudit], func(t *testing.T) {
				db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
				if err != nil {
					t.Fatal(err)
				}
				if err = db.Exec("CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null, tenant_id text not null, created_at datetime, updated_at datetime, UNIQUE (tenant_id,name))").Error; err != nil {
					t.Fatal(err)
				}
				if err = db.AutoMigrate(&auditlog.Event{}); err != nil {
					t.Fatal(err)
				}
				if err = onboarding.MigrateGrants(db); err != nil {
					t.Fatal(err)
				}
				vault, err := security.NewCredentialVault([]byte(strings.Repeat("a", 32)))
				if err != nil {
					t.Fatal(err)
				}
				repo, err := repository.NewDataSourceRepository(db, vault)
				if err != nil {
					t.Fatal(err)
				}
				ds := &model.DataSource{ID: uuid.NewString(), Name: "orders", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, OwnerID: "alice", TenantID: "tenant", Config: model.DataSourceConfig{Host: "db", Port: 3306, Password: "synthetic-secret"}}
				ctx := context.Background()
				if err = repo.Create(ctx, ds); err != nil {
					t.Fatal(err)
				}
				store, err := onboarding.NewGrantStore(db)
				if err != nil {
					t.Fatal(err)
				}
				if err = store.Provision(ctx, "https://issuer.test", "alice", "alice", "tenant"); err != nil {
					t.Fatal(err)
				}

				if failAudit {
					if err = db.Exec("DROP TABLE audit_events").Error; err != nil {
						t.Fatal(err)
					}
				}
				g := grants{store: store, db: db, repo: repo, auditKey: strings.Repeat("k", 32)}
				err = g.WithManagementGrant(ctx, "https://issuer.test", "alice", "alice", "tenant", func(txctx context.Context) error {
					atomic := repo.(repository.AtomicSourceManagement)
					scope := repository.SourceWriteScope{OwnerID: "alice", TenantID: "tenant"}
					switch op {
					case "rename":
						_, err = atomic.RenameSourceScoped(txctx, scope, ds.ID, "new")
					case "status":
						err = atomic.SetSourceStatusScoped(txctx, scope, ds.ID, model.DataSourceStatusInactive)
					case "delete":
						err = atomic.DeleteSourceScoped(txctx, scope, ds.ID, "orders")
					}
					if err != nil {
						return err
					}
					return g.RecordManagementSuccess(txctx, "alice", "tenant", "datasource."+op, ds.ID)
				})
				if (err != nil) != failAudit {
					t.Fatalf("unexpected result %v", err)
				}
				if !failAudit {
					var count int64
					if err = db.Model(&auditlog.Event{}).Count(&count).Error; err != nil || count != 1 {
						t.Fatalf("audit count %d %v", count, err)
					}
				}
				got, getErr := repo.GetByID(ctx, ds.ID)
				if failAudit {
					if getErr != nil || got.Name != "orders" || got.Status != model.DataSourceStatusActive {
						t.Fatal("mutation survived failed audit")
					}
				} else if op == "delete" {
					if getErr == nil {
						t.Fatal("delete did not commit")
					}
				} else if getErr != nil || (op == "rename" && got.Name != "new") || (op == "status" && got.Status != model.DataSourceStatusInactive) {
					t.Fatal("mutation did not commit")
				}
			})
		}
	}
}
