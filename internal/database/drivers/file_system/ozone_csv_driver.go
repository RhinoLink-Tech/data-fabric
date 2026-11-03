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

// OzoneCSVDriver exposes one bounded CSV object through the Ozone S3 Gateway.
type OzoneCSVDriver struct{}

func (*OzoneCSVDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("Ozone CSV requires a configured S3 Gateway read-only SQL adapter")
}

func (*OzoneCSVDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, endpoint, err := NormalizeOzoneCSVConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := OzoneCSVObjectKey(normalized)
	tableName, err := OzoneCSVTableName(normalized)
	if err != nil {
		return nil, err
	}
	client, err := newOzoneS3Client(ctx, normalized, endpoint)
	if err != nil {
		return nil, fmt.Errorf("create Ozone S3 Gateway client: %w", err)
	}
	maxBytes := OzoneCSVMaxFileBytes(normalized)
	return object_storage.NewCSVSQLDatabase(object_storage.CSVSQLDatabaseConfig{
		DriverName: "ozone-csv", ObjectKey: objectKey, TableName: tableName,
		Delimiter: OzoneCSVDelimiter(normalized), HasHeader: OzoneCSVHasHeader(normalized),
		SkipHeaderRows: OzoneCSVSkipHeaderRows(normalized), NullValues: OzoneCSVNullValues(normalized), MaxRows: ozoneS3MaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetObjectMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect Ozone CSV object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("Ozone CSV object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read Ozone CSV object: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, nil)
}

func (*OzoneCSVDriver) GetDefaultPort() int         { return 9878 }
func (*OzoneCSVDriver) GetDatabaseTypeName() string { return "ozone-csv" }
func (*OzoneCSVDriver) GetDriverName() string       { return "ozone-csv" }
func (*OzoneCSVDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*OzoneCSVDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*OzoneCSVDriver) ConfigureAuth(interface{}) error { return nil }
func (*OzoneCSVDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("Ozone CSV SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*OzoneCSVDriver) TestConnectionContext(context.Context) error {
	return errors.New("Ozone CSV connectivity must be checked with PingContext on the database opened from its S3 Gateway configuration")
}
func (*OzoneCSVDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return (&object_storage.S3CSVDriver{}).ApplyBatchPagination(query, batchSize, offset)
}

func (*OzoneCSVDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "ozone-s3" || parsed.Host == "" || !path.IsAbs(parsed.Path) {
		return errors.New("invalid Ozone CSV URI; expected ozone-s3://gateway/bucket/path/file.csv")
	}
	parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || !validOzoneCSVKey(strings.Join(parts[1:], "/")) {
		return errors.New("invalid Ozone CSV URI; expected ozone-s3://gateway/bucket/path/file.csv")
	}
	return nil
}

func (*OzoneCSVDriver) BuildDSN(config *model.DataSourceConfig) string {
	normalized, endpoint, err := NormalizeOzoneCSVConfig(config)
	if err != nil {
		return "ozone-s3://"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "ozone-s3://"
	}
	return (&url.URL{Scheme: "ozone-s3", Host: parsed.Host, Path: "/" + strings.TrimSpace(normalized.Database) + "/" + OzoneCSVObjectKey(normalized)}).String()
}

func NormalizeOzoneCSVConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, string, error) {
	normalized, endpoint, err := normalizeOzoneS3ObjectConfig(config, "CSV", validOzoneCSVKey)
	if err != nil {
		return nil, "", err
	}
	if delimiter := configPropString(normalized, "delimiter"); delimiter != "" && len([]rune(delimiter)) != 1 {
		return nil, "", errors.New("Ozone CSV delimiter must be exactly one character")
	}
	if quote := configPropString(normalized, "quote_char"); quote != "" && quote != `"` {
		return nil, "", errors.New("Ozone CSV currently supports the standard double-quote escape character")
	}
	if _, present := configProp(normalized, "skip_header_rows"); present {
		value, err := configPropInt64(normalized, "skip_header_rows")
		if err != nil || value < 0 || uint64(value) > uint64(^uint(0)>>1) {
			return nil, "", errors.New("Ozone CSV skip_header_rows must be a non-negative integer")
		}
	}
	if value := strings.ToLower(configPropString(normalized, "has_header")); value != "" && value != "true" && value != "false" && value != "1" && value != "0" {
		return nil, "", errors.New("Ozone CSV has_header must be true or false")
	}
	return normalized, endpoint, nil
}

func ValidateOzoneCSVConfig(config *model.DataSourceConfig) error {
	_, _, err := NormalizeOzoneCSVConfig(config)
	return err
}

func OzoneCSVObjectKey(config *model.DataSourceConfig) string { return OzoneS3ObjectKey(config) }
func OzoneCSVTableName(config *model.DataSourceConfig) (string, error) {
	return OzoneS3TableName(config, "CSV")
}
func OzoneCSVMaxFileBytes(config *model.DataSourceConfig) int64 { return OzoneS3MaxFileBytes(config) }
func OzoneCSVDelimiter(config *model.DataSourceConfig) rune {
	value := configPropString(config, "delimiter")
	if value == "" {
		return ','
	}
	return []rune(value)[0]
}
func OzoneCSVHasHeader(config *model.DataSourceConfig) bool {
	value := strings.ToLower(configPropString(config, "has_header"))
	return value == "" || value == "true" || value == "1"
}
func OzoneCSVSkipHeaderRows(config *model.DataSourceConfig) int {
	_, present := configProp(config, "skip_header_rows")
	if !present {
		return 0
	}
	parsed, err := configPropInt64(config, "skip_header_rows")
	if err != nil || parsed < 0 || uint64(parsed) > uint64(^uint(0)>>1) {
		return -1
	}
	return int(parsed)
}
func OzoneCSVNullValues(config *model.DataSourceConfig) []string {
	if config == nil || config.AdditionalProps == nil {
		return nil
	}
	switch value := config.AdditionalProps["null_values"].(type) {
	case []string:
		return append([]string(nil), value...)
	case []interface{}:
		result := make([]string, 0, len(value))
		for _, item := range value {
			if item != nil {
				result = append(result, fmt.Sprint(item))
			}
		}
		return result
	case string:
		if strings.TrimSpace(value) != "" {
			return strings.Split(value, ",")
		}
	}
	return nil
}

func validOzoneCSVKey(key string) bool { return validOzoneS3ObjectKey(key, ".csv") }
