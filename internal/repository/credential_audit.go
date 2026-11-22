package repository

import (
	"context"
	"errors"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var ErrCredentialAudit = errors.New("stored data source credentials could not be verified")

// AuditStoredDataSourceCredentials checks every persisted source's sensitive
// fields using the supplied keyring. It does not rotate or write credentials.
// Errors intentionally omit source IDs, property names, ciphertext and keys.
func AuditStoredDataSourceCredentials(ctx context.Context, db *gorm.DB, vault *security.CredentialVault) (int64, error) {
	if db == nil || vault == nil {
		return 0, ErrCredentialAudit
	}
	repo := &dataSourceRepository{db: db, vault: vault}
	var sources []model.DataSource
	var count int64
	// A malformed stored configuration must not reach SQL/driver logs.
	auditDB := db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	err := auditDB.WithContext(ctx).Unscoped().Select("id", "config").Order("id ASC").
		FindInBatches(&sources, 100, func(_ *gorm.DB, _ int) error {
			for i := range sources {
				if err := repo.decryptDataSource(&sources[i]); err != nil {
					return ErrCredentialAudit
				}
				count++
			}
			return nil
		}).Error
	if err != nil {
		return 0, ErrCredentialAudit
	}
	return count, nil
}
