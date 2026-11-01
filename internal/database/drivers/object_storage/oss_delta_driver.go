package object_storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OSSDeltaDriver reads bounded, unpartitioned Delta snapshots stored in OSS.
type OSSDeltaDriver struct {
	config    *OSSConfig
	client    *OSSClient
	tablePath string
	tableName string
	batchSize int
}

// NewOSSDeltaDriver creates a new OSS Delta Lake driver.
func NewOSSDeltaDriver(ctx context.Context, config *OSSConfig) (*OSSDeltaDriver, error) {
	client, err := NewOSSClient(ctx, config)
	if err != nil {
		return nil, err
	}
	return &OSSDeltaDriver{config: config, client: client, batchSize: 1000}, nil
}

// Open opens a connection (not applicable for OSS Delta Lake).
func (d *OSSDeltaDriver) Open(dsn string) (*sql.DB, error) {
	return nil, fmt.Errorf("OSS Delta Lake driver requires a configured read-only SQL adapter")
}

// ValidateDSN validates the connection string.
func (d *OSSDeltaDriver) ValidateDSN(dsn string) error {
	if !strings.HasPrefix(dsn, "oss://") {
		return fmt.Errorf("invalid OSS URI: %s", dsn)
	}
	bucket, tablePath, found := strings.Cut(strings.TrimPrefix(dsn, "oss://"), "/")
	if !found || strings.TrimSpace(bucket) == "" {
		return fmt.Errorf("OSS Delta URI must include a bucket and table path")
	}
	if _, err := cleanObjectDeltaTablePath(tablePath); err != nil {
		return err
	}
	return nil
}

// GetDefaultPort returns the default OSS port.
func (d *OSSDeltaDriver) GetDefaultPort() int { return 443 }

// BuildDSN builds a connection string from configuration.
func (d *OSSDeltaDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "oss://"
	}
	tablePath := configString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = config.Host
	}
	return fmt.Sprintf("oss://%s/%s", config.Database, tablePath)
}

func (d *OSSDeltaDriver) GetDatabaseTypeName() string { return "oss-delta" }
func (d *OSSDeltaDriver) GetDriverName() string       { return "oss-delta" }
func (d *OSSDeltaDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

// TestConnection tests if the connection is working.
func (d *OSSDeltaDriver) TestConnection(db *sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead")
}

// TestConnectionContext tests OSS connectivity.
func (d *OSSDeltaDriver) TestConnectionContext(ctx context.Context) error {
	if d == nil || d.client == nil {
		return fmt.Errorf("OSS Delta driver is not initialized")
	}
	_, err := d.client.ListObjects(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("failed to test OSS connection: %w", err)
	}
	return nil
}

// GetCapabilities describes the supported read-only adapter.
func (d *OSSDeltaDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     false,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       false,
	}
}

func (d *OSSDeltaDriver) ConfigureAuth(interface{}) error { return nil }
