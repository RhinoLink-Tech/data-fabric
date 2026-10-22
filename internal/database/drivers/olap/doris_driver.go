package olap

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"

	_ "github.com/go-sql-driver/mysql"
)

// DorisDriver implements Driver interface for Apache Doris
type DorisDriver struct {
	config *DorisConfig
}

// DorisConfig holds Apache Doris configuration
type DorisConfig struct {
	Host     string
	Port     int
	Database string
	Username string
	Password string
}

// NewDorisDriver creates a new Apache Doris driver
func NewDorisDriver(config *DorisConfig) (*DorisDriver, error) {
	if config.Host == "" {
		return nil, fmt.Errorf("host is required")
	}

	return &DorisDriver{
		config: config,
	}, nil
}

// Open opens a connection to Apache Doris (MySQL-compatible)
func (d *DorisDriver) Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open Doris connection: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping Doris: %w", err)
	}

	return db, nil
}

// ValidateDSN validates the connection string
func (d *DorisDriver) ValidateDSN(dsn string) error {
	if dsn == "" {
		return fmt.Errorf("DSN cannot be empty")
	}
	return nil
}

// GetDefaultPort returns the default Doris port
func (d *DorisDriver) GetDefaultPort() int {
	return 9030 // FE query port
}

// BuildDSN builds a connection string from configuration
func (d *DorisDriver) BuildDSN(config *model.DataSourceConfig) string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
		config.Username, config.Password, config.Host, config.Port, config.Database)
}

// GetDatabaseTypeName returns the database type name
func (d *DorisDriver) GetDatabaseTypeName() string {
	return "doris"
}

// TestConnection tests if the connection is working
func (d *DorisDriver) TestConnection(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return db.PingContext(ctx)
}

// GetDriverName returns the driver name
func (d *DorisDriver) GetDriverName() string {
	return "doris-mysql"
}

// GetCategory returns the driver category
func (d *DorisDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryOLAP
}

// GetCapabilities returns driver capabilities
func (d *DorisDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     true,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       false,
	}
}

// ConfigureAuth configures authentication
func (d *DorisDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// ApplyBatchPagination applies pagination to SQL query for batch processing
func (d *DorisDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", sql, batchSize, offset), nil
}

// GetTableSchema retrieves table schema from Doris
func (d *DorisDriver) GetTableSchema(ctx context.Context, db *sql.DB, tableName string) (*DorisTableSchema, error) {
	sql := fmt.Sprintf("DESCRIBE %s", tableName)
	rows, err := db.QueryContext(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	schema := &DorisTableSchema{
		TableName: tableName,
		Columns:   make([]DorisColumn, 0),
	}

	for rows.Next() {
		var field, typeStr, null, key, defaultVal, extra *string
		err := rows.Scan(&field, &typeStr, &null, &key, &defaultVal, &extra)
		if err != nil {
			return nil, err
		}

		schema.Columns = append(schema.Columns, DorisColumn{
			Name:     field,
			Type:     typeStr,
			Nullable: null != nil && *null == "YES",
		})
	}

	return schema, nil
}

// DorisTableSchema represents Doris table schema
type DorisTableSchema struct {
	TableName string
	Columns   []DorisColumn
}

// DorisColumn represents a Doris column
type DorisColumn struct {
	Name     *string
	Type     *string
	Nullable bool
	Key      *string
	Default  *string
}
