package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// completion stores only the non-secret ID of a committed mutation. It is
// bound to the same authenticated principal as the flow. Not exposed by routes.
type completion struct {
	IDHash        string `gorm:"primaryKey;size:64"`
	PrincipalHash string `gorm:"size:64;not null"`
	TenantHash    string `gorm:"size:64;index"`
	SourceID      string `gorm:"type:char(36);not null"`
	ExpiresAt     int64  `gorm:"not null;index"`
}

func (completion) TableName() string { return "df_onboarding_completions" }

// MigrateCompletions is additive and must be installed before use. Do not
// enable externally until quotas, trusted identity binding and audit are in place.
func MigrateCompletions(db *gorm.DB) error {
	if db == nil {
		return ErrInvalid
	}
	return db.AutoMigrate(&completion{})
}

func flowHash(id string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil || len(b) != 32 || base64.RawURLEncoding.EncodeToString(b) != id {
		return "", ErrUnavailable
	}
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:]), nil
}

// ApplyResult commits the business mutation and its recovery ID in the same
// transaction. The callback MUST use tx exclusively; it must not perform
// external side effects or return credentials. Retries with the same verified
// principal recover the original source ID without repeating the callback.
// Recovery expires after the store's TTL; callers must not infer failure from
// an expired recovery record. A recovered ID does not grant access: callers
// must reauthenticate, verify issuer and tenant, recheck current management
// scope, source permission and existence before returning it. Reuse of a flow
// ID always returns its first result, even if retry inputs differ; an integrating
// API must define and enforce its request-reuse policy. This primitive has no
// quotas, rate limits, scheduled cleanup, OIDC, CSRF protection or audit.
func (s *Store) ApplyResult(ctx context.Context, id string, actor Principal, apply func(*gorm.DB) (string, error)) (string, error) {
	principal, err := actor.digest()
	if err != nil || apply == nil {
		return "", ErrInvalid
	}
	hash, err := flowHash(id)
	if err != nil {
		return "", err
	}
	tenantHash, err := actor.tenantDigest()
	if err != nil {
		return "", err
	}
	var created string
	err = s.ConsumeAndApply(ctx, id, actor, func(tx *gorm.DB) error {
		source, e := apply(tx)
		if e != nil {
			return e
		}
		parsed, e := uuid.Parse(source)
		if e != nil || parsed.String() != source {
			return ErrInvalid
		}
		now, e := dbMillis(tx)
		if e != nil {
			return e
		}
		if e = tx.Create(&completion{IDHash: hash, PrincipalHash: principal, TenantHash: tenantHash, SourceID: source, ExpiresAt: now + s.ttl.Milliseconds()}).Error; e != nil {
			return e
		}
		created = source
		return nil
	})
	if err == nil {
		return created, nil
	}
	if !errors.Is(err, ErrUnavailable) {
		return "", err
	}
	now, e := dbMillis(s.db.WithContext(ctx))
	if e != nil {
		return "", e
	}
	var done completion
	e = s.db.WithContext(ctx).Where("id_hash = ? AND principal_hash = ? AND expires_at > ?", hash, principal, now).First(&done).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return "", ErrUnavailable
	}
	if e != nil {
		return "", e
	}
	return done.SourceID, nil
}

// PurgeCompletions removes at most limit expired results. A scheduled caller
// must run it periodically; without quotas or this worker, storage can grow.
func (s *Store) PurgeCompletions(ctx context.Context, limit int) (int64, error) {
	if limit < 1 || limit > 1000 {
		return 0, ErrInvalid
	}
	db := s.db.WithContext(ctx)
	now, err := dbMillis(db)
	if err != nil {
		return 0, err
	}
	var ids []string
	if err = db.Model(&completion{}).Where("expires_at <= ?", now).Order("expires_at").Limit(limit).Pluck("id_hash", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	r := db.Where("id_hash IN ? AND expires_at <= ?", ids, now).Delete(&completion{})
	return r.RowsAffected, r.Error
}
