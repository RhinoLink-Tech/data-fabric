package onboardinghttp

import (
	"context"
	"errors"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
)

// updateFlowStore is deliberately not registered with any route. A deployment
// must first enforce current configuration on every connection lease on every
// replica, and verify HTTP authentication, confirmation, and audit.
type updateFlowStore interface {
	ApplyUpdateResult(context.Context, string, onboarding.Principal, string, func(*gorm.DB) (string, error)) (string, error)
}
type updateAudit interface {
	Updated(context.Context, *gorm.DB, Actor, string) error
}
type updateInvalidator interface{ InvalidateDataSource(string) error }

// applyUpdate is the database-only part of a browser-mediated flow. The caller
// must authenticate the browser, validate CSRF, validate complete input and
// pin destinations using the deployment policy before invoking this helper.
// Secrets must never be logged or returned. The recovered source ID is not
// authorization: the caller must re-check current access when returning it.
func (h *Handler) applyUpdate(ctx context.Context, store updateFlowStore, audit updateAudit, pool updateInvalidator, actor Actor, flowID, sourceID string, typ model.DatabaseType, cfg model.DataSourceConfig) (string, error) {
	if !actorValid(actor) || store == nil || audit == nil || pool == nil || !h.types[typ] {
		return "", errors.New("update unavailable")
	}
	if !safeIdentifier.MatchString(cfg.Database) || !safeIdentifier.MatchString(cfg.Username) || (cfg.Schema != "" && !safeIdentifier.MatchString(cfg.Schema)) || !database.ValidOnboardingPassword(cfg.Password) {
		return "", errors.New("invalid configuration")
	}
	if !h.validator.IsSupportedDataSourceType(typ) || h.validator.ValidateDataSourceConfiguration(&cfg, typ) != nil {
		return "", errors.New("invalid configuration")
	}
	caller := &security.Caller{UserID: actor.UserID, TenantID: actor.Tenant, Scopes: []string{"data-fabric.sources.manage"}}
	id, err := store.ApplyUpdateResult(ctx, flowID, principal(actor), sourceID, func(tx *gorm.DB) (string, error) {
		if e := h.auth.LockAuthorization(ctx, tx, actor); e != nil {
			return "", e
		}
		var locked model.DataSource
		if e := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "owner_id", "tenant_id", "type").Where("id = ?", sourceID).First(&locked).Error; e != nil || locked.OwnerID != actor.UserID || locked.TenantID != actor.Tenant || locked.Type != typ {
			return "", errors.New("source unavailable")
		}
		repo, e := repository.NewDataSourceRepository(tx, h.vault)
		if e != nil {
			return "", e
		}
		source, e := repo.GetByID(ctx, sourceID)
		if e != nil || source.OwnerID != actor.UserID || source.TenantID != actor.Tenant || source.Type != typ || (source.Config.SSL && !cfg.SSL) {
			return "", errors.New("source unavailable")
		}
		// A nil pool keeps invalidation outside the encompassing flow transaction.
		svc := service.NewDataSourceService(repo, nil, h.validator)
		if _, e = svc.UpdateDataSource(ctx, caller, sourceID, &service.UpdateDataSourceRequest{Config: &cfg}); e != nil {
			return "", e
		}
		if e = audit.Updated(ctx, tx, actor, sourceID); e != nil {
			return "", e
		}
		return sourceID, nil
	})
	if err != nil {
		return "", errors.New("update unavailable")
	}
	// Run on both first response and idempotent recovery; replicas still require
	// the current-config check described above, including after process crashes.
	if err = pool.InvalidateDataSource(id); err != nil {
		return "", errors.New("update unavailable")
	}
	// Recovery is never an authorization: re-check under the current grant and
	// source lock even when the mutation was committed by an earlier request.
	err = h.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := h.auth.LockAuthorization(ctx, tx, actor); e != nil {
			return e
		}
		var locked model.DataSource
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "owner_id", "tenant_id", "type").Where("id = ?", id).First(&locked).Error; e != nil || locked.OwnerID != actor.UserID || locked.TenantID != actor.Tenant || locked.Type != typ {
			return errors.New("source unavailable")
		}
		return nil
	})
	if err != nil {
		return "", errors.New("update unavailable")
	}
	return id, nil
}

// Updated writes a source-specific event in the same transaction as the update.
func (a TransactionalAudit) Updated(ctx context.Context, tx *gorm.DB, actor Actor, id string) error {
	if tx == nil || len(strings.TrimSpace(a.FingerprintKey)) < 32 {
		return errors.New("audit unavailable")
	}
	store, e := auditlog.NewStore(tx, a.FingerprintKey, 90)
	if e != nil {
		return e
	}
	return store.Record(ctx, auditlog.Event{ActorID: actor.UserID, TenantID: actor.Tenant, Transport: "workbuddy_browser", Operation: "datasource.update", DataSourceID: id, Decision: auditlog.DecisionAllow, Outcome: "success", StatusCode: 200})
}
