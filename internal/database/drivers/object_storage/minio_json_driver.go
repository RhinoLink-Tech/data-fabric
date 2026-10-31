package object_storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// MinIOJSONDriver exposes one MinIO-hosted JSON object through the shared SQL adapter.
type MinIOJSONDriver struct{}

var _ drivers.Driver = (*MinIOJSONDriver)(nil)

func (d *MinIOJSONDriver) Open(string) (*sql.DB, error) {
	return nil, fmt.Errorf("MinIO JSON requires a configured S3-compatible SQL adapter")
}

func (d *MinIOJSONDriver) ValidateDSN(dsn string) error {
	if !strings.HasPrefix(dsn, "minio-json://") {
		return fmt.Errorf("invalid MinIO JSON URI")
	}
	bucket, key, err := ParseS3URI("s3://" + strings.TrimPrefix(dsn, "minio-json://"))
	if err != nil {
		return fmt.Errorf("invalid MinIO JSON URI: %w", err)
	}
	if bucket == "" || key == "" {
		return fmt.Errorf("MinIO JSON URI must include a bucket and object key")
	}
	return nil
}

func (d *MinIOJSONDriver) GetDefaultPort() int { return 9000 }

func (d *MinIOJSONDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "minio-json://"
	}
	key := configString(config.AdditionalProps, "object_key")
	if key == "" {
		key = strings.TrimSpace(config.Host)
	}
	return fmt.Sprintf("minio-json://%s/%s", strings.TrimSpace(config.Database), key)
}

func (d *MinIOJSONDriver) GetDatabaseTypeName() string { return "minio-json" }
func (d *MinIOJSONDriver) GetDriverName() string       { return "minio-json" }
func (d *MinIOJSONDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (d *MinIOJSONDriver) TestConnection(*sql.DB) error {
	return fmt.Errorf("MinIO JSON connectivity requires the configured SQL adapter")
}
func (d *MinIOJSONDriver) TestConnectionContext(context.Context) error {
	return fmt.Errorf("MinIO JSON connectivity requires source configuration")
}
func (d *MinIOJSONDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsSchemaDiscovery: true,
	}
}
func (d *MinIOJSONDriver) ConfigureAuth(interface{}) error { return nil }
func (d *MinIOJSONDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}
