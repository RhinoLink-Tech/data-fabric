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

// COSIcebergDriver routes a COS-backed Iceberg table through an Iceberg REST catalog.
type COSIcebergDriver struct{}

func (d *COSIcebergDriver) Open(string) (*sql.DB, error) {
	return nil, fmt.Errorf("Tencent COS Iceberg requires a configured REST catalog and read-only SQL adapter")
}

func (d *COSIcebergDriver) ValidateDSN(dsn string) error {
	if !strings.HasPrefix(dsn, "cos-iceberg://") {
		return fmt.Errorf("invalid Tencent COS Iceberg URI")
	}
	warehouseAndTable := strings.TrimPrefix(dsn, "cos-iceberg://")
	warehouse, table, found := strings.Cut(warehouseAndTable, "/")
	if !found || strings.TrimSpace(warehouse) == "" || strings.TrimSpace(table) == "" {
		return fmt.Errorf("Tencent COS Iceberg URI must include a warehouse and configured table")
	}
	return nil
}

func (d *COSIcebergDriver) GetDefaultPort() int { return 8181 }

func (d *COSIcebergDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "cos-iceberg://"
	}
	warehouse := configString(config.AdditionalProps, "warehouse")
	if warehouse == "" {
		warehouse = config.Database
	}
	tableName := configString(config.AdditionalProps, "table_name")
	return fmt.Sprintf("cos-iceberg://%s/%s", warehouse, tableName)
}

func (d *COSIcebergDriver) GetDatabaseTypeName() string { return "cos-iceberg" }
func (d *COSIcebergDriver) GetDriverName() string       { return "cos-iceberg" }
func (d *COSIcebergDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

func (d *COSIcebergDriver) TestConnection(*sql.DB) error {
	return fmt.Errorf("use the configured Iceberg REST catalog health check")
}

func (d *COSIcebergDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     false,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       false,
	}
}

func (d *COSIcebergDriver) ConfigureAuth(interface{}) error { return nil }

func (d *COSIcebergDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return (&table_formats.IcebergDriver{}).ApplyBatchPagination(query, batchSize, offset)
}

func (d *COSIcebergDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := cosIcebergDataSourceConfig(config)
	if err != nil {
		return nil, err
	}
	return (&table_formats.IcebergDriver{}).OpenWithConfig(ctx, normalized)
}
