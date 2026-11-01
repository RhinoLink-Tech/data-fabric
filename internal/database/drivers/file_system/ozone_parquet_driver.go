package file_system

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OzoneParquetDriver reads one Parquet object through the Apache Ozone S3 Gateway.
type OzoneParquetDriver struct{}

func (*OzoneParquetDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("Ozone Parquet requires a configured S3 Gateway read-only SQL adapter")
}

func (d *OzoneParquetDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, endpoint, err := NormalizeOzoneParquetConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := OzoneParquetObjectKey(normalized)
	tableName, err := OzoneParquetTableName(normalized)
	if err != nil {
		return nil, err
	}
	client, err := newOzoneS3Client(ctx, normalized, endpoint)
	if err != nil {
		return nil, fmt.Errorf("create Ozone S3 Gateway client: %w", err)
	}
	maxBytes := OzoneParquetMaxFileBytes(normalized)
	return object_storage.NewParquetSQLDatabase(object_storage.ParquetSQLDatabaseConfig{
		DriverName: "ozone-parquet", ObjectKey: objectKey, TableName: tableName,
		BatchSize: 1000, MaxFileBytes: maxBytes, MaxRows: ozoneParquetMaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetObjectMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect Ozone Parquet object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("Ozone Parquet object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read Ozone Parquet object: %w", err)
		}
		return data, nil
	}, nil)
}

func (*OzoneParquetDriver) GetDefaultPort() int         { return 9878 }
func (*OzoneParquetDriver) GetDatabaseTypeName() string { return "ozone-parquet" }
func (*OzoneParquetDriver) GetDriverName() string       { return "ozone-parquet" }
func (*OzoneParquetDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*OzoneParquetDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*OzoneParquetDriver) ConfigureAuth(interface{}) error { return nil }
func (*OzoneParquetDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("Ozone Parquet SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*OzoneParquetDriver) TestConnectionContext(context.Context) error {
	return errors.New("Ozone Parquet connectivity must be checked with PingContext on the database opened from its S3 Gateway configuration")
}

func (*OzoneParquetDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	if batchSize <= 0 {
		return "", errors.New("batch size must be positive")
	}
	query = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	if query == "" {
		return "", errors.New("query is required")
	}
	if offset < 0 {
		offset = 0
	}
	return fmt.Sprintf("SELECT * FROM (%s) AS __data_fabric_page LIMIT %d OFFSET %d", query, batchSize, offset), nil
}

func (d *OzoneParquetDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "ozone-s3" || parsed.Host == "" || !path.IsAbs(parsed.Path) {
		return errors.New("invalid Ozone Parquet URI; expected ozone-s3://gateway/bucket/path/file.parquet")
	}
	parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || !validOzoneParquetKey(strings.Join(parts[1:], "/")) {
		return errors.New("invalid Ozone Parquet URI; expected ozone-s3://gateway/bucket/path/file.parquet")
	}
	return nil
}

func (d *OzoneParquetDriver) BuildDSN(config *model.DataSourceConfig) string {
	normalized, endpoint, err := NormalizeOzoneParquetConfig(config)
	if err != nil {
		return "ozone-s3://"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "ozone-s3://"
	}
	return (&url.URL{Scheme: "ozone-s3", Host: parsed.Host, Path: "/" + strings.TrimSpace(normalized.Database) + "/" + OzoneParquetObjectKey(normalized)}).String()
}

// NormalizeOzoneParquetConfig validates the S3 Gateway origin and copies the
// data-source config without mutating its AdditionalProps map.
func NormalizeOzoneParquetConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, string, error) {
	return normalizeOzoneS3ObjectConfig(config, "Parquet", validOzoneParquetKey)
}

func ValidateOzoneParquetConfig(config *model.DataSourceConfig) error {
	_, _, err := NormalizeOzoneParquetConfig(config)
	return err
}

func OzoneParquetObjectKey(config *model.DataSourceConfig) string {
	return OzoneS3ObjectKey(config)
}

func OzoneParquetTableName(config *model.DataSourceConfig) (string, error) {
	return OzoneS3TableName(config, "Parquet")
}

func OzoneParquetMaxFileBytes(config *model.DataSourceConfig) int64 {
	return OzoneS3MaxFileBytes(config)
}

func validOzoneParquetKey(key string) bool {
	return validOzoneS3ObjectKey(key, ".parquet")
}

// Legacy object methods now fail explicitly instead of returning success-shaped placeholders.
func (*OzoneParquetDriver) ListParquetFiles(context.Context, string) ([]OzoneFileInfo, error) {
	return nil, errors.New("Ozone Parquet file listing requires a configured S3 Gateway data source")
}
func (*OzoneParquetDriver) QueryParquetFile(context.Context, string) (*OzoneParquetResult, error) {
	return nil, errors.New("use OpenWithConfig and the read-only SQL adapter for Ozone Parquet queries")
}
func (*OzoneParquetDriver) GetCapacity(context.Context) (*OzoneCapacity, error) {
	return nil, errors.New("Ozone cluster capacity is not exposed by the S3 Gateway adapter")
}

type OzoneParquetResult struct {
	Rows      []map[string]interface{}
	NumRows   int64
	BytesRead int64
	Key       string
}
