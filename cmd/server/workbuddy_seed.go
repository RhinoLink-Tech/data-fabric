package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/user"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// An operator-only helper for the explicitly isolated synthetic-orders profile.
// The template must be a known read-only synthetic source, never user data.
func runWorkbuddySeed(cfg *config.Config, args []string) error {
	wb, err := readWorkbuddyConfig(cfg)
	if err != nil || wb == nil {
		return errors.New("isolated WorkBuddy onboarding required")
	}
	f := flag.NewFlagSet("workbuddy-seed-synthetic", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	subject := f.String("subject", "", "OIDC subject")
	tenant := f.String("tenant", "", "tenant")
	template := f.String("template", "", "synthetic source ID")
	if err = f.Parse(args); err != nil || f.NArg() != 0 {
		return errors.New("invalid arguments")
	}
	if _, err = uuid.Parse(*subject); err != nil || *tenant != "t-"+*subject {
		return errors.New("invalid subject or tenant")
	}
	if _, err = uuid.Parse(*template); err != nil {
		return errors.New("invalid template")
	}
	operator, err := user.Current()
	if err != nil || operator.Username == "" {
		return errors.New("operator identity unavailable")
	}
	vault, err := security.LoadCredentialVaultFromEnv()
	if err != nil {
		return errors.New("vault unavailable")
	}
	db, err := config.InitDatabase(cfg)
	if err != nil {
		return errors.New("metadata unavailable")
	}
	defer func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	}()
	if err = onboarding.RequireInnoDB(db, []string{"data_sources", "audit_events", "df_onboarding_identity_grants"}); err != nil {
		return err
	}
	_, err = seedSyntheticForSubject(context.Background(), db, vault, wb.policy, cfg.MCP.OIDCIssuer, *subject, *tenant, *template, operator.Username, cfg.Security.JWTSecret)
	return err
}

func seedSyntheticForSubject(ctx context.Context, db *gorm.DB, vault *security.CredentialVault, policy *database.FixedIPEgressPolicy, issuer, subject, tenant, templateID, actor, auditKey string) (string, error) {
	var created string
	if db == nil || vault == nil || policy == nil || actor == "" || len(auditKey) < 32 {
		return "", errors.New("configuration unavailable")
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := onboarding.LockAndVerifyGrant(ctx, tx, issuer, subject, subject, tenant); err != nil {
			return errors.New("grant unavailable")
		}
		repo, err := repository.NewDataSourceRepository(tx, vault)
		if err != nil {
			return err
		}
		sample, err := repo.GetByID(ctx, templateID)
		if err != nil {
			return errors.New("synthetic source unavailable")
		}
		if sample.Type != model.DatabaseTypePostgreSQL || sample.Status != model.DataSourceStatusActive || sample.Config.Database != "synthetic_orders" || sample.Config.Username != "demo_reader" || policy.Check(sample) != nil {
			return errors.New("source is not the approved synthetic profile")
		}
		const name = "Sample Orders"
		existing, err := repo.GetByName(ctx, tenant, name)
		if err == nil {
			if existing.OwnerID != subject || existing.TenantID != tenant {
				return errors.New("source name already in use")
			}
			created = existing.ID
			return nil
		}
		if !errors.Is(err, repository.ErrDataSourceNotFound) {
			return errors.New("source lookup failed")
		}
		ds := &model.DataSource{Name: name, Type: sample.Type, Config: sample.Config, Status: model.DataSourceStatusActive, OwnerID: subject, TenantID: tenant}
		if err = repo.Create(ctx, ds); err != nil {
			return errors.New("source creation failed")
		}
		audit, err := auditlog.NewStore(tx, auditKey, 90)
		if err != nil {
			return errors.New("audit unavailable")
		}
		if err = audit.Record(ctx, auditlog.Event{ActorID: actor, TenantID: tenant, Transport: "local_operator", Operation: "workbuddy.synthetic.provision", DataSourceID: ds.ID, Decision: auditlog.DecisionAllow, Outcome: "success"}); err != nil {
			return errors.New("audit failed")
		}
		created = ds.ID
		return nil
	})
	if err != nil {
		return "", err
	}
	if created == "" {
		return "", fmt.Errorf("source unavailable")
	}
	return created, nil
}
