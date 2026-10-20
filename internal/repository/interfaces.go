package repository

import (
	"context"

	"nexus-gateway/internal/model"
)

// DataSourceRepository defines the interface for data source data operations
type DataSourceRepository interface {
	// Create a new data source
	Create(ctx context.Context, dataSource *model.DataSource) error

	// GetByID retrieves a data source by its UUID
	GetByID(ctx context.Context, id string) (*model.DataSource, error)

	// GetByName retrieves a data source by its name
	GetByName(ctx context.Context, name string) (*model.DataSource, error)

	// GetAll retrieves all data sources with optional filtering
	GetAll(ctx context.Context, status model.DataSourceStatus, limit, offset int) ([]*model.DataSource, int64, error)

	// Update updates an existing data source
	Update(ctx context.Context, dataSource *model.DataSource) error

	// Delete soft deletes a data source
	Delete(ctx context.Context, id string) error

	// Activate sets a data source status to active
	Activate(ctx context.Context, id string) error

	// Deactivate sets a data source status to inactive
	Deactivate(ctx context.Context, id string) error

	// SetError sets a data source status to error
	SetError(ctx context.Context, id string) error

	// GetActiveByType retrieves all active data sources of a specific type
	GetActiveByType(ctx context.Context, dbType model.DatabaseType) ([]*model.DataSource, error)

	// CountByStatus returns the count of data sources by status
	CountByStatus(ctx context.Context) (map[model.DataSourceStatus]int64, error)

	// MigrateCredentials encrypts legacy plaintext secrets and rotates old ciphertext.
	MigrateCredentials(ctx context.Context) (int64, error)
}
