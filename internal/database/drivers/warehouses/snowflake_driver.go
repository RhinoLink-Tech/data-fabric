package warehouses

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"time"

	// "github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"  // 移除metadata包引用以防止循环依赖
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// SnowflakeDriver implements Driver interface for Snowflake data warehouse
type SnowflakeDriver struct {
	config     *SnowflakeConfig
	typeMapper *SnowflakeTypeMapper
}

// NewSnowflakeDriver creates a new Snowflake driver
func NewSnowflakeDriver(config *SnowflakeConfig) (*SnowflakeDriver, error) {
	if err := config.ValidateConfig(); err != nil {
		return nil, err
	}

	return &SnowflakeDriver{
		config:     config,
		typeMapper: NewSnowflakeTypeMapper(),
	}, nil
}

// Open opens a connection to Snowflake
func (d *SnowflakeDriver) Open(dsn string) (*sql.DB, error) {
	// Use gosnowflake driver
	db, err := sql.Open("snowflake", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open Snowflake connection: %w", err)
	}

	// Set connection parameters
	if err := d.configureConnection(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// configureConnection sets connection parameters
func (d *SnowflakeDriver) configureConnection(db *sql.DB) error {
	// Set session parameters
	params := d.config.GetSessionParameters()
	for key, value := range params {
		_, err := db.Exec(fmt.Sprintf("ALTER SESSION SET %s = %s", key, value))
		if err != nil {
			// Log warning but don't fail connection
			continue
		}
	}

	return nil
}

// ValidateDSN validates the connection string
func (d *SnowflakeDriver) ValidateDSN(dsn string) error {
	if dsn == "" {
		return fmt.Errorf("DSN cannot be empty")
	}

	// Try to parse the DSN
	config, err := ParseConnectionString("snowflake://" + dsn)
	if err != nil {
		return fmt.Errorf("invalid DSN format: %w", err)
	}

	return config.ValidateConfig()
}

// GetDefaultPort returns the default Snowflake port
func (d *SnowflakeDriver) GetDefaultPort() int {
	return 443
}

// BuildDSN builds a gosnowflake DSN (user:password@host/database) from the
// supplied config. It deliberately does NOT touch d.config: the driver registry
// hands back a zero-value SnowflakeDriver (d.config == nil), and dereferencing
// it here panicked every BuildDSN call for registry-created Snowflake sources.
func (d *SnowflakeDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	return fmt.Sprintf("%s:%s@%s/%s", config.Username, config.Password, config.Host, config.Database)
}

// GetDatabaseTypeName returns the database type name
func (d *SnowflakeDriver) GetDatabaseTypeName() string {
	return "snowflake"
}

// TestConnection tests if the connection is working
func (d *SnowflakeDriver) TestConnection(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return db.PingContext(ctx)
}

// GetDriverName returns the underlying driver name
func (d *SnowflakeDriver) GetDriverName() string {
	return "snowflake-gosnowflake"
}

// GetCategory returns the driver category
func (d *SnowflakeDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryWarehouse
}

// GetCapabilities returns driver capabilities
func (d *SnowflakeDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     true,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      true, // Time travel via AS OF
		RequiresTokenRotation:   false,
		SupportsStreaming:       true,
	}
}

// ConfigureAuth configures authentication
func (d *SnowflakeDriver) ConfigureAuth(authConfig interface{}) error {
	// Snowflake supports basic auth, OAuth, key pair, etc.
	return nil
}

// =============================================================================
// Snowflake-Specific Methods
// =============================================================================

// GetSchema retrieves schema information
// 注意：此方法已被注释掉以防止循环依赖问题
// func (d *SnowflakeDriver) GetSchema(ctx context.Context, db *sql.DB, schemaName string) (*metadata.DataSourceSchema, error) {
// 	schema := &metadata.DataSourceSchema{
// 		Tables: make(map[string]*metadata.TableSchema),
// 	}
// 	return schema, nil
// }

// ExecuteQuery executes a query with Snowflake-specific handling
func (d *SnowflakeDriver) ExecuteQuery(ctx context.Context, db *sql.DB, sql string, params ...interface{}) (*sql.Rows, error) {
	// Handle VARIANT, ARRAY, OBJECT types properly
	rows, err := db.QueryContext(ctx, sql, params...)
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %w", err)
	}

	return rows, nil
}

// GetWarehouseInfo retrieves warehouse information
func (d *SnowflakeDriver) GetWarehouseInfo(ctx context.Context, db *sql.DB, warehouseName string) (*SnowflakeWarehouseInfo, error) {
	query := `
		SELECT
			name,
			state,
			type,
			size,
			min_cluster_count,
			max_cluster_count,
			autoscaling_enabled,
			running,
			queued,
			is_current
		FROM TABLE(INFORMATION_SCHEMA.WAREHOUSES_WAREHOUSE_INFO())
		WHERE name = ?
	`

	var info SnowflakeWarehouseInfo
	err := db.QueryRowContext(ctx, query, warehouseName).Scan(
		&info.Name,
		&info.State,
		&info.Type,
		&info.Size,
		&info.MinClusters,
		&info.MaxClusters,
		&info.AutoscalingEnabled,
		&info.Running,
		&info.Queued,
		&info.IsCurrent,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get warehouse info: %w", err)
	}

	return &info, nil
}

// SnowflakeWarehouseInfo contains warehouse information
type SnowflakeWarehouseInfo struct {
	Name               string
	State              string
	Type               string
	Size               string
	MinClusters        int
	MaxClusters        int
	AutoscalingEnabled bool
	Running            int
	Queued             int
	IsCurrent          bool
}

