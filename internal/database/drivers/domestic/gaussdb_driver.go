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

// GaussDBDriver implements Driver interface for Huawei GaussDB
type GaussDBDriver struct {
	config *GaussDBConfig
}

// GaussDBConfig holds GaussDB configuration
type GaussDBConfig struct {
	Host         string
	Port         int
	Database     string
	Username     string
	Password     string
	ReplicaCount int      // Number of replicas
	PrimaryNode  string   // Primary node address
	StandbyNodes []string // Standby node addresses
	EnableHA     bool     // Enable high availability
	SSLMode      string   // disable, require, verify-ca, verify-full
}

// NewGaussDBDriver creates a new GaussDB driver
func NewGaussDBDriver(config *GaussDBConfig) (*GaussDBDriver, error) {
	if config.Host == "" {
		return nil, fmt.Errorf("host is required")
	}

	return &GaussDBDriver{
		config: config,
	}, nil
}

// Open opens a connection to GaussDB (PostgreSQL-compatible)
func (d *GaussDBDriver) Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open GaussDB connection: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping GaussDB: %w", err)
	}

	return db, nil
}

// ValidateDSN validates the connection string
func (d *GaussDBDriver) ValidateDSN(dsn string) error {
	if dsn == "" {
		return fmt.Errorf("DSN cannot be empty")
	}
	return nil
}

// GetDefaultPort returns the default GaussDB port
func (d *GaussDBDriver) GetDefaultPort() int {
	return 8000
}

// BuildDSN builds a connection string from configuration
func (d *GaussDBDriver) BuildDSN(config *model.DataSourceConfig) string {
	sslMode := d.config.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}

	dsn := fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=%s",
		config.Host, config.Port, config.Database, config.Username, config.Password, sslMode)

	// Add fallback hosts for HA
	if d.config.EnableHA && len(d.config.StandbyNodes) > 0 {
		for _, node := range d.config.StandbyNodes {
			dsn += " host=" + node
		}
	}

	return dsn
}

// GetDatabaseTypeName returns the database type name
func (d *GaussDBDriver) GetDatabaseTypeName() string {
	return "gaussdb"
}

// TestConnection tests if the connection is working
func (d *GaussDBDriver) TestConnection(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return db.PingContext(ctx)
}

// GetDriverName returns the driver name
func (d *GaussDBDriver) GetDriverName() string {
	return "gaussdb-postgres"
}

// GetCategory returns the driver category
func (d *GaussDBDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryRelational
}

// GetCapabilities returns driver capabilities
func (d *GaussDBDriver) GetCapabilities() drivers.DriverCapabilities {
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
func (d *GaussDBDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// GetReplicaStatus retrieves replica status
func (d *GaussDBDriver) GetReplicaStatus(ctx context.Context, db *sql.DB) (*GaussDBReplicaStatus, error) {
	sql := `
		SELECT NODE_NAME, NODE_TYPE, STATUS,
		       CURRENT_SCN, REPLAY_SCN
		FROM pg_stat_replication
	`

	rows, err := db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("failed to get replica status: %w", err)
	}
	defer rows.Close()

	status := &GaussDBReplicaStatus{
		Replicas: []GaussDBNode{},
	}

	for rows.Next() {
		var node GaussDBNode
		err := rows.Scan(
			&node.NodeName,
			&node.NodeType,
			&node.Status,
			&node.CurrentSCN,
			&node.ReplaySCN,
		)
		if err != nil {
			return nil, err
		}
		status.Replicas = append(status.Replicas, node)
	}

	return status, nil
}

// GaussDBReplicaStatus represents GaussDB replica status
type GaussDBReplicaStatus struct {
	PrimaryNode string
	Replicas    []GaussDBNode
}

// GaussDBNode represents a GaussDB node
type GaussDBNode struct {
	NodeName   string
	NodeType   string // primary, standby
	Status     string
	CurrentSCN int64
	ReplaySCN  int64
	Lag        int64
}

// GetDatabaseInfo retrieves GaussDB database information
func (d *GaussDBDriver) GetDatabaseInfo(ctx context.Context, db *sql.DB) (*GaussDBDatabaseInfo, error) {
	var version string
	err := db.QueryRowContext(ctx, "SELECT version()").Scan(&version)
	if err != nil {
		return nil, fmt.Errorf("failed to get version: %w", err)
	}

	info := &GaussDBDatabaseInfo{
		Version: version,
	}

	// Get node count. A failed replica-count query must surface as an error,
	// not be masked as a silent ReplicaCount=0 (which callers read as "no
	// replicas available").
	err = db.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_replication").Scan(&info.ReplicaCount)
	if err != nil {
		return nil, fmt.Errorf("failed to get replica count: %w", err)
	}

	return info, nil
}

// GaussDBDatabaseInfo represents GaussDB database information
type GaussDBDatabaseInfo struct {
	Version      string
	ReplicaCount int
	ClusterSize  int
	StorageType  string
}

// IsPrimaryNode checks if current connection is to primary node
func (d *GaussDBDriver) IsPrimaryNode(ctx context.Context, db *sql.DB) (bool, error) {
	var isInRecovery bool
	err := db.QueryRowContext(ctx, "SELECT pg_is_in_recovery()").Scan(&isInRecovery)
	if err != nil {
		return false, fmt.Errorf("failed to check node type: %w", err)
	}
	return !isInRecovery, nil
}

// SwitchToStandby opens a connection to a standby node and returns it to the
// caller. It deliberately does NOT close the caller's pool (db): *sql.DB is a
// reference used by many goroutines, and closing it here would tear down a
// connection this method does not own. The caller adopts the returned handle
// instead.
func (d *GaussDBDriver) SwitchToStandby(ctx context.Context, db *sql.DB) (*sql.DB, error) {
	if d.config == nil {
		return nil, fmt.Errorf("driver not configured")
	}
	if len(d.config.StandbyNodes) == 0 {
		return nil, fmt.Errorf("no standby nodes configured")
	}

	// Open connection to first standby node.
	standbyHost := d.config.StandbyNodes[0]
	dsn := fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s",
		standbyHost, d.config.Port, d.config.Database, d.config.Username, d.config.Password)

	standby, err := d.Open(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to standby: %w", err)
	}

	return standby, nil
}

// ApplyBatchPagination applies pagination to a SQL query for batch processing
func (d *GaussDBDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
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
