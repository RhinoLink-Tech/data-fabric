package domestic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/go-sql-driver/mysql"
)

const tidbTLSRootCertParam = "datafabricsslrootcert"

// TiDBDriver implements Driver interface for TiDB (compatible with MySQL)
type TiDBDriver struct {
	config *TiDBConfig
}

// TiDBConfig holds TiDB configuration
type TiDBConfig struct {
	Host     string
	Port     int
	Database string
	Username string
	Password string
	Params   map[string]string // Connection parameters
}

// NewTiDBDriver creates a new TiDB driver
func NewTiDBDriver(config *TiDBConfig) (*TiDBDriver, error) {
	if config.Host == "" {
		return nil, fmt.Errorf("host is required")
	}

	return &TiDBDriver{
		config: config,
	}, nil
}

// Open opens a connection to TiDB
func (d *TiDBDriver) Open(dsn string) (*sql.DB, error) {
	if err := d.ValidateDSN(dsn); err != nil {
		return nil, fmt.Errorf("invalid TiDB connection configuration: %w", err)
	}

	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid TiDB connection string")
	}
	rootCertPath := config.Params[tidbTLSRootCertParam]
	delete(config.Params, tidbTLSRootCertParam)

	var db *sql.DB
	if rootCertPath != "" {
		tlsConfig, err := newTiDBTLSConfig(rootCertPath)
		if err != nil {
			return nil, err
		}
		config.TLS = tlsConfig
		connector, err := mysql.NewConnector(config)
		if err != nil {
			return nil, fmt.Errorf("invalid TiDB TLS configuration")
		}
		db = sql.OpenDB(connector)
	} else {
		db, err = sql.Open("mysql", dsn)
		if err != nil {
			return nil, fmt.Errorf("failed to open TiDB connection: %w", err)
		}
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping TiDB: %w", err)
	}

	return db, nil
}

// ValidateDSN validates the connection string
func (d *TiDBDriver) ValidateDSN(dsn string) error {
	if strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("DSN cannot be empty")
	}

	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		return fmt.Errorf("invalid TiDB DSN")
	}
	if config.TLSConfig != "" && config.TLSConfig != "true" && config.TLSConfig != "false" {
		return fmt.Errorf("TiDB TLS requires verified tls=true; insecure modes and custom TLS names are unsupported")
	}

	rootCertPath := config.Params[tidbTLSRootCertParam]
	delete(config.Params, tidbTLSRootCertParam)
	if rootCertPath == "" {
		return nil
	}
	if config.TLSConfig != "true" {
		return fmt.Errorf("TiDB sslRootCert requires ssl=true")
	}
	if _, err := newTiDBTLSConfig(rootCertPath); err != nil {
		return err
	}
	return nil
}

func newTiDBTLSConfig(rootCertPath string) (*tls.Config, error) {
	certificate, err := os.ReadFile(rootCertPath)
	if err != nil {
		return nil, fmt.Errorf("TiDB sslRootCert must refer to a readable PEM CA certificate")
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(certificate) {
		return nil, fmt.Errorf("TiDB sslRootCert must contain at least one valid PEM CA certificate")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    rootCAs,
	}, nil
}

// GetDefaultPort returns the default TiDB port
func (d *TiDBDriver) GetDefaultPort() int {
	return 4000
}

// BuildDSN builds a connection string from configuration
func (d *TiDBDriver) BuildDSN(config *model.DataSourceConfig) string {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
		config.Username,
		config.Password,
		config.Host,
		config.Port,
		config.Database,
	)

	// Add TiDB-specific parameters
	params := []string{
		"parseTime=true",
		"charset=utf8mb4",
		"interpolateParams=true",
	}
	if config.SSL {
		params = append(params, "tls=true")
	}
	if config.SSLRootCert != "" {
		params = append(params, tidbTLSRootCertParam+"="+url.QueryEscape(config.SSLRootCert))
	}

	if len(params) > 0 {
		dsn += "?" + params[0]
		for i := 1; i < len(params); i++ {
			dsn += "&" + params[i]
		}
	}

	return dsn
}

// GetDatabaseTypeName returns the database type name
func (d *TiDBDriver) GetDatabaseTypeName() string {
	return "tidb"
}

// TestConnection tests if the connection is working
func (d *TiDBDriver) TestConnection(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return db.PingContext(ctx)
}

// GetDriverName returns the driver name
func (d *TiDBDriver) GetDriverName() string {
	return "tidb-mysql"
}

// GetCategory returns the driver category
func (d *TiDBDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryRelational
}

// GetCapabilities returns driver capabilities
func (d *TiDBDriver) GetCapabilities() drivers.DriverCapabilities {
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
func (d *TiDBDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// GetTiDBVersion retrieves TiDB server version
func (d *TiDBDriver) GetTiDBVersion(ctx context.Context, db *sql.DB) (string, error) {
	var version string
	err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version)
	if err != nil {
		return "", fmt.Errorf("failed to get TiDB version: %w", err)
	}
	return version, nil
}

// IsDistributedQuery checks if a query is distributed
func (d *TiDBDriver) IsDistributedQuery(sql string) bool {
	// Simplified heuristic - would need actual query parsing
	// Distributed queries typically involve JOINs across tables
	// or aggregate queries with GROUP BY
	return len(sql) > 100
}

// GetTableDistribution retrieves table distribution information
func (d *TiDBDriver) GetTableDistribution(ctx context.Context, db *sql.DB, tableName string) (*TiDBTableDistribution, error) {
	// Query TiDB table regions information
	sql := `
		SELECT
			TABLE_NAME,
			REGION_COUNT,
			DISTRICT_SQL_MODE
		FROM information_schema.TIDB_TABLES
		WHERE TABLE_NAME = ?
	`

	distribution := &TiDBTableDistribution{
		TableName: tableName,
	}

	err := db.QueryRowContext(ctx, sql, tableName).Scan(
		&distribution.TableName,
		&distribution.RegionCount,
		&distribution.StrictSQLMode,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get table distribution: %w", err)
	}

	return distribution, nil
}

// TiDBTableDistribution represents TiDB table distribution
type TiDBTableDistribution struct {
	TableName     string
	RegionCount   int
	StrictSQLMode string
}

// ExecuteDistributedQuery executes a distributed query
func (d *TiDBDriver) ExecuteDistributedQuery(ctx context.Context, db *sql.DB, sql string) (*TiDBQueryResult, error) {
	rows, err := db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("failed to execute distributed query: %w", err)
	}
	defer rows.Close()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	// Read results
	var results []map[string]interface{}
	for rows.Next() {
		// Create slice to hold column values
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		// Create map
		rowMap := make(map[string]interface{})
		for i, col := range columns {
			rowMap[col] = values[i]
		}
		results = append(results, rowMap)
	}

	return &TiDBQueryResult{
		Rows:          results,
		Columns:       columns,
		RowCount:      len(results),
		IsDistributed: d.IsDistributedQuery(sql),
	}, nil
}

// TiDBQueryResult represents TiDB query results
type TiDBQueryResult struct {
	Rows          []map[string]interface{}
	Columns       []string
	RowCount      int
	IsDistributed bool
}

// ApplyBatchPagination applies pagination to a SQL query for batch processing
func (d *TiDBDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
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