// SetWarehouseSize changes the warehouse size
func (d *SnowflakeDriver) SetWarehouseSize(ctx context.Context, db *sql.DB, warehouseName, newSize string) error {
	if !ValidateWarehouseSize(newSize) {
		return fmt.Errorf("invalid warehouse size: %s", newSize)
	}

	query := fmt.Sprintf("ALTER WAREHOUSE %s SET WAREHOUSE_SIZE = %s", warehouseName, newSize)
	_, err := db.ExecContext(ctx, query)
	return err
}

// ResumeWarehouse resumes a suspended warehouse
func (d *SnowflakeDriver) ResumeWarehouse(ctx context.Context, db *sql.DB, warehouseName string) error {
	query := fmt.Sprintf("ALTER WAREHOUSE %s RESUME IF SUSPENDED", warehouseName)
	_, err := db.ExecContext(ctx, query)
	return err
}

// SuspendWarehouse suspends a warehouse
func (d *SnowflakeDriver) SuspendWarehouse(ctx context.Context, db *sql.DB, warehouseName string) error {
	query := fmt.Sprintf("ALTER WAREHOUSE %s SUSPEND", warehouseName)
	_, err := db.ExecContext(ctx, query)
	return err
}

// QueryHistory retrieves query history
func (d *SnowflakeDriver) QueryHistory(ctx context.Context, db *sql.DB, limit int) ([]SnowflakeQueryHistory, error) {
	query := `
		SELECT
			QUERY_ID,
			QUERY_TEXT,
			EXECUTION_STATUS,
			START_TIME,
			END_TIME,
			TOTAL_ELAPSED_TIME,
			WAREHOUSE_SIZE,
			WAREHOUSE_TYPE
		FROM TABLE(INFORMATION_SCHEMA.QUERY_HISTORY(
			RESULT_LIMIT => ?
		))
		ORDER BY START_TIME DESC
	`

	rows, err := db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []SnowflakeQueryHistory
	for rows.Next() {
		var h SnowflakeQueryHistory
		err := rows.Scan(
			&h.QueryID,
			&h.QueryText,
			&h.Status,
			&h.StartTime,
			&h.EndTime,
			&h.ElapsedTime,
			&h.WarehouseSize,
			&h.WarehouseType,
		)
		if err != nil {
			continue
		}
		history = append(history, h)
	}

	return history, nil
}

// SnowflakeQueryHistory contains query history information
type SnowflakeQueryHistory struct {
	QueryID       string
	QueryText     string
	Status        string
	StartTime     time.Time
	EndTime       time.Time
	ElapsedTime   int64
	WarehouseSize string
	WarehouseType string
}

// GetTableStorageInfo retrieves storage information for tables
func (d *SnowflakeDriver) GetTableStorageInfo(ctx context.Context, db *sql.DB, tableName string) (*SnowflakeTableStorageInfo, error) {
	query := `
		SELECT
			TABLE_NAME,
			TABLE_SCHEMA,
			BYTES,
			ROW_COUNT,
			TABLE_TYPE
		FROM INFORMATION_SCHEMA.TABLES
		WHERE TABLE_NAME = ?
	`

	var info SnowflakeTableStorageInfo
	err := db.QueryRowContext(ctx, query, tableName).Scan(
		&info.TableName,
		&info.Schema,
		&info.Bytes,
		&info.RowCount,
		&info.TableType,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get storage info: %w", err)
	}

	return &info, nil
}

// SnowflakeTableStorageInfo contains table storage information
type SnowflakeTableStorageInfo struct {
	TableName string
	Schema    string
	Bytes     int64
	RowCount  int64
	TableType string
}

// SupportQueryAcceleration checks if query acceleration is available
func (d *SnowflakeDriver) SupportQueryAcceleration() bool {
	// Snowflake has built-in query acceleration (result caching)
	return true
}

// GetAccountInfo retrieves account information
func (d *SnowflakeDriver) GetAccountInfo(ctx context.Context, db *sql.DB) (*SnowflakeAccountInfo, error) {
	query := `
		SELECT
			CURRENT_ACCOUNT() AS account,
			CURRENT_REGION() AS region,
			CURRENT_WAREHOUSE() AS warehouse,
			CURRENT_DATABASE() AS database,
			CURRENT_SCHEMA() AS schema,
			CURRENT_ROLE() AS role
	`

	var info SnowflakeAccountInfo
	err := db.QueryRowContext(ctx, query).Scan(
		&info.Account,
		&info.Region,
		&info.Warehouse,
		&info.Database,
		&info.Schema,
		&info.Role,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get account info: %w", err)
	}

	return &info, nil
}

// ApplyBatchPagination applies pagination to a SQL query for batch processing
func (d *SnowflakeDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	// Snowflake uses LIMIT and OFFSET for pagination
	if batchSize <= 0 {
		return "", fmt.Errorf("batch size must be greater than 0")
	}

	// Check if the SQL already has LIMIT clause
	// For simplicity, we'll append LIMIT and OFFSET
	limitOffsetClause := fmt.Sprintf(" LIMIT %d OFFSET %d", batchSize, offset)
	return sql + limitOffsetClause, nil
}

// SnowflakeAccountInfo contains account information
type SnowflakeAccountInfo struct {
	Account   string
	Region    string
	Warehouse string
	Database  string
	Schema    string
	Role      string
}
