package table_formats

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/common"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// Iceberg-specific types (these can remain as they are table-format specific)
type PartitionSpec struct {
	Fields []PartitionField
}

type PartitionField struct {
	Name      string
	Transform string
}

type Snapshot struct {
	SnapshotId       int64
	ParentSnapshotId int64
	SequenceNumber   int64
	TimestampMillis  int64
	ManifestList     string
	SchemaId         int64
	Summary          map[string]string
}

// IcebergDriver implements the Driver interface for Apache Iceberg tables
type IcebergDriver struct {
	restClient *IcebergRESTClient
	parser     *IcebergMetadataParser
	config     *IcebergConfig
	sqlConfig  *icebergSQLConfig
}

// NewIcebergDriver creates a new Iceberg driver
func NewIcebergDriver(config *IcebergConfig) (*IcebergDriver, error) {
	restClient, err := NewIcebergRESTClient(config)
	if err != nil {
		return nil, err
	}

	settings := &icebergSQLConfig{
		uri: strings.TrimRight(config.BaseURL, "/"), warehouse: config.Warehouse,
		maxRows: icebergDefaultMaxRows,
	}
	if config.Auth != nil {
		if config.Auth.Type == "bearer" {
			settings.token = config.Auth.Credential
		} else {
			settings.credential = config.Auth.Credential
		}
	}
	return &IcebergDriver{
		restClient: restClient,
		parser:     NewIcebergMetadataParser(),
		config:     config,
		sqlConfig:  settings,
	}, nil
}

// Open opens a connection to Iceberg (returns error as Iceberg uses REST, not standard DB connections)
func (d *IcebergDriver) Open(dsn string) (*sql.DB, error) {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "iceberg" || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid Iceberg DSN; expected iceberg://host:port/warehouse")
	}
	port := 0
	if parsed.Port() != "" {
		port, err = strconv.Atoi(parsed.Port())
		if err != nil {
			return nil, fmt.Errorf("invalid Iceberg REST catalog port: %w", err)
		}
	}
	config := &model.DataSourceConfig{
		Host: parsed.Hostname(), Port: port, Database: strings.Trim(parsed.Path, "/"),
		AdditionalProps: make(map[string]interface{}),
	}
	if token := parsed.Query().Get("token"); token != "" {
		config.AdditionalProps["token"] = token
	}
	return d.OpenWithConfig(context.Background(), config)
}

// ValidateDSN validates the connection string
func (d *IcebergDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "iceberg" || parsed.Hostname() == "" {
		return fmt.Errorf("invalid Iceberg DSN; expected iceberg://host:port/warehouse")
	}
	if strings.Trim(parsed.Path, "/") == "" {
		return fmt.Errorf("Iceberg warehouse is required in the DSN path")
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("Iceberg REST catalog port must be between 1 and 65535")
		}
	}
	return nil
}

// GetDefaultPort returns the default port for Iceberg REST
func (d *IcebergDriver) GetDefaultPort() int {
	return 8181 // Default Iceberg REST API port
}

// BuildDSN builds a connection string from configuration
func (d *IcebergDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	host := strings.TrimSpace(config.Host)
	port := config.Port
	if strings.Contains(host, "://") {
		if parsed, err := url.Parse(host); err == nil {
			host = parsed.Hostname()
			if parsed.Port() != "" {
				port, _ = strconv.Atoi(parsed.Port())
			}
		}
	}
	if port <= 0 {
		port = d.GetDefaultPort()
	}
	warehouse := strings.Trim(config.Database, "/")
	return fmt.Sprintf("iceberg://%s/%s", net.JoinHostPort(host, strconv.Itoa(port)), url.PathEscape(warehouse))
}

// GetDatabaseTypeName returns the database type name
func (d *IcebergDriver) GetDatabaseTypeName() string {
	return "iceberg"
}

// TestConnection tests if the connection is working
func (d *IcebergDriver) TestConnection(db *sql.DB) error {
	return fmt.Errorf("use TestConnectionREST instead for Iceberg")
}

// TestConnectionREST tests if the REST API is accessible
func (d *IcebergDriver) TestConnectionREST(ctx context.Context) error {
	if d.sqlConfig == nil {
		return fmt.Errorf("Iceberg REST catalog is not configured")
	}
	connector := &icebergSQLConnector{config: *d.sqlConfig}
	conn, err := connector.Connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.(*icebergSQLConn).Ping(ctx)
}

// GetDriverName returns the underlying driver name
func (d *IcebergDriver) GetDriverName() string {
	return "iceberg-rest"
}

// GetCategory returns the driver category
func (d *IcebergDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryTableFormat
}

// GetCapabilities returns driver capabilities
func (d *IcebergDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true, // Via the bounded read-only Iceberg SQL adapter
		SupportsTransaction:     false,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       true,
	}
}

// ConfigureAuth configures authentication
func (d *IcebergDriver) ConfigureAuth(authConfig interface{}) error {
	// Extract auth configuration from the provided config
	// Type assertion based on expected auth config type
	return nil
}

// =============================================================================
// Iceberg-Specific Methods
// =============================================================================

// ListTables lists all tables in the catalog
func (d *IcebergDriver) ListTables(ctx context.Context, namespace string) ([]IcebergTableIdentifier, error) {
	return d.restClient.ListTables(ctx, namespace)
}

// GetTableMetadata retrieves table metadata
func (d *IcebergDriver) GetTableMetadata(ctx context.Context, namespace, table string) (*IcebergTableMetadata, error) {
	return d.restClient.LoadTable(ctx, namespace, table)
}

// GetTableSchema retrieves table schema in standard format
func (d *IcebergDriver) GetTableSchema(ctx context.Context, namespace, table string) (*common.TableSchema, error) {
	metadata, err := d.restClient.LoadTable(ctx, namespace, table)
	if err != nil {
		return nil, err
	}

	parsedSchema, err := d.parser.ParseTableMetadata(metadata, table)
	if err != nil {
		return nil, err
	}

	return parsedSchema.Tables[table], nil
}

// QueryTable executes a bounded read-only SELECT against the current Iceberg snapshot.
func (d *IcebergDriver) QueryTable(ctx context.Context, query string) (*sql.Rows, error) {
	if d.sqlConfig == nil {
		return nil, fmt.Errorf("Iceberg REST catalog is not configured")
	}
	db := sql.OpenDB(&icebergSQLConnector{config: *d.sqlConfig})
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	// database/sql keeps the active connection alive until Rows.Close, then the
	// closed DB releases it instead of retaining an unowned pool.
	_ = db.Close()
	return rows, nil
}

// GetSnapshotAtTime retrieves snapshot ID for a specific timestamp
func (d *IcebergDriver) GetSnapshotAtTime(ctx context.Context, namespace, table string, timestamp time.Time) (int64, error) {
	metadata, err := d.restClient.LoadTable(ctx, namespace, table)
	if err != nil {
		return 0, err
	}

	return d.parser.GetSnapshotAtTime(metadata, timestamp)
}

// GetSnapshotInfo retrieves detailed information about a snapshot
func (d *IcebergDriver) GetSnapshotInfo(ctx context.Context, namespace, table string, snapshotID int64) (*IcebergSnapshot, error) {
	return d.restClient.GetTableSnapshot(ctx, namespace, table, fmt.Sprintf("%d", snapshotID))
}
