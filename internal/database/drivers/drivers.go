package drivers

import (
	"database/sql"
	"fmt"

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

// Driver interface
type Driver interface {
	ApplyBatchPagination(sql string, batchSize, offset int64) (string, error)
	// Open opens a database connection
	Open(dsn string) (*sql.DB, error)

	// ValidateDSN validates the connection string
	ValidateDSN(dsn string) error

	// GetDefaultPort returns the default port for the database
	GetDefaultPort() int

	// BuildDSN builds a connection string from configuration
	BuildDSN(config *model.DataSourceConfig) string

	// GetDatabaseTypeName returns the database type name
	GetDatabaseTypeName() string

	// TestConnection tests if the connection is working
	TestConnection(db *sql.DB) error

	// GetDriverName returns the underlying SQL driver name
	GetDriverName() string

	// GetCategory returns the driver category
	GetCategory() DriverCategory

	// GetCapabilities returns driver capabilities
	GetCapabilities() DriverCapabilities

	// ConfigureAuth configures authentication (for IAM/OAuth2/Kerberos)
	ConfigureAuth(authConfig interface{}) error
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
