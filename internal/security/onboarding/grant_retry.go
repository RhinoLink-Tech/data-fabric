package onboarding

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EnsureProvisioned is restricted to a trusted provisioning operator. The
// caller must authenticate the identity, verify legacy ownership, and audit.
func (s *GrantStore) EnsureProvisioned(ctx context.Context, issuer, subject, owner, tenant string) error {
	_, err := s.EnsureProvisionedState(ctx, issuer, subject, owner, tenant)
	return err
}

// EnsureProvisionedState reports whether a matching grant was newly created.
// Call inside a transaction to hold the row lock across dependent operations.
// It never rewrites an identity or restores a revoked grant. A concurrent
// insert conflict fails closed and can be retried in a fresh transaction.
func (s *GrantStore) EnsureProvisionedState(ctx context.Context, issuer, subject, owner, tenant string) (bool, error) {
	if s == nil || !validIdentity(issuer, subject) || !canonicalName.MatchString(owner) || !canonicalName.MatchString(tenant) || owner != subject {
		return false, ErrInvalid
	}
	desired := grantRecord{ID: hashFields(issuer, subject), OwnerKey: hashFields(strings.ToLower(owner)), Owner: owner, Tenant: tenant, Enabled: true}
	var existing grantRecord
	err := s.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", desired.ID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err = s.db.WithContext(ctx).Create(&desired).Error; err != nil {
			return false, ErrUnavailable
		}
		return true, nil
	}
	if err != nil || !existing.Enabled || existing.OwnerKey != desired.OwnerKey || existing.Owner != owner || existing.Tenant != tenant {
		return false, ErrUnavailable
	}
	return false, nil
}
