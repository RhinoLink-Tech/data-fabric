package repository

import (
	"context"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// DataSourceRepository defines the interface for data source data operations
type DataSourceRepository interface {
	// Create a new data source
	Create(ctx context.Context, dataSource *model.DataSource) error

	// GetByID retrieves a data source by its UUID
	GetByID(ctx context.Context, id string) (*model.DataSource, error)

	// GetByName retrieves a data source by its name within a tenant bucket
	// (name uniqueness is per tenant). An empty tenantID is the default
	// bucket used by callers without a tenant claim.
	GetByName(ctx context.Context, tenantID, name string) (*model.DataSource, error)

	// GetAll retrieves data sources visible to the caller's scope with
	// optional status filtering. A nil scope sees everything (admin or
	// authorization-disabled mode).
	GetAll(ctx context.Context, scope *AccessScope, status model.DataSourceStatus, limit, offset int) ([]*model.DataSource, int64, error)

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

	// CountByStatus returns the count of data sources by status within the
	// caller's scope (nil scope counts everything)
	CountByStatus(ctx context.Context, scope *AccessScope) (map[model.DataSourceStatus]int64, error)

	// MigrateCredentials encrypts legacy plaintext secrets and rotates old ciphertext.
	MigrateCredentials(ctx context.Context) (int64, error)
}
