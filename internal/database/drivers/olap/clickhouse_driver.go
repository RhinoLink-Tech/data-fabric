package olap

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// ClickHouseDriver implements Driver interface for ClickHouse
type ClickHouseDriver struct {
	config *ClickHouseConfig
}

// NewClickHouseDriver creates a new ClickHouse driver
func NewClickHouseDriver(config *ClickHouseConfig) (*ClickHouseDriver, error) {
	if config.Host == "" {
		return nil, fmt.Errorf("host is required")
	}

	return &ClickHouseDriver{
		config: config,
	}, nil
}

// ApplyBatchPagination applies pagination to SQL query for batch processing
func (d *ClickHouseDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", sql, batchSize, offset), nil
}

// Open opens a connection to ClickHouse
func (d *ClickHouseDriver) Open(dsn string) (*sql.DB, error) {
	// Use sql.Open with clickhouse driver
	db, err := sql.Open("clickhouse", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open ClickHouse connection: %w", err)
	}
	return db, nil
}

// ValidateDSN validates the connection string
func (d *ClickHouseDriver) ValidateDSN(dsn string) error {
	if dsn == "" {
		return fmt.Errorf("DSN cannot be empty")
	}
	return nil
}

// GetDefaultPort returns the default ClickHouse port
func (d *ClickHouseDriver) GetDefaultPort() int {
	return 9000 // Native protocol port
}

// BuildDSN builds a connection string from configuration
func (d *ClickHouseDriver) BuildDSN(config *model.DataSourceConfig) string {
	return fmt.Sprintf("clickhouse://%s:%s@%s:%d/%s",
		config.Username, config.Password, config.Host, config.Port, config.Database)
}

// GetDatabaseTypeName returns the database type name
func (d *ClickHouseDriver) GetDatabaseTypeName() string {
	return "clickhouse"
}

// TestConnection tests if the connection is working
func (d *ClickHouseDriver) TestConnection(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return db.PingContext(ctx)
}

// GetDriverName returns the driver name
func (d *ClickHouseDriver) GetDriverName() string {
	return "clickhouse-native"
}

// GetCategory returns the driver category
func (d *ClickHouseDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryOLAP
}

// GetCapabilities returns driver capabilities
func (d *ClickHouseDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     false, // ClickHouse doesn't support traditional transactions
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       true,
	}
}

// ConfigureAuth configures authentication
func (d *ClickHouseDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// ExecuteQuery executes a ClickHouse query
func (d *ClickHouseDriver) ExecuteQuery(ctx context.Context, query string) (*ClickHouseQueryResult, error) {
	// Build DSN
	dsn := fmt.Sprintf("clickhouse://%s:%s@%s:%d/%s?database=%s",
		d.config.Username, d.config.Password, d.config.Host, d.config.Port, d.config.Database, d.config.Database)

	db, err := sql.Open("clickhouse", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open ClickHouse connection: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	defer rows.Close()

	// Get column information
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	// Read results
	var results []map[string]interface{}
	for rows.Next() {
		values := make([]interface{}, len(columns))
		pointers := make([]interface{}, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}

		if err := rows.Scan(pointers...); err != nil {
			continue
		}

		row := make(map[string]interface{})
		for i, col := range columns {
			row[col] = values[i]
		}
		results = append(results, row)
	}

	return &ClickHouseQueryResult{
		Rows:  results,
		Count: len(results),
	}, nil
}

// ClickHouseQueryResult represents ClickHouse query results
type ClickHouseQueryResult struct {
	Rows  []map[string]interface{}
	Count int
}

// GetTableSchema retrieves table schema from ClickHouse
func (d *ClickHouseDriver) GetTableSchema(ctx context.Context, tableName string) (*ClickHouseTableSchema, error) {
	query := fmt.Sprintf("DESCRIBE TABLE %s", tableName)
	result, err := d.ExecuteQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	schema := &ClickHouseTableSchema{
		TableName: tableName,
		Columns:   make([]ClickHouseColumn, 0),
	}

	for _, row := range result.Rows {
		col := ClickHouseColumn{
			Name:     row["name"].(string),
			Type:     row["type"].(string),
			Nullable: row["default_kind"] != nil,
		}
		schema.Columns = append(schema.Columns, col)
	}

	return schema, nil
}

// ClickHouseTableSchema represents ClickHouse table schema
type ClickHouseTableSchema struct {
	TableName string
	Columns   []ClickHouseColumn
}

// ClickHouseColumn represents a ClickHouse column
type ClickHouseColumn struct {
	Name     string
	Type     string
	Nullable bool
	Default  interface{}
}
