// Package onboarding stores correlation state, not credentials or authentication.
// This package is not exposed as an endpoint; a caller must separately verify
// the browser's identity and authorization before invoking it.
package onboarding

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

var ErrUnavailable = errors.New("onboarding flow unavailable")
var ErrInvalid = errors.New("invalid onboarding request")

// Principal is obtained only from a trusted identity provider, never user text.
type Principal struct{ Issuer, Subject, Tenant string }

func (p Principal) digest() (string, error) {
	fields := []string{p.Issuer, p.Subject, p.Tenant}
	h := sha256.New()
	for _, field := range fields {
		// Preserve exact authenticated bytes; trimming would alias distinct identities.
		if strings.TrimSpace(field) == "" || len(field) > 4096 {
			return "", ErrInvalid
		}
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(field))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (p Principal) tenantDigest() (string, error) {
	return (Principal{Issuer: p.Issuer, Subject: "tenant-quota-v1", Tenant: p.Tenant}).digest()
}

type record struct {
	IDHash        string `gorm:"primaryKey;size:64"`
	PrincipalHash string `gorm:"size:64;not null"`
	TenantHash    string `gorm:"size:64;index"`
	Operation     string `gorm:"size:16;not null;default:''"`
	TargetID      string `gorm:"size:36;not null;default:''"`
	ExpiresAt     int64  `gorm:"not null;index"`
}

func (record) TableName() string { return "df_onboarding_flows" }

type Store struct {
	db  *gorm.DB
	ttl time.Duration
}

// New does not migrate schemas or expose the store. The application must
// provision the new table on every deployment before enabling any flow.
func New(db *gorm.DB, ttl time.Duration) (*Store, error) {
	if db == nil || db.Dialector == nil || ttl < time.Millisecond || ttl > 10*time.Minute {
		return nil, ErrInvalid
	}
	if db.Dialector.Name() != "mysql" && db.Dialector.Name() != "sqlite" {
		return nil, ErrInvalid
	}
	return &Store{db: db, ttl: ttl}, nil
}

// dbMillis uses the database server's clock. MySQL replicas must share the
// same writable primary; SQLite derives time from the host and is single-host only.
func dbMillis(db *gorm.DB) (int64, error) {
	var expression string
	switch db.Dialector.Name() {
	case "mysql":
		expression = "(UNIX_TIMESTAMP() * 1000 + MICROSECOND(CURRENT_TIMESTAMP(3)) DIV 1000)"
	case "sqlite":
		expression = "CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)"
	default:
		return 0, ErrInvalid
	}
	var ms int64
	if err := db.Raw("SELECT " + expression).Scan(&ms).Error; err != nil {
		return 0, err
	}
	return ms, nil
}

// Migrate creates an additive table. Deploy it ahead of enabling the feature.
func Migrate(db *gorm.DB) error {
	if db == nil {
		return ErrInvalid
	}
	return db.AutoMigrate(&record{})
}

// Begin returns a random correlation ID, not a credential. It never grants
// access; all reads and mutations additionally require the verified principal.
// Deprecated: this legacy helper is unbounded and MUST NOT be used by a public
// endpoint. Production onboarding MUST use BeginBounded and provision quotas.
func (s *Store) Begin(ctx context.Context, actor Principal) (string, error) {
	principal, err := actor.digest()
	if err != nil {
		return "", err
	}
	var b [32]byte
	if _, err = rand.Read(b[:]); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(b[:])
	hash := sha256.Sum256([]byte(id))
	now, err := dbMillis(s.db.WithContext(ctx))
	if err != nil {
		return "", err
	}
	tenantHash, err := actor.tenantDigest()
	if err != nil {
		return "", err
	}
	row := record{IDHash: hex.EncodeToString(hash[:]), PrincipalHash: principal, TenantHash: tenantHash, ExpiresAt: now + s.ttl.Milliseconds()}
	if err = s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return "", fmt.Errorf("store onboarding flow: %w", err)
	}
	return id, nil
}

// ConsumeAndApply deletes an unexpired flow and applies a database-only change
// in one transaction. A callback error rolls back both operations. The callback
// MUST use tx, must not call external services, and must never return secrets.
// Database uniqueness/idempotency must also be enforced by the callback.
// Existing DataSourceService.CreateDataSource does not accept tx and cannot be
// used here. A transaction-aware creation path is required before integration.
// This primitive does not implement outcome recovery, quotas or bounded cleanup.
func (s *Store) ConsumeAndApply(ctx context.Context, id string, actor Principal, apply func(*gorm.DB) error) error {
	return s.consumeIntent(ctx, id, actor, "", "", apply)
}
func (s *Store) consumeIntent(ctx context.Context, id string, actor Principal, operation, target string, apply func(*gorm.DB) error) error {
	principal, err := actor.digest()
	if err != nil || apply == nil {
		return ErrInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != id {
		return ErrUnavailable
	}
	hash := sha256.Sum256([]byte(id))
	// Read expiry before starting the write transaction. Records are immutable;
	// a subsequent conditional delete verifies the same identity and expiry.
	var row record
	lookup := s.db.WithContext(ctx).Where("id_hash = ? AND principal_hash = ? AND operation = ? AND target_id = ?", hex.EncodeToString(hash[:]), principal, operation, target).First(&row)
	if errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
		return ErrUnavailable
	}
	if lookup.Error != nil {
		return lookup.Error
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now, err := dbMillis(tx)
		if err != nil {
			return err
		}
		result := tx.Where("id_hash = ? AND principal_hash = ? AND operation = ? AND target_id = ? AND expires_at > ?", hex.EncodeToString(hash[:]), principal, operation, target, now).Delete(&record{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrUnavailable
		}
		// Re-read server time after acquiring the delete lock; a request may
		// have waited past expiry while another transaction held the row.
		now, err = dbMillis(tx)
		if err != nil {
			return err
		}
		if now >= row.ExpiresAt {
			return ErrUnavailable
		}
		return apply(tx)
	})
}

// PurgeExpired removes only expired correlations, never live flows.
func (s *Store) PurgeExpired(ctx context.Context) (int64, error) {
	now, err := dbMillis(s.db.WithContext(ctx))
	if err != nil {
		return 0, err
	}
	result := s.db.WithContext(ctx).Where("expires_at <= ?", now).Delete(&record{})
	return result.RowsAffected, result.Error
}
