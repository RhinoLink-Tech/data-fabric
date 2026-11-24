package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Shared entries are operator-provided test databases. Their credentials must
// be independently restricted to SELECT by the database administrator.
type sharedTestEntry struct {
	Key      string                 `json:"key"`
	Name     string                 `json:"name"`
	Type     model.DatabaseType     `json:"type"`
	Config   model.DataSourceConfig `json:"config"`
	ReadOnly bool                   `json:"readOnly"`
}

func loadSharedTestCatalog(path string, policy *database.FixedIPEgressPolicy) ([]sharedTestEntry, error) {
	if path == "" {
		return nil, nil
	}
	stat, err := os.Stat(path)
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > 65536 {
		return nil, errors.New("shared test catalog unavailable")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("shared test catalog unavailable")
	}
	var entries []sharedTestEntry
	if json.Unmarshal(b, &entries) != nil || len(entries) == 0 || len(entries) > 16 {
		return nil, errors.New("invalid shared test catalog")
	}
	keys, names := map[string]bool{}, map[string]bool{}
	for _, e := range entries {
		if e.Key == "" || len(e.Key) > 64 || strings.TrimSpace(e.Key) != e.Key || strings.TrimSpace(e.Name) != e.Name || e.Name == "" || len(e.Name) > 100 || !e.ReadOnly || keys[e.Key] || names[e.Name] || (e.Type != model.DatabaseTypeMySQL && e.Type != model.DatabaseTypePostgreSQL) || e.Config.Database == "" || e.Config.Username == "" || e.Config.Password == "" {
			return nil, errors.New("invalid shared test catalog entry")
		}
		ds := &model.DataSource{Type: e.Type, Config: e.Config, Status: model.DataSourceStatusActive}
		if policy == nil || policy.Check(ds) != nil {
			return nil, errors.New("shared test catalog is outside egress policy")
		}
		keys[e.Key], names[e.Name] = true, true
	}
	return entries, nil
}

// Called under the locked, already verified identity grant. Each tenant gets
// a separate metadata record while the underlying database remains shared.
func provisionSharedTestSources(ctx context.Context, tx *gorm.DB, vault *security.CredentialVault, issuer, subject, tenant, auditKey string, entries []sharedTestEntry) error {
	repo, err := repository.NewDataSourceRepository(tx, vault)
	if err != nil {
		return err
	}
	audit, err := auditlog.NewStore(tx, auditKey, 90)
	if err != nil {
		return err
	}
	for _, e := range entries {
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("data-fabric-shared-test\x00"+issuer+"\x00"+subject+"\x00"+e.Key)).String()
		existing, err := repo.GetByID(ctx, id)
		if err == nil {
			if existing.OwnerID != subject || existing.TenantID != tenant {
				return errors.New("shared source identity conflict")
			}
			continue
		}
		if !errors.Is(err, repository.ErrDataSourceNotFound) {
			return err
		}
		name := "共享测试 · " + e.Name
		for i := 0; i < 10; i++ {
			candidate := name
			if i > 0 {
				candidate = fmt.Sprintf("%s (%d)", name, i+1)
			}
			_, err = repo.GetByName(ctx, tenant, candidate)
			if errors.Is(err, repository.ErrDataSourceNotFound) {
				name = candidate
				break
			}
			if err != nil {
				return err
			}
			if i == 9 {
				return errors.New("shared source name unavailable")
			}
		}
		ds := &model.DataSource{ID: id, Name: name, Type: e.Type, Config: e.Config, Status: model.DataSourceStatusActive, OwnerID: subject, TenantID: tenant}
		if err = repo.Create(ctx, ds); err != nil {
			return err
		}
		if err = audit.Record(ctx, auditlog.Event{ActorID: subject, TenantID: tenant, Transport: "mcp-http", Operation: "workbuddy.shared.provision", DataSourceID: id, Decision: auditlog.DecisionAllow, Outcome: "success"}); err != nil {
			return err
		}
	}
	return nil
}
