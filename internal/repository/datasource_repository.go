package repository

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"nexus-gateway/internal/model"
	"nexus-gateway/internal/security"
)

var (
	ErrCredentialVaultUnavailable = errors.New("credential vault is unavailable")
	ErrPlaintextCredential        = errors.New("plaintext credential found; credential migration is required")
)

type dataSourceRepository struct {
	db    *gorm.DB
	vault *security.CredentialVault
}

func NewDataSourceRepository(db *gorm.DB, vault *security.CredentialVault) (DataSourceRepository, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	if vault == nil {
		return nil, ErrCredentialVaultUnavailable
	}
	return &dataSourceRepository{db: db, vault: vault}, nil
}

func (r *dataSourceRepository) Create(ctx context.Context, ds *model.DataSource) error {
	stored, err := r.encryptDataSource(ds)
	if err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).Create(stored).Error; err != nil {
		return err
	}
	ds.ID, ds.CreatedAt, ds.UpdatedAt = stored.ID, stored.CreatedAt, stored.UpdatedAt
	return nil
}

func (r *dataSourceRepository) GetByID(ctx context.Context, id string) (*model.DataSource, error) {
	var ds model.DataSource
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&ds).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDataSourceNotFound
		}
		return nil, err
	}
	if err := r.decryptDataSource(&ds); err != nil {
		return nil, fmt.Errorf("decrypt data source %s credentials: %w", id, err)
	}
	return &ds, nil
}

func (r *dataSourceRepository) GetByName(ctx context.Context, name string) (*model.DataSource, error) {
	var ds model.DataSource
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&ds).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDataSourceNotFound
		}
		return nil, err
	}
	if err := r.decryptDataSource(&ds); err != nil {
		return nil, fmt.Errorf("decrypt data source %s credentials: %w", ds.ID, err)
	}
	return &ds, nil
}

func (r *dataSourceRepository) GetAll(ctx context.Context, status model.DataSourceStatus, limit, offset int) ([]*model.DataSource, int64, error) {
	var sources []*model.DataSource
	var total int64
	query := r.db.WithContext(ctx).Model(&model.DataSource{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Limit(limit).Offset(offset).Order("created_at DESC").Find(&sources).Error; err != nil {
		return nil, 0, err
	}
	for _, ds := range sources {
		if err := r.decryptDataSource(ds); err != nil {
			return nil, 0, fmt.Errorf("decrypt data source %s credentials: %w", ds.ID, err)
		}
	}
	return sources, total, nil
}

func (r *dataSourceRepository) Update(ctx context.Context, ds *model.DataSource) error {
	stored, err := r.encryptDataSource(ds)
	if err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).Save(stored).Error; err != nil {
		return err
	}
	ds.UpdatedAt = stored.UpdatedAt
	return nil
}

func (r *dataSourceRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.DataSource{}).Error
}
func (r *dataSourceRepository) Activate(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.DataSource{}).Where("id = ?", id).Update("status", model.DataSourceStatusActive).Error
}
func (r *dataSourceRepository) Deactivate(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.DataSource{}).Where("id = ?", id).Update("status", model.DataSourceStatusInactive).Error
}
func (r *dataSourceRepository) SetError(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&model.DataSource{}).Where("id = ?", id).Update("status", model.DataSourceStatusError).Error
}

func (r *dataSourceRepository) GetActiveByType(ctx context.Context, typ model.DatabaseType) ([]*model.DataSource, error) {
	var sources []*model.DataSource
	if err := r.db.WithContext(ctx).Where("type = ? AND status = ?", typ, model.DataSourceStatusActive).Find(&sources).Error; err != nil {
		return nil, err
	}
	for _, ds := range sources {
		if err := r.decryptDataSource(ds); err != nil {
			return nil, fmt.Errorf("decrypt data source %s credentials: %w", ds.ID, err)
		}
	}
	return sources, nil
}

