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

type record struct {
	IDHash        string `gorm:"primaryKey;size:64"`
	PrincipalHash string `gorm:"size:64;not null"`
	ExpiresAt     int64  `gorm:"not null;index"`
}

func (record) TableName() string { return "df_onboarding_flows" }

type Store struct {
	db  *gorm.DB
	ttl time.Duration
	now func() time.Time
}

// New does not migrate schemas or expose the store. The application must
// provision the new table on every deployment before enabling any flow.
func New(db *gorm.DB, ttl time.Duration) (*Store, error) {
	if db == nil || ttl <= 0 || ttl > 10*time.Minute {
		return nil, ErrInvalid
	}
	return &Store{db: db, ttl: ttl, now: time.Now}, nil
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
	row := record{IDHash: hex.EncodeToString(hash[:]), PrincipalHash: principal, ExpiresAt: s.now().Add(s.ttl).UnixMilli()}
	if err = s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return "", fmt.Errorf("store onboarding flow: %w", err)
	}
	return id, nil
}

// ConsumeAndApply deletes an unexpired flow and applies a database-only change
// in one transaction. A callback error rolls back both operations. The callback
// MUST use tx, must not call external services, and must never return secrets.
// Database uniqueness/idempotency must also be enforced by the callback.
func (s *Store) ConsumeAndApply(ctx context.Context, id string, actor Principal, apply func(*gorm.DB) error) error {
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
	lookup := s.db.WithContext(ctx).Where("id_hash = ? AND principal_hash = ?", hex.EncodeToString(hash[:]), principal).First(&row)
	if errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
		return ErrUnavailable
	}
	if lookup.Error != nil {
		return lookup.Error
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id_hash = ? AND principal_hash = ? AND expires_at > ?", hex.EncodeToString(hash[:]), principal, s.now().UnixMilli()).Delete(&record{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 || s.now().UnixMilli() >= row.ExpiresAt {
			return ErrUnavailable
		}
		return apply(tx)
	})
}

// PurgeExpired removes only expired correlations, never live flows.
func (s *Store) PurgeExpired(ctx context.Context) (int64, error) {
	result := s.db.WithContext(ctx).Where("expires_at <= ?", s.now().UnixMilli()).Delete(&record{})
	return result.RowsAffected, result.Error
}
