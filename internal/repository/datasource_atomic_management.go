package repository

import (
	"context"
	"errors"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"gorm.io/gorm"
)

// SourceWriteScope is an authenticated caller's write authority. Admins may
// manage legacy rows; other callers must match both owner and tenant.
type SourceWriteScope struct {
	OwnerID, TenantID string
	Admin             bool
}
type SourceMutationResult struct {
	ID     string
	Name   string
	Status model.DataSourceStatus
}
type AtomicSourceManagement interface {
	RenameSourceScoped(context.Context, SourceWriteScope, string, string) (*SourceMutationResult, error)
	SetSourceStatusScoped(context.Context, SourceWriteScope, string, model.DataSourceStatus) error
}

func (r *dataSourceRepository) mutateSourceScoped(ctx context.Context, scope SourceWriteScope, id string, values map[string]any) (*SourceMutationResult, error) {
	if !scope.Admin && scope.OwnerID == "" {
		return nil, ErrDataSourceNotFound
	}
	var output SourceMutationResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scoped := func(q *gorm.DB) *gorm.DB {
			q = q.Model(&model.DataSource{}).Where("id = ?", id)
			if !scope.Admin {
				// Identity and tenant claims are opaque bytes: MySQL's default
				// collations can ignore case, accents, or trailing spaces.
				switch tx.Dialector.Name() {
				case "mysql":
					q = q.Where("CAST(owner_id AS BINARY) = CAST(? AS BINARY) AND CAST(tenant_id AS BINARY) = CAST(? AS BINARY)", scope.OwnerID, scope.TenantID)
				case "sqlite":
					q = q.Where("CAST(owner_id AS BLOB) = CAST(? AS BLOB) AND CAST(tenant_id AS BLOB) = CAST(? AS BLOB)", scope.OwnerID, scope.TenantID)
				default:
					q = q.Where("1 = 0")
				}
			}
			return q
		}
		// The database acquires the row lock for the conditional update and keeps
		// it until the subsequent read, including when the requested value matches.
		result := scoped(tx).Updates(values)
		if result.Error != nil {
			if isDuplicateEntry(result.Error) {
				return ErrDataSourceExists
			}
			return result.Error
		}
		// A matching row may report zero changed rows in MySQL. Recheck with the
		// same authorization predicate in this transaction, never by ID alone.
		if err := scoped(tx).Select("id", "name", "status").Take(&output).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrDataSourceNotFound
			}
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &output, nil
}
func (r *dataSourceRepository) RenameSourceScoped(ctx context.Context, scope SourceWriteScope, id, name string) (*SourceMutationResult, error) {
	return r.mutateSourceScoped(ctx, scope, id, map[string]any{"name": name})
}
func (r *dataSourceRepository) SetSourceStatusScoped(ctx context.Context, scope SourceWriteScope, id string, status model.DataSourceStatus) error {
	_, err := r.mutateSourceScoped(ctx, scope, id, map[string]any{"status": status})
	return err
}
