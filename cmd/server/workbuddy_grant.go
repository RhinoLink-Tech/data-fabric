package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/user"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/gorm"
)

// Run only as an OS-authorized operator on an isolated deployment. This does
// not make an unrestricted second service on the same metadata DB safe.
func runWorkbuddyGrant(cfg *config.Config, args []string) error {
	if c, err := readWorkbuddyConfig(cfg); err != nil {
		return err
	} else if c == nil {
		return errors.New("WorkBuddy onboarding is disabled")
	}
	if len(args) < 1 || (args[0] != "add" && args[0] != "revoke" && args[0] != "restore") {
		return errors.New("expected workbuddy-grant add, revoke or restore")
	}
	fs := flag.NewFlagSet("workbuddy-grant", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	subject := fs.String("subject", "", "OIDC subject")
	tenant := fs.String("tenant", "", "tenant identifier")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || *subject == "" || *tenant == "" {
		return errors.New("subject and tenant required")
	}
	operator, err := user.Current()
	if err != nil || operator.Username == "" {
		return errors.New("OS operator identity unavailable")
	}
	db, err := config.InitDatabase(cfg)
	if err != nil {
		return errors.New("metadata database unavailable")
	}
	defer func() {
		sqlDB, e := db.DB()
		if e == nil {
			_ = sqlDB.Close()
		}
	}()
	if err = onboarding.MigrateGrants(db); err != nil {
		return errors.New("grant schema unavailable")
	}
	if !db.Migrator().HasTable(&auditlog.Event{}) {
		return errors.New("audit schema unavailable")
	}
	if err = onboarding.RequireInnoDB(db, []string{"data_sources", "audit_events", "df_onboarding_identity_grants"}); err != nil {
		return err
	}
	return setWorkbuddyGrant(context.Background(), db, cfg.MCP.OIDCIssuer, *subject, *tenant, operator.Username, cfg.Security.JWTSecret, args[0])
}
func setWorkbuddyGrant(ctx context.Context, db *gorm.DB, issuer, subject, tenant, actor, key, action string) error {
	if db == nil || strings.TrimSpace(actor) == "" || len(strings.TrimSpace(key)) < 32 || (action != "add" && action != "revoke" && action != "restore") {
		return errors.New("grant request rejected")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		store, err := onboarding.NewGrantStore(tx)
		if err != nil {
			return errors.New("grant store unavailable")
		}
		// Check case-folded legacy owner collisions before adding. A separate
		// unrestricted service can still race this check and must not be deployed.
		if action == "add" || action == "restore" {
			var sources []struct {
				OwnerID  string
				TenantID string
			}
			if err = tx.Table("data_sources").Select("owner_id,tenant_id").Where("LOWER(owner_id) = LOWER(?)", subject).Limit(10001).Scan(&sources).Error; err != nil || len(sources) > 10000 {
				return errors.New("source ownership verification unavailable")
			}
			for _, source := range sources {
				if source.OwnerID != subject || source.TenantID != tenant {
					return errors.New("conflicting legacy source ownership")
				}
			}
			if action == "add" {
				err = store.Provision(ctx, issuer, subject, subject, tenant)
			} else {
				err = store.Restore(ctx, issuer, subject, subject, tenant)
			}
			if err != nil {
				return errors.New("grant not created or restored")
			}
		} else {
			_, current, allowed, e := store.Resolve(ctx, issuer, subject)
			if e != nil || !allowed || current != tenant {
				return errors.New("grant not found")
			}
			if err = store.Revoke(ctx, issuer, subject); err != nil {
				return errors.New("grant not revoked")
			}
		}
		audit, err := auditlog.NewStore(tx, key, 90)
		if err != nil {
			return errors.New("audit unavailable")
		}
		digest := sha256.Sum256([]byte(issuer + "\x00" + subject))
		return audit.Record(ctx, auditlog.Event{ActorID: actor, TenantID: tenant, Transport: "local_operator", Operation: "workbuddy.grant." + action, ReasonCode: "target:" + hex.EncodeToString(digest[:]), Decision: auditlog.DecisionAllow, Outcome: "success"})
	})
}
func printWorkbuddyGrantResult() { fmt.Println("WorkBuddy grant updated") }
