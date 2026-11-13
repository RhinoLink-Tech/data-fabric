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
	DeleteSourceScoped(context.Context, SourceWriteScope, string, string) error
}

func (r *dataSourceRepository) mutateSourceScoped(ctx context.Context, scope SourceWriteScope, id string, values map[string]any) (*SourceMutationResult, error) {
	if !scope.Admin && scope.OwnerID == "" {
		return nil, ErrDataSourceNotFound
	}
	var output SourceMutationResult
	err := SourceManagementDB(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		scoped := func(q *gorm.DB) *gorm.DB { return scopedSource(q, scope, id) }

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

// byteEqual uses binary comparison because the configured MySQL collation may
// ignore case, accents, or trailing spaces; fail closed for other dialects.
func byteEqual(q *gorm.DB, column, value string) *gorm.DB {
	switch q.Dialector.Name() {
	case "mysql":
		return q.Where("CAST("+column+" AS BINARY) = CAST(? AS BINARY)", value)
	case "sqlite":
		return q.Where("CAST("+column+" AS BLOB) = CAST(? AS BLOB)", value)
	default:
		return q.Where("1 = 0")
	}
}
func scopedSource(q *gorm.DB, scope SourceWriteScope, id string) *gorm.DB {
	q = q.Model(&model.DataSource{}).Where("id = ?", id)
	if !scope.Admin {
		q = byteEqual(q, "owner_id", scope.OwnerID)
		q = byteEqual(q, "tenant_id", scope.TenantID)
	}
	return q
}

// DeleteSourceScoped compares the displayed source name in the DELETE itself,
// so a rename or ownership change cannot redirect a confirmed deletion.
func (r *dataSourceRepository) DeleteSourceScoped(ctx context.Context, scope SourceWriteScope, id, expectedName string) error {
	// Deletion requires ownership in the exact tenant even for an admin.
	scope.Admin = false
	if scope.OwnerID == "" || expectedName == "" {
		return ErrDataSourceNotFound
	}
	q := scopedSource(SourceManagementDB(ctx, r.db), scope, id)
	result := byteEqual(q, "name", expectedName).Delete(&model.DataSource{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrDataSourceNotFound
	}
	return nil
}
