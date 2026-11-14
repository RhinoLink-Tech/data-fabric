package repository

import (
	"context"
	"errors"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// LockedSourceUpdater makes the callback and field-specific persistence one
// transaction against the same row locked by token rotation.
type LockedSourceUpdater interface {
	UpdateSourceLocked(context.Context, string, []string, func(*model.DataSource) error) (*model.DataSource, error)
}

func (r *dataSourceRepository) UpdateSourceLocked(ctx context.Context, id string, fields []string, mutate func(*model.DataSource) error) (*model.DataSource, error) {
	for _, field := range fields {
		if field != "name" && field != "config" && field != "status" {
			return nil, errors.New("unsupported source update field")
		}
	}
	if mutate == nil {
		return nil, errors.New("source update callback is required")
	}
	var ds model.DataSource
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&ds).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrDataSourceNotFound
		}
		if err != nil {
			return err
		}
		if err = r.decryptDataSource(&ds); err != nil {
			return err
		}
		if err = mutate(&ds); err != nil {
			return err
		}
		if len(fields) == 0 {
			return nil
		}
		stored, err := r.encryptDataSource(&ds)
		if err != nil {
			return err
		}
		result := tx.Model(&model.DataSource{}).Where("id = ?", id).Select(fields).Updates(stored)
		if result.Error != nil {
			if isDuplicateEntry(result.Error) {
				return ErrDataSourceExists
			}
			return result.Error
		}
		ds.UpdatedAt = stored.UpdatedAt
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &ds, nil
}

// LockedSourceMutator applies authorization to the same row locked for the
// status change or deletion, avoiding a check-then-write race.
type LockedSourceMutator interface {
	MutateSourceLocked(context.Context, string, *model.DataSourceStatus, func(*model.DataSource) error) error
}

func (r *dataSourceRepository) MutateSourceLocked(ctx context.Context, id string, status *model.DataSourceStatus, authorize func(*model.DataSource) error) error {
	if authorize == nil {
		return errors.New("source authorization callback is required")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ds model.DataSource
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "owner_id", "tenant_id").Where("id = ?", id).First(&ds).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrDataSourceNotFound
		}
		if err != nil {
			return err
		}
		if err = authorize(&ds); err != nil {
			return err
		}
		if status == nil {
			return tx.Where("id = ?", id).Delete(&model.DataSource{}).Error
		}
		return tx.Model(&model.DataSource{}).Where("id = ?", id).Update("status", *status).Error
	})
}
