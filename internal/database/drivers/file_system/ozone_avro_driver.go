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

// OzoneAvroDriver reads one bounded Avro OCF object through the Ozone S3 Gateway.
type OzoneAvroDriver struct{}

func (*OzoneAvroDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("Ozone Avro requires a configured S3 Gateway read-only SQL adapter")
}

func (*OzoneAvroDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, endpoint, err := NormalizeOzoneAvroConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := OzoneAvroObjectKey(normalized)
	tableName, err := OzoneAvroTableName(normalized)
	if err != nil {
		return nil, err
	}
	client, err := newOzoneS3Client(ctx, normalized, endpoint)
	if err != nil {
		return nil, fmt.Errorf("create Ozone S3 Gateway client: %w", err)
	}
	maxBytes := OzoneAvroMaxFileBytes(normalized)
	return object_storage.NewAvroSQLDatabase(object_storage.AvroSQLDatabaseConfig{
		DriverName: "ozone-avro", ObjectKey: objectKey, TableName: tableName,
		BatchSize: 1000, MaxFileBytes: maxBytes, MaxRows: ozoneAvroMaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetObjectMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect Ozone Avro object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("Ozone Avro object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read Ozone Avro object: %w", err)
		}
		return data, nil
	}, nil)
}

func (*OzoneAvroDriver) GetDefaultPort() int         { return 9878 }
func (*OzoneAvroDriver) GetDatabaseTypeName() string { return "ozone-avro" }
func (*OzoneAvroDriver) GetDriverName() string       { return "ozone-avro" }
func (*OzoneAvroDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*OzoneAvroDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*OzoneAvroDriver) ConfigureAuth(interface{}) error { return nil }
func (*OzoneAvroDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("Ozone Avro SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*OzoneAvroDriver) TestConnectionContext(context.Context) error {
	return errors.New("Ozone Avro connectivity must be checked with PingContext on the database opened from its S3 Gateway configuration")
}

func (*OzoneAvroDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
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

func (*OzoneAvroDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "ozone-s3" || parsed.Host == "" || !path.IsAbs(parsed.Path) {
		return errors.New("invalid Ozone Avro URI; expected ozone-s3://gateway/bucket/path/file.avro")
	}
	parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || !validOzoneAvroKey(strings.Join(parts[1:], "/")) {
		return errors.New("invalid Ozone Avro URI; expected ozone-s3://gateway/bucket/path/file.avro")
	}
	return nil
}

func (*OzoneAvroDriver) BuildDSN(config *model.DataSourceConfig) string {
	normalized, endpoint, err := NormalizeOzoneAvroConfig(config)
	if err != nil {
		return "ozone-s3://"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "ozone-s3://"
	}
	return (&url.URL{Scheme: "ozone-s3", Host: parsed.Host, Path: "/" + strings.TrimSpace(normalized.Database) + "/" + OzoneAvroObjectKey(normalized)}).String()
}

func NormalizeOzoneAvroConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, string, error) {
	return normalizeOzoneS3ObjectConfig(config, "Avro", validOzoneAvroKey)
}

func ValidateOzoneAvroConfig(config *model.DataSourceConfig) error {
	_, _, err := NormalizeOzoneAvroConfig(config)
	return err
}

func OzoneAvroObjectKey(config *model.DataSourceConfig) string {
	return OzoneS3ObjectKey(config)
}

func OzoneAvroTableName(config *model.DataSourceConfig) (string, error) {
	return OzoneS3TableName(config, "Avro")
}

func OzoneAvroMaxFileBytes(config *model.DataSourceConfig) int64 {
	return OzoneS3MaxFileBytes(config)
}

func validOzoneAvroKey(key string) bool {
	return validOzoneS3ObjectKey(key, ".avro")
}

// Legacy listing is unavailable because this adapter is configured for one object.
func (*OzoneAvroDriver) ListAvroFiles(context.Context, string) ([]OzoneFileInfo, error) {
	return nil, errors.New("Ozone Avro object listing is not provided by the single-object S3 Gateway adapter")
}

// QueryAvroFile is retained for compatibility and fails instead of returning placeholder rows.
func (*OzoneAvroDriver) QueryAvroFile(context.Context, string) (*OzoneAvroResult, error) {
	return nil, errors.New("use OpenWithConfig and the read-only SQL adapter for Ozone Avro queries")
}

type OzoneAvroResult struct {
	Rows      []map[string]interface{}
	NumRows   int64
	BytesRead int64
	Key       string
}
