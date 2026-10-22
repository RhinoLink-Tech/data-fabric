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

// OpenGaussDriver implements Driver interface for OpenGauss
type OpenGaussDriver struct {
	config *OpenGaussConfig
}

// OpenGaussConfig holds OpenGauss configuration
type OpenGaussConfig struct {
	Host         string
	Port         int
	Database     string
	Username     string
	Password     string
	ReplicaCount int    // Number of replicas
	EnableHA     bool   // Enable high availability
	SSLMode      string // disable, require, verify-ca, verify-full
}

// NewOpenGaussDriver creates a new OpenGauss driver
func NewOpenGaussDriver(config *OpenGaussConfig) (*OpenGaussDriver, error) {
	if config.Host == "" {
		return nil, fmt.Errorf("host is required")
	}

	return &OpenGaussDriver{
		config: config,
	}, nil
}

// Open opens a connection to OpenGauss (PostgreSQL-compatible)
func (d *OpenGaussDriver) Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open OpenGauss connection: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping OpenGauss: %w", err)
	}

	return db, nil
}

// ValidateDSN validates the connection string
func (d *OpenGaussDriver) ValidateDSN(dsn string) error {
	if dsn == "" {
		return fmt.Errorf("DSN cannot be empty")
	}
	return nil
}

// GetDefaultPort returns the default OpenGauss port
func (d *OpenGaussDriver) GetDefaultPort() int {
	return 5432
}

// BuildDSN builds a connection string from configuration
func (d *OpenGaussDriver) BuildDSN(config *model.DataSourceConfig) string {
	sslMode := "disable"
	if config.SSL {
		sslMode = "require"
	}
	if d != nil && d.config != nil && d.config.SSLMode != "" {
		sslMode = d.config.SSLMode
	}

	dsn := fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=%s",
		config.Host, config.Port, config.Database, config.Username, config.Password, sslMode)

	return dsn
}

// GetDatabaseTypeName returns the database type name
func (d *OpenGaussDriver) GetDatabaseTypeName() string {
	return "opengauss"
}

// TestConnection tests if the connection is working
func (d *OpenGaussDriver) TestConnection(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return db.PingContext(ctx)
}

// GetDriverName returns the driver name
func (d *OpenGaussDriver) GetDriverName() string {
	return "opengauss-pg"
}

// GetCategory returns the driver category
func (d *OpenGaussDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryRelational
}

// GetCapabilities returns driver capabilities
func (d *OpenGaussDriver) GetCapabilities() drivers.DriverCapabilities {
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
func (d *OpenGaussDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// GetDatabaseInfo retrieves OpenGauss database information
func (d *OpenGaussDriver) GetDatabaseInfo(ctx context.Context, db *sql.DB) (*OpenGaussDatabaseInfo, error) {
	var version string
	err := db.QueryRowContext(ctx, "SELECT version()").Scan(&version)
	if err != nil {
		return nil, fmt.Errorf("failed to get version: %w", err)
	}

	info := &OpenGaussDatabaseInfo{
		Version: version,
	}

	return info, nil
}

// OpenGaussDatabaseInfo represents OpenGauss database information
type OpenGaussDatabaseInfo struct {
	Version string
	Edition string // Enterprise, Standard
	Cluster bool
}

// GetTableDistribution retrieves table distribution information
func (d *OpenGaussDriver) GetTableDistribution(ctx context.Context, db *sql.DB, tableName string) (*OpenGaussTableDistribution, error) {
	sql := `
		SELECT TABLE_NAME, DISTRIBUTION_TYPE, DISTRIBUTION_KEY
		FROM PG_TABLES
		WHERE TABLENAME = ?
	`

	dist := &OpenGaussTableDistribution{
		TableName: tableName,
	}

	err := db.QueryRowContext(ctx, sql, tableName).Scan(
		&dist.TableName,
		&dist.DistributionType,
		&dist.DistributionKey,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get table distribution: %w", err)
	}

	return dist, nil
}

// OpenGaussTableDistribution represents table distribution info
type OpenGaussTableDistribution struct {
	TableName        string
	DistributionType string // HASH, RANGE, REPLICATION
	DistributionKey  string
	NodeCount        int
}

// EnableRowSecurity enables row-level security
func (d *OpenGaussDriver) EnableRowSecurity(ctx context.Context, db *sql.DB, tableName string) error {
	sql := fmt.Sprintf("ALTER TABLE %s ENABLE ROW LEVEL SECURITY", tableName)
	_, err := db.ExecContext(ctx, sql)
	return err
}

// CreateRowSecurityPolicy creates a row-level security policy
func (d *OpenGaussDriver) CreateRowSecurityPolicy(ctx context.Context, db *sql.DB, policy *OpenGaussRLSPolicy) error {
	sql := fmt.Sprintf("CREATE ROW LEVEL SECURITY POLICY %s ON %s USING (%s)",
		policy.PolicyName, policy.TableName, policy.UsingExpression)
	_, err := db.ExecContext(ctx, sql)
	return err
}

// ApplyBatchPagination applies pagination to a SQL query for batch processing
func (d *OpenGaussDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
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