// MigrateCredentials encrypts legacy plaintext data and rotates ciphertext in one transaction.
// Any corrupt envelope, unavailable old key, or database error rolls back the full migration.
func (r *dataSourceRepository) MigrateCredentials(ctx context.Context) (int64, error) {
	var migrated int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var sources []model.DataSource
		if err := tx.Unscoped().Order("id ASC").Find(&sources).Error; err != nil {
			return err
		}
		for i := range sources {
			stored, changed, err := r.encryptDataSourceWithChange(&sources[i])
			if err != nil {
				return fmt.Errorf("migrate data source %s credentials: %w", sources[i].ID, err)
			}
			if !changed {
				continue
			}
			if err := tx.Unscoped().Model(&model.DataSource{}).Where("id = ?", stored.ID).UpdateColumn("config", stored.Config).Error; err != nil {
				return fmt.Errorf("persist migrated credentials for data source %s: %w", stored.ID, err)
			}
			migrated++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return migrated, nil
}

func (r *dataSourceRepository) encryptDataSource(ds *model.DataSource) (*model.DataSource, error) {
	stored, _, err := r.encryptDataSourceWithChange(ds)
	return stored, err
}

func (r *dataSourceRepository) encryptDataSourceWithChange(ds *model.DataSource) (*model.DataSource, bool, error) {
	if ds == nil {
		return nil, false, errors.New("data source is required")
	}
	stored := *ds
	cfg := stored.Config
	changed := false
	if cfg.Password != "" {
		value, didChange, err := encryptSecret(r.vault, cfg.Password)
		if err != nil {
			return nil, false, fmt.Errorf("password: %w", err)
		}
		cfg.Password, changed = value, changed || didChange
	}
	props, propsChanged, err := transformSensitiveProperties(r.vault, cfg.AdditionalProps, true)
	if err != nil {
		return nil, false, fmt.Errorf("additional properties: %w", err)
	}
	cfg.AdditionalProps = props
	stored.Config = cfg
	return &stored, changed || propsChanged, nil
}

func (r *dataSourceRepository) decryptDataSource(ds *model.DataSource) error {
	if ds.Config.Password != "" {
		value, err := decryptSecret(r.vault, ds.Config.Password)
		if err != nil {
			return fmt.Errorf("password: %w", err)
		}
		ds.Config.Password = value
	}
	props, _, err := transformSensitiveProperties(r.vault, ds.Config.AdditionalProps, false)
	if err != nil {
		return fmt.Errorf("additional properties: %w", err)
	}
	ds.Config.AdditionalProps = props
	return nil
}

func encryptSecret(vault *security.CredentialVault, value string) (string, bool, error) {
	if !vault.IsEncrypted(value) {
		ciphertext, err := vault.EncryptCredentials([]byte(value))
		return ciphertext, err == nil, err
	}
	keyID, ok := vault.KeyID(value)
	if ok && keyID == vault.ActiveKeyID() {
		if _, err := vault.DecryptCredentials(value); err != nil {
			return "", false, err
		}
		return value, false, nil
	}
	ciphertext, err := vault.ReencryptCredentials(value)
	return ciphertext, err == nil, err
}

func decryptSecret(vault *security.CredentialVault, value string) (string, error) {
	plaintext, err := vault.DecryptCredentials(value)
	if errors.Is(err, security.ErrCredentialNotEncrypted) {
		return "", ErrPlaintextCredential
	}
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func transformSensitiveProperties(vault *security.CredentialVault, props map[string]interface{}, encrypt bool) (map[string]interface{}, bool, error) {
	if props == nil {
		return nil, false, nil
	}
	result, changed, err := transformPropertyValue(vault, props, false, encrypt)
	if err != nil {
		return nil, false, err
	}
	return result.(map[string]interface{}), changed, nil
}

func transformPropertyValue(vault *security.CredentialVault, value interface{}, force, encrypt bool) (interface{}, bool, error) {
	switch typed := value.(type) {
	case map[string]interface{}:
		result, changed := make(map[string]interface{}, len(typed)), false
		for key, child := range typed {
			out, didChange, err := transformPropertyValue(vault, child, force || model.IsSensitiveDataSourceProperty(key), encrypt)
			if err != nil {
				return nil, false, fmt.Errorf("%s: %w", key, err)
			}
			result[key], changed = out, changed || didChange
		}
		return result, changed, nil
	case []interface{}:
		result, changed := make([]interface{}, len(typed)), false
		for i, child := range typed {
			out, didChange, err := transformPropertyValue(vault, child, force, encrypt)
			if err != nil {
				return nil, false, fmt.Errorf("[%d]: %w", i, err)
			}
			result[i], changed = out, changed || didChange
		}
		return result, changed, nil
	case string:
		if !force || typed == "" {
			return typed, false, nil
		}
		if encrypt {
			return encryptSecret(vault, typed)
		}
		if !vault.IsEncrypted(typed) {
			return nil, false, ErrPlaintextCredential
		}
		plaintext, err := vault.DecryptCredentials(typed)
		if err != nil {
			return nil, false, err
		}
		return string(plaintext), false, nil
	default:
		return value, false, nil
	}
}

func (r *dataSourceRepository) CountByStatus(ctx context.Context) (map[model.DataSourceStatus]int64, error) {
	var results []struct {
		Status model.DataSourceStatus
		Count  int64
	}
	if err := r.db.WithContext(ctx).Model(&model.DataSource{}).
		Select("status, COUNT(*) as count").Group("status").Scan(&results).Error; err != nil {
		return nil, err
	}
	counts := make(map[model.DataSourceStatus]int64, len(results))
	for _, result := range results {
		counts[result.Status] = result.Count
	}
	return counts, nil
}
