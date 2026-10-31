package object_storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// MinIOAvroDriver exposes one MinIO-hosted Avro OCF object through the shared SQL adapter.
type MinIOAvroDriver struct{}

var _ drivers.Driver = (*MinIOAvroDriver)(nil)

func (d *MinIOAvroDriver) Open(string) (*sql.DB, error) {
	return nil, fmt.Errorf("MinIO Avro requires a configured S3-compatible SQL adapter")
}

func (d *MinIOAvroDriver) ValidateDSN(dsn string) error {
	if !strings.HasPrefix(dsn, "minio-avro://") {
		return fmt.Errorf("invalid MinIO Avro URI")
	}
	bucket, key, err := ParseS3URI("s3://" + strings.TrimPrefix(dsn, "minio-avro://"))
	if err != nil {
		return fmt.Errorf("invalid MinIO Avro URI: %w", err)
	}
	if bucket == "" || key == "" {
		return fmt.Errorf("MinIO Avro URI must include a bucket and object key")
	}
	return nil
}

func (d *MinIOAvroDriver) GetDefaultPort() int { return 9000 }

func (d *MinIOAvroDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "minio-avro://"
	}
	key := configString(config.AdditionalProps, "object_key")
	if key == "" {
		key = strings.TrimSpace(config.Host)
	}
	return fmt.Sprintf("minio-avro://%s/%s", strings.TrimSpace(config.Database), key)
}

func (d *MinIOAvroDriver) GetDatabaseTypeName() string { return "minio-avro" }
func (d *MinIOAvroDriver) GetDriverName() string       { return "minio-avro" }
func (d *MinIOAvroDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (d *MinIOAvroDriver) TestConnection(*sql.DB) error {
	return fmt.Errorf("MinIO Avro connectivity requires the configured SQL adapter")
}
func (d *MinIOAvroDriver) TestConnectionContext(context.Context) error {
	return fmt.Errorf("MinIO Avro connectivity requires source configuration")
}
func (d *MinIOAvroDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsSchemaDiscovery: true,
	}
}
func (d *MinIOAvroDriver) ConfigureAuth(interface{}) error { return nil }
func (d *MinIOAvroDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}
