package domestic

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"

	_ "github.com/go-sql-driver/mysql"
)

// TDSQLDriver implements Driver interface for Tencent TDSQL
type TDSQLDriver struct {
	config *TDSQLConfig
}

// TDSQLConfig holds TDSQL configuration
type TDSQLConfig struct {
	Host           string
	Port           int
	Database       string
	Username       string
	Password       string
	ShardKey       string // Shard key for distributed queries
	ProxyHost      string // Proxy host for connection routing
	ProxyPort      int    // Proxy port
	ReadWriteSplit bool   // Enable read-write splitting
	Consistency    string // Strong, Eventual, Session
	Charset        string // utf8mb4, utf8, etc.
}

// NewTDSQLDriver creates a new TDSQL driver
func NewTDSQLDriver(config *TDSQLConfig) (*TDSQLDriver, error) {
	if config.Host == "" {
		return nil, fmt.Errorf("host is required")
	}

	return &TDSQLDriver{
		config: config,
	}, nil
}

// Open opens a connection to TDSQL
func (d *TDSQLDriver) Open(dsn string) (*sql.DB, error) {
	if err := d.ValidateDSN(dsn); err != nil {
		return nil, err
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open TDSQL connection: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping TDSQL: %w", err)
	}

	return db, nil
}

// ValidateDSN validates the connection string
func (d *TDSQLDriver) ValidateDSN(dsn string) error {
	if dsn == unsupportedDomesticTLSDSN {
		return fmt.Errorf("requested TLS configuration is unsupported by this driver")
	}
	if dsn == "" {
		return fmt.Errorf("DSN cannot be empty")
	}
	return nil
}

// GetDefaultPort returns the default TDSQL port
func (d *TDSQLDriver) GetDefaultPort() int {
	return 3306
}

// BuildDSN builds a connection string from configuration.
//
// The driver registry hands back a zero-value TDSQLDriver (d.config == nil);
// dereferencing d.config here panicked every registry-sourced BuildDSN call.
// The DSN is built from the supplied *model.DataSourceConfig, and the
// constructor-only tuning fields are consulted defensively.
func (d *TDSQLDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if unsupportedDomesticTLS(config, "") {
		return unsupportedDomesticTLSDSN
	}

	charset := "utf8mb4"
	if d.config != nil && d.config.Charset != "" {
		charset = d.config.Charset
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s",
		config.Username, config.Password, config.Host, config.Port, config.Database, charset)

	// Add read-write split parameters
	if d.config != nil && d.config.ReadWriteSplit {
		dsn += "&readWriteSplit=true"
	}

	// Add consistency level
	if d.config != nil && d.config.Consistency != "" {
		dsn += "&consistency=" + d.config.Consistency
	}

	return dsn
}

// GetDatabaseTypeName returns the database type name
func (d *TDSQLDriver) GetDatabaseTypeName() string {
	return "tdsql"
}

// TestConnection tests if the connection is working
func (d *TDSQLDriver) TestConnection(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return db.PingContext(ctx)
}

// GetDriverName returns the driver name
func (d *TDSQLDriver) GetDriverName() string {
	return "tdsql-mysql"
}

// GetCategory returns the driver category
func (d *TDSQLDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryRelational
}

// GetCapabilities returns driver capabilities
func (d *TDSQLDriver) GetCapabilities() drivers.DriverCapabilities {
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
func (d *TDSQLDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// GetShardInfo retrieves shard information for a table
func (d *TDSQLDriver) GetShardInfo(ctx context.Context, db *sql.DB, tableName string) (*TDSQLShardInfo, error) {
	sql := `
		SELECT TABLE_NAME, SHARD_KEY, SHARD_COUNT,
		       PARTITION_NAME, PARTITION_METHOD
		FROM information_schema.TDSQL_TABLES
		WHERE TABLE_NAME = ?
	`

	info := &TDSQLShardInfo{
		TableName: tableName,
	}

	err := db.QueryRowContext(ctx, sql, tableName).Scan(
		&info.TableName,
		&info.ShardKey,
		&info.ShardCount,
		&info.PartitionName,
		&info.PartitionMethod,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get shard info: %w", err)
	}

	return info, nil
}

// TDSQLShardInfo represents TDSQL shard information
type TDSQLShardInfo struct {
	TableName       string
	ShardKey        string
	ShardCount      int
	PartitionName   string
	PartitionMethod string
	ShardNodes      []string
}

// ExecuteDistributedQuery executes a distributed query with routing
func (d *TDSQLDriver) ExecuteDistributedQuery(ctx context.Context, db *sql.DB, sql string, shardKey string) (*TDSQLQueryResult, error) {
	rows, err := db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("failed to execute distributed query: %w", err)
	}
	defer rows.Close()

	columns, _ := rows.Columns()
	var results []map[string]interface{}

	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		rowMap := make(map[string]interface{})
		for i, col := range columns {
			rowMap[col] = values[i]
		}
		results = append(results, rowMap)
	}

	return &TDSQLQueryResult{
		Rows:     results,
		Columns:  columns,
		Count:    len(results),
		ShardKey: shardKey,
		IsRouted: shardKey != "",
	}, nil
}

// TDSQLQueryResult represents TDSQL query results
type TDSQLQueryResult struct {
	Rows     []map[string]interface{}
	Columns  []string
	Count    int
	ShardKey string
	IsRouted bool
}

// GetProxyStatus retrieves proxy status
func (d *TDSQLDriver) GetProxyStatus(ctx context.Context, db *sql.DB) (*TDSQLProxyStatus, error) {
	sql := "SELECT PROXY_HOST, PROXY_PORT, STATUS, CONNECTIONS FROM information_schema.TDSQL_PROXY"

	status := &TDSQLProxyStatus{}
	err := db.QueryRowContext(ctx, sql).Scan(
		&status.ProxyHost,
		&status.ProxyPort,
		&status.Status,
		&status.Connections,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get proxy status: %w", err)
	}

	return status, nil
}

// TDSQLProxyStatus represents TDSQL proxy status
type TDSQLProxyStatus struct {
	ProxyHost   string
	ProxyPort   int
	Status      string
	Connections int
	ActiveNodes []string
}

// ApplyBatchPagination applies pagination to a SQL query for batch processing
func (d *TDSQLDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	sql = strings.TrimSpace(sql)

	// Check if query already has LIMIT or OFFSET
	sqlUpper := strings.ToUpper(sql)
	if strings.Contains(sqlUpper, " LIMIT ") || strings.Contains(sqlUpper, " OFFSET ") {
		// For complex queries with existing pagination, add a subquery wrapper
		return fmt.Sprintf("SELECT * FROM (%s) AS batch_query LIMIT %d OFFSET %d", sql, batchSize, offset), nil
	}
	// Use standard LIMIT/OFFSET for MySQL-compatible databases
	if offset > 0 {
		return fmt.Sprintf("%s LIMIT %d OFFSET %d", sql, batchSize, offset), nil
	}
	return fmt.Sprintf("%s LIMIT %d", sql, batchSize), nil
}
