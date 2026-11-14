package onboarding

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// quotaLock serializes quota admission across replicas using one database row.
// It is an additive opt-in schema, not part of the existing automatic migrations.
type quotaLock struct {
	ID       int   `gorm:"primaryKey"`
	Revision int64 `gorm:"not null;default:0"`
}

func (quotaLock) TableName() string { return "df_onboarding_quota_lock" }
func MigrateQuota(db *gorm.DB) error {
	if db == nil {
		return ErrInvalid
	}
	if err := db.AutoMigrate(&quotaLock{}); err != nil {
		return err
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&quotaLock{ID: 1}).Error
}

// BeginBounded enforces both per-principal and deployment-wide storage quotas.
// The shared row is locked before counting and inserting. Expired records are
// removed in bounded batches. A separate scheduled cleanup is still required.
func (s *Store) BeginBounded(ctx context.Context, actor Principal, perPrincipal, perTenant, global int) (string, error) {
	return s.beginBoundedIntent(ctx, actor, "", "", perPrincipal, perTenant, global)
}

// BeginBoundedUpdate binds a flow to an exact canonical source ID; the caller must separately authorize it.
func (s *Store) BeginBoundedUpdate(ctx context.Context, actor Principal, target string, perPrincipal, perTenant, global int) (string, error) {
	parsed, e := uuid.Parse(target)
	if e != nil || parsed.String() != target {
		return "", ErrInvalid
	}
	return s.beginBoundedIntent(ctx, actor, "update", target, perPrincipal, perTenant, global)
}
func (s *Store) beginBoundedIntent(ctx context.Context, actor Principal, operation, target string, perPrincipal, perTenant, global int) (string, error) {
	if perPrincipal < 1 || perTenant < perPrincipal || global < perTenant || global > 1000000 {
		return "", ErrInvalid
	}
	principal, e := actor.digest()
	if e != nil {
		return "", e
	}
	tenantHash, e := actor.tenantDigest()
	if e != nil {
		return "", e
	}
	var raw [32]byte
	if _, e = rand.Read(raw[:]); e != nil {
		return "", e
	}
	id := base64.RawURLEncoding.EncodeToString(raw[:])
	h := sha256.Sum256([]byte(id))
	e = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lock := tx.Model(&quotaLock{}).Where("id = ?", 1).UpdateColumn("revision", gorm.Expr("revision + 1"))
		if lock.Error != nil {
			return lock.Error
		}
		if lock.RowsAffected != 1 {
			return ErrUnavailable
		}
		now, err := dbMillis(tx)
		if err != nil {
			return err
		}
		if _, err = purgeFlowBatch(tx, now, 100); err != nil {
			return err
		}
		if _, err = purgeCompletionBatch(tx, now, 100); err != nil {
			return err
		}
		var all, own, tenantCount, unknown, flowCount, doneCount int64
		if err = tx.Model(&record{}).Where("tenant_hash = ? OR tenant_hash IS NULL", "").Count(&unknown).Error; err != nil {
			return err
		}
		if unknown != 0 {
			return ErrUnavailable
		}
		if err = tx.Model(&completion{}).Where("tenant_hash = ? OR tenant_hash IS NULL", "").Count(&unknown).Error; err != nil {
			return err
		}
		if unknown != 0 {
			return ErrUnavailable
		}
		if err = tx.Model(&record{}).Count(&flowCount).Error; err != nil {
			return err
		}
		if err = tx.Model(&completion{}).Count(&doneCount).Error; err != nil {
			return err
		}
		all = flowCount + doneCount
		if err = tx.Model(&record{}).Where("principal_hash = ?", principal).Count(&flowCount).Error; err != nil {
			return err
		}
		if err = tx.Model(&completion{}).Where("principal_hash = ?", principal).Count(&doneCount).Error; err != nil {
			return err
		}
		own = flowCount + doneCount
		if err = tx.Model(&record{}).Where("tenant_hash = ?", tenantHash).Count(&flowCount).Error; err != nil {
			return err
		}
		if err = tx.Model(&completion{}).Where("tenant_hash = ?", tenantHash).Count(&doneCount).Error; err != nil {
			return err
		}
		tenantCount = flowCount + doneCount
		if own >= int64(perPrincipal) || tenantCount >= int64(perTenant) || all >= int64(global) {
			return ErrUnavailable
		}
		return tx.Create(&record{IDHash: hex.EncodeToString(h[:]), PrincipalHash: principal, TenantHash: tenantHash, Operation: operation, TargetID: target, ExpiresAt: now + s.ttl.Milliseconds()}).Error
	})
	if e != nil {
		return "", e
	}
	return id, nil
}
func purgeFlowBatch(tx *gorm.DB, now int64, limit int) (int64, error) {
	var ids []string
	if e := tx.Model(&record{}).Where("expires_at <= ?", now).Order("expires_at").Limit(limit).Pluck("id_hash", &ids).Error; e != nil {
		return 0, e
	}
	if len(ids) == 0 {
		return 0, nil
	}
	r := tx.Where("id_hash IN ? AND expires_at <= ?", ids, now).Delete(&record{})
	return r.RowsAffected, r.Error
}
func purgeCompletionBatch(tx *gorm.DB, now int64, limit int) (int64, error) {
	var ids []string
	if e := tx.Model(&completion{}).Where("expires_at <= ?", now).Order("expires_at").Limit(limit).Pluck("id_hash", &ids).Error; e != nil {
		return 0, e
	}
	if len(ids) == 0 {
		return 0, nil
	}
	r := tx.Where("id_hash IN ? AND expires_at <= ?", ids, now).Delete(&completion{})
	return r.RowsAffected, r.Error
}

