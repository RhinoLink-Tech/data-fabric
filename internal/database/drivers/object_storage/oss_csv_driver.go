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
	defaultOSSCSVMaxFileBytes int64 = 16 << 20
	maxOSSCSVMaxFileBytes     int64 = 64 << 20
	ossCSVMaxRows                   = 100000
)

// OSSCSVDriver exposes one bounded Alibaba OSS CSV object as a read-only SQL table.
type OSSCSVDriver struct{}

func (*OSSCSVDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("OSS CSV requires a configured read-only SQL adapter")
}

func (*OSSCSVDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	normalized, endpoint, err := NormalizeOSSCSVConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := OSSCSVObjectKey(normalized)
	tableName, err := OSSCSVTableName(normalized)
	if err != nil {
		return nil, err
	}
	client, err := NewOSSClient(ctx, &OSSConfig{
		Endpoint: endpoint, AccessKeyID: normalized.Username, AccessKeySecret: normalized.Password,
		BucketName: strings.TrimSpace(normalized.Database), SecurityToken: ossCSVSecurityToken(normalized),
		ForcePathStyle: configBool(normalized.AdditionalProps, "force_path_style", false),
	})
	if err != nil {
		return nil, fmt.Errorf("create OSS CSV client: %w", err)
	}
	maxBytes, _ := ossCSVMaxFileBytes(normalized)
	return NewCSVSQLDatabase(CSVSQLDatabaseConfig{
		DriverName: "oss-csv", ObjectKey: objectKey, TableName: tableName,
		Delimiter: OSSCSVDelimiter(normalized), HasHeader: OSSCSVHasHeader(normalized),
		SkipHeaderRows: OSSCSVSkipHeaderRows(normalized), NullValues: configStringSlice(normalized.AdditionalProps, "null_values"),
		MaxRows: ossCSVMaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetObjectMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect OSS CSV object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("OSS CSV object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read OSS CSV object: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, nil)
}

func (*OSSCSVDriver) GetDefaultPort() int         { return 443 }
func (*OSSCSVDriver) GetDatabaseTypeName() string { return "oss_csv" }
func (*OSSCSVDriver) GetDriverName() string       { return "oss-csv" }
func (*OSSCSVDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*OSSCSVDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*OSSCSVDriver) ConfigureAuth(interface{}) error { return nil }
func (*OSSCSVDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("OSS CSV SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*OSSCSVDriver) TestConnectionContext(context.Context) error {
	return errors.New("OSS CSV connectivity must be checked with PingContext on the database opened from its configuration")
}
func (*OSSCSVDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}

func (*OSSCSVDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "oss" || strings.TrimSpace(parsed.Host) == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid OSS CSV URI; expected oss://bucket/path/file.csv")
	}
	key := strings.TrimPrefix(parsed.EscapedPath(), "/")
	decoded, err := url.PathUnescape(key)
	if err != nil || !validOSSCSVObjectKey(decoded) {
		return errors.New("invalid OSS CSV URI; expected a safe relative .csv object key")
	}
	return nil
}

func (*OSSCSVDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "oss://"
	}
	key := OSSCSVObjectKey(config)
	return (&url.URL{Scheme: "oss", Host: strings.TrimSpace(config.Database), Path: "/" + key}).String()
}

// NormalizeOSSCSVConfig validates the single-object OSS CSV connection profile.
func NormalizeOSSCSVConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, string, error) {
	if config == nil {
		return nil, "", errors.New("oss-csv data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" || bucket == "." || bucket == ".." || strings.ContainsAny(bucket, "/\\") {
		return nil, "", errors.New("oss-csv bucket must be one path-safe name in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, "", errors.New("oss-csv access key ID and secret are required")
	}
	if !validOSSCSVObjectKey(OSSCSVObjectKey(config)) {
		return nil, "", errors.New("oss-csv object_key must be a safe relative .csv object key")
	}
	endpoint, err := ossCSVEndpointFromConfig(config)
	if err != nil {
		return nil, "", err
	}
	if _, err := ossCSVMaxFileBytes(config); err != nil {
		return nil, "", err
	}
	delimiter := configString(config.AdditionalProps, "delimiter")
	if delimiter != "" && (!utf8.ValidString(delimiter) || len([]rune(delimiter)) != 1) {
		return nil, "", errors.New("oss-csv delimiter must be exactly one character")
	}
	quote := configString(config.AdditionalProps, "quote_char")
	if quote != "" && quote != "\"" {
		return nil, "", errors.New("oss-csv currently supports the standard double-quote CSV escape character")
	}
	if _, err := ossCSVNonNegativeInt(config.AdditionalProps, "skip_header_rows"); err != nil {
		return nil, "", fmt.Errorf("oss-csv skip_header_rows must be a non-negative integer: %w", err)
	}
	if value := strings.ToLower(configString(config.AdditionalProps, "has_header")); value != "" && value != "true" && value != "false" && value != "1" && value != "0" {
		return nil, "", errors.New("oss-csv has_header must be true or false")
	}
	normalized := *config
	normalized.AdditionalProps = make(map[string]interface{}, len(config.AdditionalProps)+1)
	for key, value := range config.AdditionalProps {
		normalized.AdditionalProps[key] = value
	}
	normalized.AdditionalProps["endpoint_url"] = endpoint
	return &normalized, endpoint, nil
}

func ValidateOSSCSVConfig(config *model.DataSourceConfig) error {
	_, _, err := NormalizeOSSCSVConfig(config)
	return err
}

func ossCSVEndpointFromConfig(config *model.DataSourceConfig) (string, error) {
	raw := configString(config.AdditionalProps, "endpoint_url")
	if raw == "" {
		raw = configString(config.AdditionalProps, "endpoint")
	}
	if raw == "" {
		return "", errors.New("oss-csv endpoint_url is required in additionalProps")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("oss-csv endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return parsed.String(), nil
}

func ossCSVSecurityToken(config *model.DataSourceConfig) string {
	token := configString(config.AdditionalProps, "session_token")
	if token == "" {
		token = configString(config.AdditionalProps, "security_token")
	}
	return token
}

func ossCSVMaxFileBytes(config *model.DataSourceConfig) (int64, error) {
	if config == nil {
		return defaultOSSCSVMaxFileBytes, nil
	}
	value, exists := config.AdditionalProps["max_file_bytes"]
	if !exists || value == nil {
		return defaultOSSCSVMaxFileBytes, nil
	}
	parsed, err := parseOSSCSVInt64(value)
	if err != nil {
		return 0, errors.New("oss-csv max_file_bytes must be an integer")
	}
	if parsed <= 0 || parsed > maxOSSCSVMaxFileBytes {
		return 0, fmt.Errorf("oss-csv max_file_bytes must be between 1 and %d", maxOSSCSVMaxFileBytes)
	}
	return parsed, nil
}

func ossCSVNonNegativeInt(props map[string]interface{}, key string) (int, error) {
	value, exists := props[key]
	if !exists || value == nil {
		return 0, nil
	}
	parsed, err := parseOSSCSVInt64(value)
	if err != nil || parsed < 0 || uint64(parsed) > uint64(^uint(0)>>1) {
		return 0, errors.New("invalid value")
	}
	return int(parsed), nil
}

func parseOSSCSVInt64(value interface{}) (int64, error) {
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

func validOSSCSVObjectKey(key string) bool {
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

func OSSCSVObjectKey(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if key := configString(config.AdditionalProps, "object_key"); key != "" {
		return key
	}
	return strings.TrimSpace(config.Host)
}

func OSSCSVTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "oss-csv")
}

func OSSCSVMaxFileBytes(config *model.DataSourceConfig) int64 {
	limit, _ := ossCSVMaxFileBytes(config)
	return limit
}

func OSSCSVDelimiter(config *model.DataSourceConfig) rune {
	value := configString(config.AdditionalProps, "delimiter")
	if value == "" {
		return ','
	}
	return []rune(value)[0]
}

func OSSCSVHasHeader(config *model.DataSourceConfig) bool {
	value := strings.ToLower(configString(config.AdditionalProps, "has_header"))
	return value == "" || value == "true" || value == "1"
}

func OSSCSVSkipHeaderRows(config *model.DataSourceConfig) int {
	value, err := ossCSVNonNegativeInt(config.AdditionalProps, "skip_header_rows")
	if err != nil {
		return -1
	}
	return value
}
