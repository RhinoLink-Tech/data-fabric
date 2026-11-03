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

// OzoneJSONDriver reads one bounded JSON object through the Ozone S3 Gateway.
type OzoneJSONDriver struct{}

func (*OzoneJSONDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("Ozone JSON requires a configured S3 Gateway read-only SQL adapter")
}

func (*OzoneJSONDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, endpoint, err := NormalizeOzoneJSONConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := OzoneJSONObjectKey(normalized)
	tableName, err := OzoneJSONTableName(normalized)
	if err != nil {
		return nil, err
	}
	client, err := newOzoneS3Client(ctx, normalized, endpoint)
	if err != nil {
		return nil, fmt.Errorf("create Ozone S3 Gateway client: %w", err)
	}
	maxBytes := OzoneJSONMaxFileBytes(normalized)
	return object_storage.NewJSONSQLDatabase(object_storage.JSONSQLDatabaseConfig{
		DriverName: "ozone-json", ObjectKey: objectKey, TableName: tableName,
		MaxFileBytes: maxBytes, MaxRecords: ozoneS3MaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetObjectMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect Ozone JSON object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("Ozone JSON object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string, limit int64) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, limit)
		if err != nil {
			return nil, fmt.Errorf("read Ozone JSON object: %w", err)
		}
		return data, nil
	}, nil)
}

func (*OzoneJSONDriver) GetDefaultPort() int         { return 9878 }
func (*OzoneJSONDriver) GetDatabaseTypeName() string { return "ozone-json" }
func (*OzoneJSONDriver) GetDriverName() string       { return "ozone-json" }
func (*OzoneJSONDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*OzoneJSONDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*OzoneJSONDriver) ConfigureAuth(interface{}) error { return nil }
func (*OzoneJSONDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("Ozone JSON SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*OzoneJSONDriver) TestConnectionContext(context.Context) error {
	return errors.New("Ozone JSON connectivity must be checked with PingContext on the database opened from its S3 Gateway configuration")
}
func (*OzoneJSONDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return object_storage.ApplyJSONBatchPagination(query, batchSize, offset)
}

func (*OzoneJSONDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "ozone-s3" || parsed.Host == "" || !path.IsAbs(parsed.Path) {
		return errors.New("invalid Ozone JSON URI; expected ozone-s3://gateway/bucket/path/file.json")
	}
	parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || !validOzoneJSONKey(strings.Join(parts[1:], "/")) {
		return errors.New("invalid Ozone JSON URI; expected ozone-s3://gateway/bucket/path/file.json")
	}
	return nil
}

func (*OzoneJSONDriver) BuildDSN(config *model.DataSourceConfig) string {
	normalized, endpoint, err := NormalizeOzoneJSONConfig(config)
	if err != nil {
		return "ozone-s3://"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "ozone-s3://"
	}
	return (&url.URL{Scheme: "ozone-s3", Host: parsed.Host, Path: "/" + strings.TrimSpace(normalized.Database) + "/" + OzoneJSONObjectKey(normalized)}).String()
}

func NormalizeOzoneJSONConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, string, error) {
	return normalizeOzoneS3ObjectConfig(config, "JSON", validOzoneJSONKey)
}

func ValidateOzoneJSONConfig(config *model.DataSourceConfig) error {
	_, _, err := NormalizeOzoneJSONConfig(config)
	return err
}

func OzoneJSONObjectKey(config *model.DataSourceConfig) string {
	return OzoneS3ObjectKey(config)
}

func OzoneJSONTableName(config *model.DataSourceConfig) (string, error) {
	return OzoneS3TableName(config, "JSON")
}

func OzoneJSONMaxFileBytes(config *model.DataSourceConfig) int64 {
	return OzoneS3MaxFileBytes(config)
}

func validOzoneJSONKey(key string) bool {
	return validOzoneS3ObjectKey(key, ".json", ".jsonl", ".ndjson", ".jsonlines")
}

// Listing is unavailable because this adapter is configured for one object.
func (*OzoneJSONDriver) ListJSONFiles(context.Context, string) ([]OzoneFileInfo, error) {
	return nil, errors.New("Ozone JSON object listing is not provided by the single-object S3 Gateway adapter")
}

// QueryJSONFile is retained for compatibility and directs callers to the SQL adapter.
func (*OzoneJSONDriver) QueryJSONFile(context.Context, string) (*OzoneJSONResult, error) {
	return nil, errors.New("use OpenWithConfig and the read-only SQL adapter for Ozone JSON queries")
}

type OzoneJSONResult struct {
	Rows      []map[string]interface{}
	NumRows   int64
	BytesRead int64
	Key       string
}