// Inspect returns only pending or a completed source ID for an exact principal.
// It does not authorize recovery: the caller must recheck scope and source access.
func (s *Store) Inspect(ctx context.Context, id string, actor Principal) (state, source string, err error) {
	return s.InspectTx(ctx, s.db, id, actor)
}

// InspectTx permits authorization and source existence to be checked under a
// single caller-owned transaction. The transaction must use the same database.
func (s *Store) InspectTx(ctx context.Context, tx *gorm.DB, id string, actor Principal) (state, source string, err error) {
	return s.inspectIntentTx(ctx, tx, id, actor, "", "")
}

// InspectUpdateTx is an intent check, not authorization to update the source.
func (s *Store) InspectUpdateTx(ctx context.Context, tx *gorm.DB, id string, actor Principal, target string) (string, string, error) {
	parsed, e := uuid.Parse(target)
	if e != nil || parsed.String() != target {
		return "", "", ErrInvalid
	}
	return s.inspectIntentTx(ctx, tx, id, actor, "update", target)
}
func (s *Store) inspectIntentTx(ctx context.Context, tx *gorm.DB, id string, actor Principal, operation, target string) (state, source string, err error) {
	if tx == nil {
		return "", "", ErrUnavailable
	}
	principal, e := actor.digest()
	if e != nil {
		return "", "", ErrInvalid
	}
	hash, e := flowHash(id)
	if e != nil {
		return "", "", ErrUnavailable
	}
	db := tx.WithContext(ctx)
	now, e := dbMillis(db)
	if e != nil {
		return "", "", e
	}
	var row record
	e = db.Where("id_hash = ? AND principal_hash = ? AND operation = ? AND target_id = ? AND expires_at > ?", hash, principal, operation, target, now).First(&row).Error
	if e == nil {
		return "pending", "", nil
	}
	if !errors.Is(e, gorm.ErrRecordNotFound) {
		return "", "", e
	}
	var done completion
	e = db.Where("id_hash = ? AND principal_hash = ? AND operation = ? AND target_id = ? AND expires_at > ?", hash, principal, operation, target, now).First(&done).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return "", "", ErrUnavailable
	}
	if e != nil {
		return "", "", e
	}
	return "completed", done.SourceID, nil
}

// PurgeBounded removes at most limit expired flows and limit expired results.
// A deployment must schedule this independently of client traffic.
func (s *Store) PurgeBounded(ctx context.Context, limit int) (int64, error) {
	if limit < 1 || limit > 1000 {
		return 0, ErrInvalid
	}
	var n int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lock := tx.Model(&quotaLock{}).Where("id = ?", 1).UpdateColumn("revision", gorm.Expr("revision + 1"))
		if lock.Error != nil {
			return lock.Error
		}
		if lock.RowsAffected != 1 {
			return ErrUnavailable
		}
		now, e := dbMillis(tx)
		if e != nil {
			return e
		}
		a, e := purgeFlowBatch(tx, now, limit)
		if e != nil {
			return e
		}
		b, e := purgeCompletionBatch(tx, now, limit)
		if e != nil {
			return e
		}
		n = a + b
		return nil
	})
	return n, err
}

// Exists checks only opaque ID liveness for a login initiation. It grants no authorization.
// Public callers require rate limiting and generic errors to prevent abuse.
func (s *Store) Exists(ctx context.Context, id string) (bool, error) {
	hash, e := flowHash(id)
	if e != nil {
		return false, nil
	}
	db := s.db.WithContext(ctx)
	now, e := dbMillis(db)
	if e != nil {
		return false, e
	}
	var count int64
	e = db.Model(&record{}).Where("id_hash = ? AND expires_at > ?", hash, now).Count(&count).Error
	return count == 1, e
}
