package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/url"
	"regexp"
	"strings"
)

// GrantStore is a shared-db, operator-provisioned mapping. No HTTP mutation
// routes are provided. Updates require independently authenticated operator
// controls and audit; callers must additionally enforce access-token scope.
type GrantStore struct{ db *gorm.DB }
type grantRecord struct {
	ID       string `gorm:"primaryKey;size:64"`
	OwnerKey string `gorm:"uniqueIndex;size:64;not null"`
	Owner    string `gorm:"size:255;not null"`
	Tenant   string `gorm:"size:255;not null"`
	Enabled  bool   `gorm:"not null"`
}

func (grantRecord) TableName() string { return "df_onboarding_identity_grants" }

var canonicalName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_|@.:-]{0,254}$`)

func hashFields(fields ...string) string {
	h := sha256.New()
	for _, s := range fields {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(len(s)))
		_, _ = h.Write(b[:])
		_, _ = h.Write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func validIdentity(issuer, subject string) bool {
	u, e := url.Parse(issuer)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(issuer) != issuer || strings.TrimSpace(subject) != subject || subject == "" || len(subject) > 255 {
		return false
	}
	for _, c := range []byte(subject) {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func NewGrantStore(db *gorm.DB) (*GrantStore, error) {
	if db == nil || db.Dialector == nil || (db.Dialector.Name() != "mysql" && db.Dialector.Name() != "sqlite") {
		return nil, ErrInvalid
	}
	return &GrantStore{db}, nil
}
func MigrateGrants(db *gorm.DB) error {
	if db == nil {
		return ErrInvalid
	}
	// MySQL GORM AutoMigrate can misidentify an existing unique index as a
	// foreign key on repeated startup. Verify the immutable schema instead of
	// mutating a live grant table; incomplete schemas fail closed.
	if db.Dialector.Name() == "mysql" && db.Migrator().HasTable(&grantRecord{}) {
		for _, name := range []string{"id", "owner_key", "owner", "tenant", "enabled"} {
			if !db.Migrator().HasColumn(&grantRecord{}, name) {
				return ErrUnavailable
			}
		}
		var n int64
		err := db.Raw(`SELECT COUNT(*) FROM (SELECT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='df_onboarding_identity_grants' AND NON_UNIQUE=0 GROUP BY INDEX_NAME HAVING COUNT(*)=1 AND MIN(COLUMN_NAME) IN ('owner_key','id') AND MAX(COALESCE(SUB_PART,0))=0) AS safe_indexes`).Scan(&n).Error
		if err != nil || n < 2 {
			return ErrUnavailable
		}
		return nil
	}
	return db.AutoMigrate(&grantRecord{})
}

// Provision must only be invoked by an authenticated operator after verifying
// canonical owner belongs exclusively to this tenant under legacy REST rules.
// The first rollout requires owner == subject, matching legacy MCP tools.
// Case-folded owner IDs are unique across mappings to guard common legacy
// case-insensitive database collations. Exact owner/tenant comparison remains
// required on the legacy API before this resolver can be enabled.
// Owner IDs must be globally unique across mappings to prevent cross-tenant
// owner matching; unsupported IDs are rejected rather than rewritten.
func (s *GrantStore) Provision(ctx context.Context, issuer, subject, owner, tenant string) error {
	if s == nil || !validIdentity(issuer, subject) || !canonicalName.MatchString(owner) || !canonicalName.MatchString(tenant) || owner != subject {
		return ErrInvalid
	}
	row := grantRecord{ID: hashFields(issuer, subject), OwnerKey: hashFields(strings.ToLower(owner)), Owner: owner, Tenant: tenant, Enabled: true}
	return s.db.WithContext(ctx).Create(&row).Error
}
func (s *GrantStore) Revoke(ctx context.Context, issuer, subject string) error {
	if s == nil || !validIdentity(issuer, subject) {
		return ErrInvalid
	}
	return s.db.WithContext(ctx).Model(&grantRecord{}).Where("id = ?", hashFields(issuer, subject)).Update("enabled", false).Error
}
func (s *GrantStore) Resolve(ctx context.Context, issuer, subject string) (string, string, bool, error) {
	if s == nil || !validIdentity(issuer, subject) {
		return "", "", false, ErrUnavailable
	}
	var row grantRecord
	if err := s.db.WithContext(ctx).Where("id = ?", hashFields(issuer, subject)).First(&row).Error; err != nil {
		return "", "", false, ErrUnavailable
	}
	if !row.Enabled || !canonicalName.MatchString(row.Owner) || !canonicalName.MatchString(row.Tenant) || row.Owner != subject {
		return "", "", false, ErrUnavailable
	}
	return row.Owner, row.Tenant, true, nil
}

// Restore explicitly re-enables a previously revoked matching grant. The
// operator must revalidate ownership and authorization before calling this.
func (s *GrantStore) Restore(ctx context.Context, issuer, subject, owner, tenant string) error {
	if s == nil || !validIdentity(issuer, subject) || !canonicalName.MatchString(owner) || !canonicalName.MatchString(tenant) || owner != subject {
		return ErrInvalid
	}
	var row grantRecord
	if err := s.db.WithContext(ctx).Where("id = ?", hashFields(issuer, subject)).First(&row).Error; err != nil || row.Enabled || row.Owner != owner || row.Tenant != tenant {
		return ErrUnavailable
	}
	result := s.db.WithContext(ctx).Model(&grantRecord{}).Where("id = ? AND enabled = ?", row.ID, false).Update("enabled", true)
	if result.Error != nil || result.RowsAffected != 1 {
		return ErrUnavailable
	}
	return nil
}

// LockAndVerifyGrant rechecks current authorization while locking the grant
// row in the caller's transaction. Revoke takes the same row lock through its
// UPDATE, so revocation and authorized mutations have a serial order.
func LockAndVerifyGrant(ctx context.Context, tx *gorm.DB, issuer, subject, owner, tenant string) error {
	if tx == nil || !validIdentity(issuer, subject) || owner != subject || !canonicalName.MatchString(owner) || !canonicalName.MatchString(tenant) {
		return ErrUnavailable
	}
	var row grantRecord
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", hashFields(issuer, subject)).First(&row).Error; err != nil || !row.Enabled || row.Owner != owner || row.Tenant != tenant {
		return ErrUnavailable
	}
	return nil
}
