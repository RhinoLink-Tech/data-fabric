package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrCredentialVaultUnavailable = errors.New("credential vault is unavailable")
	ErrPlaintextCredential        = errors.New("plaintext credential found; credential migration is required")
)

type dataSourceRepository struct {
	db    *gorm.DB
	vault *security.CredentialVault
}

var _ security.TokenStore = (*dataSourceRepository)(nil)

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
	if err := SourceManagementDB(ctx, r.db).Where("id = ?", id).First(&ds).Error; err != nil {
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

// LoadAccessToken reads the currently persisted OAuth access token. The
// repository decrypts the token using the configured credential vault.
func (r *dataSourceRepository) LoadAccessToken(ctx context.Context, dataSourceID string) (string, time.Time, error) {
	ds, err := r.GetByID(ctx, dataSourceID)
	if err != nil {
		return "", time.Time{}, err
	}
	if ds.Config.AdditionalProps == nil {
		return "", time.Time{}, nil
	}
	token, _ := ds.Config.AdditionalProps["access_token"].(string)
	rawExpiry, _ := ds.Config.AdditionalProps["access_token_expires_at"].(string)
	if rawExpiry == "" {
		return token, time.Time{}, nil
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, rawExpiry)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("parse stored access token expiry: %w", err)
	}
	return token, expiresAt, nil
}

// CompareAndSwapAccessToken persists a replacement token and expiry in one
// transaction. The row lock and expected-token check prevent concurrent
// gateway instances from overwriting a newer token with a stale refresh result.
func (r *dataSourceRepository) CompareAndSwapAccessToken(ctx context.Context, dataSourceID, expectedToken, replacement string, expiresAt time.Time) (bool, error) {
	if replacement == "" {
		return false, errors.New("replacement access token is required")
	}
	if !expiresAt.After(time.Now()) {
		return false, errors.New("replacement access token must not be expired")
	}

	swapped := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ds model.DataSource
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", dataSourceID).First(&ds).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrDataSourceNotFound
			}
			return err
		}
		if err := r.decryptDataSource(&ds); err != nil {
			return fmt.Errorf("decrypt data source %s credentials: %w", dataSourceID, err)
		}
		if ds.Config.AdditionalProps == nil {
			ds.Config.AdditionalProps = make(map[string]interface{})
		}
		currentToken, _ := ds.Config.AdditionalProps["access_token"].(string)
		if currentToken != expectedToken {
			return nil
		}

		ds.Config.AdditionalProps["access_token"] = replacement
		ds.Config.AdditionalProps["access_token_expires_at"] = expiresAt.UTC().Format(time.RFC3339Nano)
		stored, err := r.encryptDataSource(&ds)
		if err != nil {
			return err
		}
		result := tx.Model(&model.DataSource{}).Where("id = ?", dataSourceID).Update("config", stored.Config)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrDataSourceNotFound
		}
		swapped = true
		return nil
	})
	return swapped, err
}

func (r *dataSourceRepository) GetByName(ctx context.Context, tenantID, name string) (*model.DataSource, error) {
	var ds model.DataSource
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND name = ?", tenantID, name).First(&ds).Error; err != nil {
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

func (r *dataSourceRepository) GetAll(ctx context.Context, scope *AccessScope, status model.DataSourceStatus, limit, offset int) ([]*model.DataSource, int64, error) {
	var sources []*model.DataSource
	var total int64
	query := scope.apply(r.db.WithContext(ctx).Model(&model.DataSource{}))
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
	result := r.db.WithContext(ctx).Model(&model.DataSource{}).
		Where("id = ?", stored.ID).
		Select("name", "type", "config", "status", "owner_id", "tenant_id").
		Updates(stored)
	if err := r.checkMutationResult(ctx, stored.ID, result); err != nil {
		// The tenant/name constraint is the race-safe authority for uniqueness;
		// translate its violation so a rename collision is an actionable conflict
		// rather than a server failure (#516).
		if isDuplicateEntry(err) {
			return ErrDataSourceExists
		}
		return err
	}
	ds.UpdatedAt = stored.UpdatedAt
	return nil
}

// isDuplicateEntry reports whether err is the metadata database's unique-name
// violation (MySQL 1062, or SQLite's UNIQUE constraint failure).
func isDuplicateEntry(err error) bool {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}

func (r *dataSourceRepository) Delete(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.DataSource{})
	return r.checkMutationResult(ctx, id, result)
}
func (r *dataSourceRepository) Activate(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Model(&model.DataSource{}).Where("id = ?", id).Update("status", model.DataSourceStatusActive)
	return r.checkMutationResult(ctx, id, result)
}
func (r *dataSourceRepository) Deactivate(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Model(&model.DataSource{}).Where("id = ?", id).Update("status", model.DataSourceStatusInactive)
	return r.checkMutationResult(ctx, id, result)
}
func (r *dataSourceRepository) SetError(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Model(&model.DataSource{}).Where("id = ?", id).Update("status", model.DataSourceStatusError)
	return r.checkMutationResult(ctx, id, result)
}

func (r *dataSourceRepository) checkMutationResult(ctx context.Context, id string, result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}

	// Some databases report zero affected rows when an existing record already
	// has the requested values. Distinguish that case from a missing data source.
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.DataSource{}).Where("id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrDataSourceNotFound
	}
	return nil
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

func (r *dataSourceRepository) CountByStatus(ctx context.Context, scope *AccessScope) (map[model.DataSourceStatus]int64, error) {
	var results []struct {
		Status model.DataSourceStatus
		Count  int64
	}
	if err := scope.apply(r.db.WithContext(ctx).Model(&model.DataSource{})).
		Select("status, COUNT(*) as count").Group("status").Scan(&results).Error; err != nil {
		return nil, err
	}
	counts := make(map[model.DataSourceStatus]int64, len(results))
	for _, result := range results {
		counts[result.Status] = result.Count
	}
	return counts, nil
}
