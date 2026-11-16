package domestic

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"

	_ "github.com/lib/pq"
)

// OscarDriver implements Driver interface for Oscar (ShenTong)
type OscarDriver struct {
	config *OscarConfig
}

// OscarConfig holds Oscar database configuration
type OscarConfig struct {
	Host        string
	Port        int
	Database    string
	Username    string
	Password    string
	Schema      string
	Charset     string // UTF8, GB18030
	ClusterMode bool   // Enable cluster mode
	NodeCount   int    // Number of nodes in cluster
}

// NewOscarDriver creates a new Oscar driver
func NewOscarDriver(config *OscarConfig) (*OscarDriver, error) {
	if config.Host == "" {
		return nil, fmt.Errorf("host is required")
	}

	return &OscarDriver{
		config: config,
	}, nil
}

// Open opens a connection to Oscar (PostgreSQL-compatible)
func (d *OscarDriver) Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open Oscar connection: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping Oscar: %w", err)
	}

	return db, nil
}

// ValidateDSN validates the connection string
func (d *OscarDriver) ValidateDSN(dsn string) error {
	if dsn == "" {
		return fmt.Errorf("DSN cannot be empty")
	}
	return nil
}

// GetDefaultPort returns the default Oscar port
func (d *OscarDriver) GetDefaultPort() int {
	return 2003
}

// BuildDSN builds a connection string from configuration.
//
// The driver registry hands back a zero-value OscarDriver (d.config == nil);
// dereferencing d.config here panicked every registry-sourced BuildDSN call.
// The DSN is built from the supplied *model.DataSourceConfig, and the
// constructor-only tunings are consulted defensively.
func (d *OscarDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}

	charset := "UTF8"
	if d.config != nil && d.config.Charset != "" {
		charset = d.config.Charset
	}

	dsn := fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=disable client_encoding=%s",
		config.Host, config.Port, config.Database, config.Username, config.Password, charset)

	// Add schema if specified
	if d.config != nil && d.config.Schema != "" {
		dsn += " search_path=" + d.config.Schema
	}

	return dsn
}

// GetDatabaseTypeName returns the database type name
func (d *OscarDriver) GetDatabaseTypeName() string {
	return "oscar"
}

// TestConnection tests if the connection is working
func (d *OscarDriver) TestConnection(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return db.PingContext(ctx)
}

// GetDriverName returns the driver name
func (d *OscarDriver) GetDriverName() string {
	return "oscar-pg"
}

// GetCategory returns the driver category
func (d *OscarDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryRelational
}

// GetCapabilities returns driver capabilities
func (d *OscarDriver) GetCapabilities() drivers.DriverCapabilities {
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
func (d *OscarDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// GetDatabaseInfo retrieves Oscar database information
func (d *OscarDriver) GetDatabaseInfo(ctx context.Context, db *sql.DB) (*OscarDatabaseInfo, error) {
	var version string
	err := db.QueryRowContext(ctx, "SELECT version()").Scan(&version)
	if err != nil {
		return nil, fmt.Errorf("failed to get version: %w", err)
	}

	info := &OscarDatabaseInfo{
		Version:     version,
		ClusterMode: d.config.ClusterMode,
	}

	return info, nil
}

// OscarDatabaseInfo represents Oscar database information
type OscarDatabaseInfo struct {
	Version     string
	ClusterMode bool
	NodeCount   int
	Charset     string
}

// GetClusterStatus retrieves cluster status
func (d *OscarDriver) GetClusterStatus(ctx context.Context, db *sql.DB) (*OscarClusterStatus, error) {
	if !d.config.ClusterMode {
		return nil, fmt.Errorf("cluster mode is not enabled")
	}

	sql := `
		SELECT NODE_ID, NODE_NAME, NODE_ROLE,
		       STATUS, CURRENT_LOAD
		FROM SYSTEM_CLUSTER_NODES
	`

	rows, err := db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster status: %w", err)
	}
	defer rows.Close()

	status := &OscarClusterStatus{
		Nodes: []OscarNode{},
	}

	for rows.Next() {
		var node OscarNode
		err := rows.Scan(
			&node.NodeID,
			&node.NodeName,
			&node.NodeRole,
			&node.Status,
			&node.CurrentLoad,
		)
		if err != nil {
			return nil, err
		}
		status.Nodes = append(status.Nodes, node)
	}

	return status, nil
}

// OscarClusterStatus and OscarNode are defined in oscar_cluster.go

// ExecuteDistributedQuery executes a distributed query
func (d *OscarDriver) ExecuteDistributedQuery(ctx context.Context, db *sql.DB, sql string) (*OscarDistributedResult, error) {
	if !d.config.ClusterMode {
		return nil, fmt.Errorf("cluster mode is not enabled")
	}

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

	return &OscarDistributedResult{
		Rows:          results,
		Columns:       columns,
		Count:         len(results),
		IsDistributed: true,
	}, nil
}

// OscarDistributedResult represents distributed query result
type OscarDistributedResult struct {
	Rows          []map[string]interface{}
	Columns       []string
	Count         int
	IsDistributed bool
	NodesInvolved []string
}

// ApplyBatchPagination applies pagination to a SQL query for batch processing
func (d *OscarDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	sql = strings.TrimSpace(sql)

	// Check if query already has LIMIT or OFFSET
	sqlUpper := strings.ToUpper(sql)
	if strings.Contains(sqlUpper, " LIMIT ") || strings.Contains(sqlUpper, " OFFSET ") {
		// For complex queries with existing pagination, add a subquery wrapper
		return fmt.Sprintf("SELECT * FROM (%s) AS batch_query LIMIT %d OFFSET %d", sql, batchSize, offset), nil
	}
	// Use standard LIMIT/OFFSET for PostgreSQL-compatible databases
	if offset > 0 {
		return fmt.Sprintf("%s LIMIT %d OFFSET %d", sql, batchSize, offset), nil
	}
	return fmt.Sprintf("%s LIMIT %d", sql, batchSize), nil
}

// GetTablePartitionInfo retrieves partition information
