package object_storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/table_formats"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// MinIOIcebergDriver routes a MinIO-backed Iceberg table through an Iceberg REST catalog.
type MinIOIcebergDriver struct {
	config *MinIOConfig
	client *MinIOClient
}

func NewMinIOIcebergDriver(ctx context.Context, config *MinIOConfig) (*MinIOIcebergDriver, error) {
	client, err := NewMinIOClient(ctx, config)
	if err != nil {
		return nil, err
	}
	return &MinIOIcebergDriver{config: config, client: client}, nil
}

func (d *MinIOIcebergDriver) Open(string) (*sql.DB, error) {
	return nil, fmt.Errorf("MinIO Iceberg requires a configured REST catalog and read-only SQL adapter")
}

func (d *MinIOIcebergDriver) ValidateDSN(dsn string) error {
	if !strings.HasPrefix(dsn, "minio-iceberg://") {
		return fmt.Errorf("invalid MinIO Iceberg URI: %s", dsn)
	}
	warehouseAndTable := strings.TrimPrefix(dsn, "minio-iceberg://")
	warehouse, table, found := strings.Cut(warehouseAndTable, "/")
	if !found || strings.TrimSpace(warehouse) == "" || strings.TrimSpace(table) == "" {
		return fmt.Errorf("MinIO Iceberg URI must include a warehouse and configured table")
	}
	return nil
}

func (d *MinIOIcebergDriver) GetDefaultPort() int { return 8181 }

func (d *MinIOIcebergDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "minio-iceberg://"
	}
	warehouse := configString(config.AdditionalProps, "warehouse")
	if warehouse == "" {
		warehouse = config.Database
	}
	tableName := configString(config.AdditionalProps, "table_name")
	return fmt.Sprintf("minio-iceberg://%s/%s", warehouse, tableName)
}

func (d *MinIOIcebergDriver) GetDatabaseTypeName() string { return "minio-iceberg" }
func (d *MinIOIcebergDriver) GetDriverName() string       { return "minio-iceberg" }
func (d *MinIOIcebergDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

func (d *MinIOIcebergDriver) TestConnection(*sql.DB) error {
	return fmt.Errorf("use the configured Iceberg catalog health check")
}

func (d *MinIOIcebergDriver) TestConnectionContext(ctx context.Context) error {
	if d == nil || d.client == nil {
		return fmt.Errorf("MinIO Iceberg driver is not initialized")
	}
	_, err := d.client.ListBuckets(ctx)
	if err != nil {
		return fmt.Errorf("failed to test MinIO connection: %w", err)
	}
	return nil
}

func (d *MinIOIcebergDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     false,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       false,
	}
}

func (d *MinIOIcebergDriver) ConfigureAuth(interface{}) error { return nil }

func (d *MinIOIcebergDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return (&table_formats.IcebergDriver{}).ApplyBatchPagination(query, batchSize, offset)
}
