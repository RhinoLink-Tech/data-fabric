package repository

import (
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"

	"gorm.io/gorm"
)

// AccessScope narrows list and count queries to the data sources a non-admin
// caller may see (#9): own records and records of the caller's real tenant.
// A nil scope means no restriction (admin or authorization-disabled mode).
type AccessScope struct {
	OwnerID  string
	TenantID string
}

// ScopeForCaller derives the repository scope for a caller. Admins and the
// authorization-disabled nil caller get unrestricted access.
func ScopeForCaller(caller *security.Caller) *AccessScope {
	if caller == nil || caller.IsAdmin() {
		return nil
	}
	return &AccessScope{OwnerID: caller.UserID, TenantID: caller.TenantID}
}

// apply adds the scope predicate to a query. A caller without a tenant claim
// is not matched against the empty tenant: they see their own records, not
// other tenant-less users' records or legacy rows.
func (s *AccessScope) apply(query *gorm.DB) *gorm.DB {
	if s == nil {
		return query
	}
	if s.TenantID == "" {
		return query.Where("owner_id = ?", s.OwnerID)
	}
	return query.Where("owner_id = ? OR tenant_id = ?", s.OwnerID, s.TenantID)
}

// legacyNameIndex is the pre-#9 single-column unique index that made data
// source names globally unique.
const legacyNameIndex = "idx_data_sources_name"

// MigrateDataSourceSchema migrates the data_sources table to the #9 schema:
// ownership columns, the per-tenant name uniqueness, and removal of the
// pre-#9 globally unique name index. It is idempotent and runs at startup
// before any repository use.
func MigrateDataSourceSchema(db *gorm.DB) error {
	if err := db.AutoMigrate(&model.DataSource{}); err != nil {
		return err
	}
	return retireLegacyNameIndex(db)
}

// retireLegacyNameIndex drops the old single-column unique index on name.
// AutoMigrate never drops indexes; without this step the old index would keep
// rejecting same-name sources across tenants even after the composite
// (tenant_id, name) index exists.
func retireLegacyNameIndex(db *gorm.DB) error {
	migrator := db.Migrator()
	if !migrator.HasIndex(&model.DataSource{}, legacyNameIndex) {
		return nil
	}
	return migrator.DropIndex(&model.DataSource{}, legacyNameIndex)
}
