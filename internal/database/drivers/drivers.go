package drivers

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/RhinoLink-Tech/data-fabric/internal/execution"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// DriverCategory categorizes drivers by their type
type DriverCategory string

const (
	CategoryRelational         DriverCategory = "relational"
	CategoryTableFormat        DriverCategory = "table_format"
	CategoryWarehouse          DriverCategory = "warehouse"
	CategoryObjectStorage      DriverCategory = "object_storage"
	CategoryOLAP               DriverCategory = "olap"
	CategoryCloudDataWarehouse DriverCategory = "cloud_data_warehouse"
	CategoryQueryEngine        DriverCategory = "query_engine"
	CategoryDomesticDatabase   DriverCategory = "domestic_database"
	CategoryFileSystem         DriverCategory = "file_system"
	CategoryNoSQL              DriverCategory = "nosql"
)

// DriverCapabilities defines what operations a driver supports
type DriverCapabilities struct {
	SupportsSQL               bool
	SupportsTransaction       bool
	SupportsSchemaDiscovery   bool
	SupportsTimeTravel        bool
	RequiresTokenRotation     bool
	SupportsStreaming         bool
	SupportsSnapshotDiscovery bool
	SupportsIncrementalRead   bool
}

// DriverBase provides common functionality for all drivers
type DriverBase struct {
	dbType   model.DatabaseType
	category DriverCategory
}

func NewDriverBase(dbType model.DatabaseType, category DriverCategory) *DriverBase {
	return &DriverBase{dbType: dbType, category: category}
}

func (db *DriverBase) GetDatabaseTypeName() string {
	return string(db.dbType)
}

func (db *DriverBase) GetDriverName() string {
	return string(db.dbType)
}

func (db *DriverBase) GetCategory() DriverCategory {
	return db.category
}

// Driver describes source identity and configuration. SQL connection behavior is
// optional so native API, object, and file executors need not implement a
// database/sql compatibility layer just to enter the registry.
type Driver interface {
	ValidateDSN(dsn string) error
	GetDefaultPort() int
	BuildDSN(config *model.DataSourceConfig) string
	GetDatabaseTypeName() string
	GetDriverName() string
	GetCategory() DriverCategory
	GetCapabilities() DriverCapabilities
	ConfigureAuth(authConfig interface{}) error
}

// SQLDatabaseOpener opens a database/sql compatibility connection.
type SQLDatabaseOpener interface {
	Open(dsn string) (*sql.DB, error)
}

// SQLConnectionTester validates a database/sql connection.
type SQLConnectionTester interface {
	TestConnection(db *sql.DB) error
}

// SQLQueryPaginater applies dialect-specific LIMIT/OFFSET pagination.
type SQLQueryPaginater interface {
	ApplyBatchPagination(query string, batchSize, offset int64) (string, error)
}

// SQLDriver is the optional legacy SQL connection extension implemented by
// drivers that use database/sql for query execution.
type SQLDriver interface {
	Driver
	SQLDatabaseOpener
	SQLConnectionTester
	SQLQueryPaginater
}

// NativeAPIHealthChecker provides a protocol-native connectivity probe.
type NativeAPIHealthChecker interface {
	CheckNativeAPIConnection(context.Context, *model.DataSourceConfig) error
}

// NativeAPIExecutorProvider is an optional extension for drivers whose native
// protocol should bypass the database/sql compatibility path.
type NativeAPIExecutorProvider interface {
	OpenNativeAPIExecutor(context.Context, *model.DataSourceConfig) (execution.NativeAPIExecutor, error)
}

// DriverFactory creates database drivers for different database types
type DriverFactory struct{}

// NewDriverFactory creates a new DriverFactory instance
func NewDriverFactory() *DriverFactory {
	return &DriverFactory{}
}

// CreateDriver creates a database driver for the specified database type
func (df *DriverFactory) CreateDriver(dbType model.DatabaseType) (Driver, error) {
	// This is now handled by DriverRegistry, keeping for backward compatibility
	return nil, fmt.Errorf("use DriverRegistry instead of DriverFactory")
}
