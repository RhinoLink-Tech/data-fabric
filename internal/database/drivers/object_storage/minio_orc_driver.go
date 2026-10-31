package object_storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// MinIOORCDriver exposes one MinIO-hosted ORC object through the shared ORC SQL adapter.
type MinIOORCDriver struct{}

var _ drivers.Driver = (*MinIOORCDriver)(nil)

func (d *MinIOORCDriver) Open(string) (*sql.DB, error) {
	return nil, fmt.Errorf("MinIO ORC requires a configured S3-compatible SQL adapter")
}

func (d *MinIOORCDriver) ValidateDSN(dsn string) error {
	if !strings.HasPrefix(dsn, "minio-orc://") {
		return fmt.Errorf("invalid MinIO ORC URI")
	}
	bucket, key, err := ParseS3URI("s3://" + strings.TrimPrefix(dsn, "minio-orc://"))
	if err != nil {
		return fmt.Errorf("invalid MinIO ORC URI: %w", err)
	}
	if bucket == "" || key == "" {
		return fmt.Errorf("MinIO ORC URI must include a bucket and object key")
	}
	return nil
}

func (d *MinIOORCDriver) GetDefaultPort() int { return 9000 }

func (d *MinIOORCDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "minio-orc://"
	}
	key := configString(config.AdditionalProps, "object_key")
	if key == "" {
		key = strings.TrimSpace(config.Host)
	}
	return fmt.Sprintf("minio-orc://%s/%s", strings.TrimSpace(config.Database), key)
}

func (d *MinIOORCDriver) GetDatabaseTypeName() string { return "minio-orc" }
func (d *MinIOORCDriver) GetDriverName() string       { return "minio-orc" }
func (d *MinIOORCDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (d *MinIOORCDriver) TestConnection(*sql.DB) error {
	return fmt.Errorf("MinIO ORC connectivity requires the configured SQL adapter")
}
func (d *MinIOORCDriver) TestConnectionContext(context.Context) error {
	return fmt.Errorf("MinIO ORC connectivity requires source configuration")
}
func (d *MinIOORCDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsSchemaDiscovery: true,
	}
}
func (d *MinIOORCDriver) ConfigureAuth(interface{}) error { return nil }
func (d *MinIOORCDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}
