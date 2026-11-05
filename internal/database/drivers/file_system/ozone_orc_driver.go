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

// OzoneORCDriver reads one ORC object through the Apache Ozone S3 Gateway.
type OzoneORCDriver struct{}

func (*OzoneORCDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("Ozone ORC requires a configured S3 Gateway read-only SQL adapter")
}

func (d *OzoneORCDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, endpoint, err := NormalizeOzoneORCConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := OzoneORCObjectKey(normalized)
	tableName, err := OzoneORCTableName(normalized)
	if err != nil {
		return nil, err
	}
	client, err := newOzoneS3Client(ctx, normalized, endpoint)
	if err != nil {
		return nil, fmt.Errorf("create Ozone S3 Gateway client: %w", err)
	}
	maxBytes := OzoneORCMaxFileBytes(normalized)
	return object_storage.NewORCSQLDatabase(object_storage.ORCSQLDatabaseConfig{
		DriverName: "ozone-orc", ObjectKey: objectKey, TableName: tableName,
		BatchSize: 1000, MaxFileBytes: maxBytes, MaxRows: ozoneORCMaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetObjectMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect Ozone ORC object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("Ozone ORC object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read Ozone ORC object: %w", err)
		}
		return data, nil
	}, nil)
}

func (*OzoneORCDriver) GetDefaultPort() int         { return 9878 }
func (*OzoneORCDriver) GetDatabaseTypeName() string { return "ozone-orc" }
func (*OzoneORCDriver) GetDriverName() string       { return "ozone-orc" }
func (*OzoneORCDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*OzoneORCDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*OzoneORCDriver) ConfigureAuth(interface{}) error { return nil }
func (*OzoneORCDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("Ozone ORC SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*OzoneORCDriver) TestConnectionContext(context.Context) error {
	return errors.New("Ozone ORC connectivity must be checked with PingContext on the database opened from its S3 Gateway configuration")
}

func (*OzoneORCDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
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

func (d *OzoneORCDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "ozone-s3" || parsed.Host == "" || !path.IsAbs(parsed.Path) {
		return errors.New("invalid Ozone ORC URI; expected ozone-s3://gateway/bucket/path/file.orc")
	}
	parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || !validOzoneORCKey(strings.Join(parts[1:], "/")) {
		return errors.New("invalid Ozone ORC URI; expected ozone-s3://gateway/bucket/path/file.orc")
	}
	return nil
}

func (d *OzoneORCDriver) BuildDSN(config *model.DataSourceConfig) string {
	normalized, endpoint, err := NormalizeOzoneORCConfig(config)
	if err != nil {
		return "ozone-s3://"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "ozone-s3://"
	}
	return (&url.URL{Scheme: "ozone-s3", Host: parsed.Host, Path: "/" + strings.TrimSpace(normalized.Database) + "/" + OzoneORCObjectKey(normalized)}).String()
}

// NormalizeOzoneORCConfig validates the S3 Gateway origin and copies the
// data-source config without mutating its AdditionalProps map.
func NormalizeOzoneORCConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, string, error) {
	return normalizeOzoneS3ObjectConfig(config, "ORC", validOzoneORCKey)
}

func ValidateOzoneORCConfig(config *model.DataSourceConfig) error {
	_, _, err := NormalizeOzoneORCConfig(config)
	return err
}

func OzoneORCObjectKey(config *model.DataSourceConfig) string {
	return OzoneS3ObjectKey(config)
}

func OzoneORCTableName(config *model.DataSourceConfig) (string, error) {
	return OzoneS3TableName(config, "ORC")
}

func OzoneORCMaxFileBytes(config *model.DataSourceConfig) int64 {
	return OzoneS3MaxFileBytes(config)
}

func validOzoneORCKey(key string) bool {
	return validOzoneS3ObjectKey(key, ".orc")
}

// Legacy object methods now fail explicitly instead of returning success-shaped placeholders.
func (*OzoneORCDriver) ListORCFiles(context.Context, string) ([]OzoneFileInfo, error) {
	return nil, errors.New("Ozone ORC file listing requires a configured S3 Gateway data source")
}
func (*OzoneORCDriver) QueryORCFile(context.Context, string) (*OzoneORCResult, error) {
	return nil, errors.New("use OpenWithConfig and the read-only SQL adapter for Ozone ORC queries")
}
func (*OzoneORCDriver) GetCapacity(context.Context) (*OzoneCapacity, error) {
	return nil, errors.New("Ozone cluster capacity is not exposed by the S3 Gateway adapter")
}

type OzoneORCResult struct {
	Rows      []map[string]interface{}
	NumRows   int64
	BytesRead int64
	Key       string
}
