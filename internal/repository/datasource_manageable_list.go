package repository

import (
	"context"
	"errors"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// ManageableSource is a non-secret projection. Owner and tenant are retained
// internally for the caller-relative ownership indicator and never sent out.
type ManageableSource struct {
	ID, Name, OwnerID, TenantID string
	Type                        model.DatabaseType
	Status                      model.DataSourceStatus
}

// ManageableSourceLister is a filtered inventory, not an authorization check.
// Callers must separately enforce the management scope and current grant.
type ManageableSourceLister interface {
	ListManageableSources(context.Context, SourceWriteScope, int, int) ([]ManageableSource, int64, error)
}

func (r *dataSourceRepository) ListManageableSources(ctx context.Context, scope SourceWriteScope, limit, offset int) ([]ManageableSource, int64, error) {
	if scope.OwnerID == "" || limit < 1 || limit > 1000 || offset < 0 || offset > 2147482647 {
		return nil, 0, errors.New("invalid source-management inventory scope or page")
	}
	query := SourceManagementDB(ctx, r.db).Model(&model.DataSource{})
	if !scope.Admin {
		query = byteEqual(query, "owner_id", scope.OwnerID)
		query = byteEqual(query, "tenant_id", scope.TenantID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var sources []ManageableSource
	if err := query.Select("id", "name", "type", "status", "owner_id", "tenant_id").
		Order("created_at DESC").Order("id DESC").Limit(limit).Offset(offset).Scan(&sources).Error; err != nil {
		return nil, 0, err
	}
	return sources, total, nil
}
