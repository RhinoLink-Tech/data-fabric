package object_storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// MinIODeltaDriver reads bounded, unpartitioned Delta snapshots stored in MinIO.
type MinIODeltaDriver struct {
	config    *MinIOConfig
	client    *MinIOClient
	tablePath string
	tableName string
	batchSize int
}

func NewMinIODeltaDriver(ctx context.Context, config *MinIOConfig) (*MinIODeltaDriver, error) {
	client, err := NewMinIOClient(ctx, config)
	if err != nil {
		return nil, err
	}
	return &MinIODeltaDriver{config: config, client: client, batchSize: 1000}, nil
}

func (d *MinIODeltaDriver) Open(string) (*sql.DB, error) {
	return nil, fmt.Errorf("MinIO Delta requires a configured read-only SQL adapter")
}

func (d *MinIODeltaDriver) ValidateDSN(dsn string) error {
	if !strings.HasPrefix(dsn, "s3://") {
		return fmt.Errorf("invalid MinIO Delta URI: %s", dsn)
	}
	bucket, tablePath, found := strings.Cut(strings.TrimPrefix(dsn, "s3://"), "/")
	if !found || strings.TrimSpace(bucket) == "" {
		return fmt.Errorf("MinIO Delta URI must include a bucket and table path")
	}
	if _, err := cleanObjectDeltaTablePath(tablePath); err != nil {
		return err
	}
	return nil
}

func (d *MinIODeltaDriver) GetDefaultPort() int { return 9000 }

func (d *MinIODeltaDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "s3://"
	}
	tablePath := configString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = config.Host
	}
	return fmt.Sprintf("s3://%s/%s", config.Database, tablePath)
}

func (d *MinIODeltaDriver) GetDatabaseTypeName() string { return "minio-delta" }
func (d *MinIODeltaDriver) GetDriverName() string       { return "minio-delta" }
func (d *MinIODeltaDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

func (d *MinIODeltaDriver) TestConnection(*sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead")
}

func (d *MinIODeltaDriver) TestConnectionContext(ctx context.Context) error {
	if d == nil || d.client == nil {
		return fmt.Errorf("MinIO Delta driver is not initialized")
	}
	_, err := d.client.ListBuckets(ctx)
	if err != nil {
		return fmt.Errorf("failed to test MinIO connection: %w", err)
	}
	return nil
}

func (d *MinIODeltaDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     false,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       false,
	}
}

func (d *MinIODeltaDriver) ConfigureAuth(interface{}) error { return nil }

func (d *MinIODeltaDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}
