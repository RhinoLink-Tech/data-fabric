package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	defaultCOSCSVMaxFileBytes int64 = 16 << 20
	maxCOSCSVMaxFileBytes     int64 = 64 << 20
	cosCSVMaxRows                   = 100000
)

// COSCSVDriver exposes one bounded Tencent COS CSV object as a read-only SQL table.
type COSCSVDriver struct{}

func (*COSCSVDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("COS CSV requires a configured read-only SQL adapter")
}

func (*COSCSVDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	normalized, err := NormalizeCOSCSVConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := COSCSVObjectKey(normalized)
	tableName, err := COSCSVTableName(normalized)
	if err != nil {
		return nil, err
	}
	client, err := NewCOSClient(ctx, &COSConfig{
		SecretID: normalized.Username, SecretKey: normalized.Password,
		Region: configString(normalized.AdditionalProps, "region"), BucketName: strings.TrimSpace(normalized.Database),
		HTTPS:       configBool(normalized.AdditionalProps, "https", true),
		EndpointURL: configString(normalized.AdditionalProps, "endpoint_url"),
	})
	if err != nil {
		return nil, fmt.Errorf("create COS CSV client: %w", err)
	}
	maxBytes, _ := cosCSVMaxFileBytes(normalized)
	return NewCSVSQLDatabase(CSVSQLDatabaseConfig{
		DriverName: "cos-csv", ObjectKey: objectKey, TableName: tableName,
		Delimiter: COSCSVDelimiter(normalized), HasHeader: COSCSVHasHeader(normalized),
		SkipHeaderRows: COSCSVSkipHeaderRows(normalized), NullValues: configStringSlice(normalized.AdditionalProps, "null_values"),
		MaxRows: cosCSVMaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetObjectMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect COS CSV object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("COS CSV object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read COS CSV object: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, nil)
}

func (*COSCSVDriver) GetDefaultPort() int         { return 443 }
func (*COSCSVDriver) GetDatabaseTypeName() string { return "cos_csv" }
func (*COSCSVDriver) GetDriverName() string       { return "cos-csv" }
func (*COSCSVDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*COSCSVDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*COSCSVDriver) ConfigureAuth(interface{}) error { return nil }
func (*COSCSVDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("COS CSV SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*COSCSVDriver) TestConnectionContext(context.Context) error {
	return errors.New("COS CSV connectivity must be checked with PingContext on the database opened from its configuration")
}
func (*COSCSVDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}

func (*COSCSVDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "cos" || strings.TrimSpace(parsed.Host) == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid COS CSV URI; expected cos://bucket/path/file.csv")
	}
	key, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || !validCOSCSVObjectKey(key) {
		return errors.New("invalid COS CSV URI; expected a safe relative .csv object key")
	}
	return nil
}

func (*COSCSVDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "cos://"
	}
	return (&url.URL{Scheme: "cos", Host: strings.TrimSpace(config.Database), Path: "/" + COSCSVObjectKey(config)}).String()
}

// NormalizeCOSCSVConfig validates the single-object Tencent COS CSV connection profile.
func NormalizeCOSCSVConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("cos-csv data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" || bucket == "." || bucket == ".." || strings.ContainsAny(bucket, "/\\") {
		return nil, errors.New("cos-csv bucket must be one path-safe name in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("cos-csv SecretId and SecretKey are required")
	}
	if configString(config.AdditionalProps, "region") == "" {
		return nil, errors.New("cos-csv region is required in additionalProps")
	}
	if !validCOSCSVObjectKey(COSCSVObjectKey(config)) {
		return nil, errors.New("cos-csv object_key must be a safe relative .csv object key")
	}
	endpoint := configString(config.AdditionalProps, "endpoint_url")
	if endpoint != "" {
		if err := validateCOSEndpoint(endpoint); err != nil {
			return nil, err
		}
	}
	if _, err := cosCSVMaxFileBytes(config); err != nil {
		return nil, err
	}
	delimiter := configString(config.AdditionalProps, "delimiter")
	if delimiter != "" && (!utf8.ValidString(delimiter) || len([]rune(delimiter)) != 1) {
		return nil, errors.New("cos-csv delimiter must be exactly one character")
	}
	quote := configString(config.AdditionalProps, "quote_char")
	if quote != "" && quote != "\"" {
		return nil, errors.New("cos-csv currently supports the standard double-quote CSV escape character")
	}
	if _, err := cosCSVNonNegativeInt(config.AdditionalProps, "skip_header_rows"); err != nil {
		return nil, fmt.Errorf("cos-csv skip_header_rows must be a non-negative integer: %w", err)
	}
	if value := strings.ToLower(configString(config.AdditionalProps, "has_header")); value != "" && value != "true" && value != "false" && value != "1" && value != "0" {
		return nil, errors.New("cos-csv has_header must be true or false")
	}
	if value := strings.ToLower(configString(config.AdditionalProps, "https")); value != "" && value != "true" && value != "false" && value != "1" && value != "0" {
		return nil, errors.New("cos-csv https must be true or false")
	}
	normalized := *config
	normalized.AdditionalProps = make(map[string]interface{}, len(config.AdditionalProps))
	for key, value := range config.AdditionalProps {
		normalized.AdditionalProps[key] = value
	}
	return &normalized, nil
}

func ValidateCOSCSVConfig(config *model.DataSourceConfig) error {
	_, err := NormalizeCOSCSVConfig(config)
	return err
}

func cosCSVMaxFileBytes(config *model.DataSourceConfig) (int64, error) {
	if config == nil {
		return defaultCOSCSVMaxFileBytes, nil
	}
	value, exists := config.AdditionalProps["max_file_bytes"]
	if !exists || value == nil {
		return defaultCOSCSVMaxFileBytes, nil
	}
	parsed, err := parseCOSCSVInt64(value)
	if err != nil {
		return 0, errors.New("cos-csv max_file_bytes must be an integer")
	}
	if parsed <= 0 || parsed > maxCOSCSVMaxFileBytes {
		return 0, fmt.Errorf("cos-csv max_file_bytes must be between 1 and %d", maxCOSCSVMaxFileBytes)
	}
	return parsed, nil
}

func cosCSVNonNegativeInt(props map[string]interface{}, key string) (int, error) {
	value, exists := props[key]
	if !exists || value == nil {
		return 0, nil
	}
	parsed, err := parseCOSCSVInt64(value)
	if err != nil || parsed < 0 || uint64(parsed) > uint64(^uint(0)>>1) {
		return 0, errors.New("invalid value")
	}
	return int(parsed), nil
}

func parseCOSCSVInt64(value interface{}) (int64, error) {
	switch number := value.(type) {
	case int:
		return int64(number), nil
	case int64:
		return number, nil
	case int32:
		return int64(number), nil
	case uint:
		if uint64(number) > uint64(^uint64(0)>>1) {
			return 0, errors.New("integer overflows int64")
		}
		return int64(number), nil
	case uint64:
		if number > uint64(^uint64(0)>>1) {
			return 0, errors.New("integer overflows int64")
		}
		return int64(number), nil
	case float64:
		if number != float64(int64(number)) {
			return 0, errors.New("fractional value")
		}
		return int64(number), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(number), 10, 64)
	default:
		return 0, errors.New("unsupported value")
	}
}

func validCOSCSVObjectKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || path.IsAbs(key) || strings.Contains(key, "\\") || !strings.EqualFold(path.Ext(key), ".csv") {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func COSCSVObjectKey(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if key := configString(config.AdditionalProps, "object_key"); key != "" {
		return key
	}
	return strings.TrimSpace(config.Host)
}

func COSCSVTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "cos-csv")
}

func COSCSVMaxFileBytes(config *model.DataSourceConfig) int64 {
	limit, _ := cosCSVMaxFileBytes(config)
	return limit
}

func COSCSVDelimiter(config *model.DataSourceConfig) rune {
	value := configString(config.AdditionalProps, "delimiter")
	if value == "" {
		return ','
	}
	return []rune(value)[0]
}

func COSCSVHasHeader(config *model.DataSourceConfig) bool {
	value := strings.ToLower(configString(config.AdditionalProps, "has_header"))
	return value == "" || value == "true" || value == "1"
}

func COSCSVSkipHeaderRows(config *model.DataSourceConfig) int {
	value, err := cosCSVNonNegativeInt(config.AdditionalProps, "skip_header_rows")
	if err != nil {
		return -1
	}
	return value
}
