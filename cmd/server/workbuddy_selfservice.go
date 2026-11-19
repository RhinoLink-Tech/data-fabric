package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/mcp"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/gorm"
)

// selfServiceValidator is only constructed for an explicitly opted-in,
// isolated WorkBuddy deployment using the fixed OIDC validator. It does not
// provision a user until a signed access token asserts a verified email.
type selfServiceValidator struct {
	next      mcp.MCPTokenValidator
	issuer    string
	provision func(context.Context, string, string) error
}

func (v *selfServiceValidator) ValidateMCPToken(ctx context.Context, raw string) (*security.Claims, error) {
	claims, err := v.next.ValidateMCPToken(ctx, raw)
	if err != nil || claims == nil {
		return nil, errors.New("token unavailable")
	}
	if claims.TenantID != "" {
		return claims, nil
	}
	if claims.Issuer != v.issuer || claims.Subject == "" || claims.UserID != claims.Subject || !claims.EmailVerified {
		return nil, errors.New("verified account required")
	}
	digest := sha256.Sum256([]byte(claims.Issuer + "\x00" + claims.Subject))
	tenant := "t-" + hex.EncodeToString(digest[:16])
	if err := v.provision(ctx, claims.Subject, tenant); err != nil {
		return nil, errors.New("account setup unavailable")
	}
	result := *claims
	result.TenantID = tenant
	return &result, nil
}

// provisionSelfServiceGrant keeps source-owner validation, the grant and its
// audit event in one transaction. An existing revoked grant is never restored.
func provisionSelfServiceGrant(ctx context.Context, db *gorm.DB, issuer, subject, tenant, auditKey string, seed func(context.Context, *gorm.DB) error) error {
	if db == nil || len(strings.TrimSpace(auditKey)) < 32 {
		return onboarding.ErrUnavailable
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var sources []struct {
			OwnerID  string
			TenantID string
		}
		if err := tx.Table("data_sources").Select("owner_id, tenant_id").Where("LOWER(owner_id) = LOWER(?)", subject).Limit(10001).Scan(&sources).Error; err != nil || len(sources) > 10000 {
			return onboarding.ErrUnavailable
		}
		for _, source := range sources {
			if source.OwnerID != subject || source.TenantID != tenant {
				return onboarding.ErrUnavailable
			}
		}
		store, err := onboarding.NewGrantStore(tx)
		if err != nil {
			return onboarding.ErrUnavailable
		}
		created, err := store.EnsureProvisionedState(ctx, issuer, subject, subject, tenant)
		if err != nil {
			return onboarding.ErrUnavailable
		}
		if created && seed != nil {
			if err = seed(ctx, tx); err != nil {
				return onboarding.ErrUnavailable
			}
		}
		audit, err := auditlog.NewStore(tx, auditKey, 90)
		if err != nil {
			return onboarding.ErrUnavailable
		}
		digest := sha256.Sum256([]byte(issuer + "\x00" + subject))
		return audit.Record(ctx, auditlog.Event{ActorID: subject, TenantID: tenant, Transport: "mcp-http", Operation: "workbuddy.selfservice.provision", ReasonCode: "target:" + hex.EncodeToString(digest[:]), Decision: auditlog.DecisionAllow, Outcome: "success"})
	})
}
