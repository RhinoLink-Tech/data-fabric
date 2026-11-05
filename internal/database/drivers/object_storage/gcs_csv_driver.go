package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	defaultGCSCSVEndpoint     = "https://storage.googleapis.com"
	defaultGCSCSVRegion       = "us-east-1"
	defaultGCSCSVMaxFileBytes = int64(16 << 20)
	maxGCSCSVMaxFileBytes     = int64(64 << 20)
	gcsCSVMaxRows             = 100000
)

// GCSCSVDriver exposes one bounded Google Cloud Storage CSV object as a
// read-only SQL table. It uses GCS XML API interoperability with HMAC keys.
type GCSCSVDriver struct{}

func (*GCSCSVDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("GCS CSV requires a configured read-only SQL adapter")
}

func (*GCSCSVDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	normalized, endpoint, err := NormalizeGCSCSVConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := GCSCSVObjectKey(normalized)
	tableName, err := GCSCSVTableName(normalized)
	if err != nil {
		return nil, err
	}
	client, err := NewS3Client(ctx, &S3Config{
		Region: configString(normalized.AdditionalProps, "region"),
		Bucket: strings.TrimSpace(normalized.Database), AccessKey: normalized.Username, SecretKey: normalized.Password,
		EndpointURL: endpoint, ForcePathStyle: configBool(normalized.AdditionalProps, "force_path_style", true),
	})
	if err != nil {
		return nil, fmt.Errorf("create GCS XML API interoperability client: %w", err)
	}
	maxBytes := GCSCSVMaxFileBytes(normalized)
	return NewCSVSQLDatabase(CSVSQLDatabaseConfig{
		DriverName: "gcs-csv", ObjectKey: objectKey, TableName: tableName,
		Delimiter: GCSCSVDelimiter(normalized), HasHeader: GCSCSVHasHeader(normalized),
		SkipHeaderRows: GCSCSVSkipHeaderRows(normalized), NullValues: configStringSlice(normalized.AdditionalProps, "null_values"),
		MaxRows: gcsCSVMaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetObjectMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect GCS CSV object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("GCS CSV object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read GCS CSV object: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, nil)
}

func (*GCSCSVDriver) GetDefaultPort() int         { return 443 }
func (*GCSCSVDriver) GetDatabaseTypeName() string { return "gcs_csv" }
func (*GCSCSVDriver) GetDriverName() string       { return "gcs-csv" }
func (*GCSCSVDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*GCSCSVDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*GCSCSVDriver) ConfigureAuth(interface{}) error { return nil }
func (*GCSCSVDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("GCS CSV SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*GCSCSVDriver) TestConnectionContext(context.Context) error {
	return errors.New("GCS CSV connectivity must be checked with PingContext on the database opened from its configuration")
}
func (*GCSCSVDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}

func (*GCSCSVDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "gcs" || strings.TrimSpace(parsed.Host) == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid GCS CSV URI; expected gcs://bucket/path/file.csv")
	}
	key, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || !validGCSCSVObjectKey(key) {
		return errors.New("invalid GCS CSV URI; expected a safe relative .csv object key")
	}
	return nil
}

func (*GCSCSVDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "gcs://"
	}
	return (&url.URL{Scheme: "gcs", Host: strings.TrimSpace(config.Database), Path: "/" + GCSCSVObjectKey(config)}).String()
}

// NormalizeGCSCSVConfig validates the one-object GCS CSV connection profile.
func NormalizeGCSCSVConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, string, error) {
	if config == nil {
		return nil, "", errors.New("gcs-csv data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" || bucket == "." || bucket == ".." || strings.ContainsAny(bucket, "/\\") {
		return nil, "", errors.New("gcs-csv bucket must be one path-safe name in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, "", errors.New("gcs-csv HMAC access ID and secret are required")
	}
	if !validGCSCSVObjectKey(GCSCSVObjectKey(config)) {
		return nil, "", errors.New("gcs-csv object_key must be a safe relative .csv object key")
	}

	props := make(map[string]interface{}, len(config.AdditionalProps)+4)
	for key, value := range config.AdditionalProps {
		props[key] = value
	}
	endpoint, err := gcsCSVEndpoint(props)
	if err != nil {
		return nil, "", err
	}
	region := configString(props, "region")
	if region == "" {
		region = defaultGCSCSVRegion
	}
	if !validGCSCSVRegion(region) {
		return nil, "", errors.New("gcs-csv region must contain only letters, digits, and hyphens")
	}
	forcePathStyle, err := gcsCSVBool(props, "force_path_style", true)
	if err != nil {
		return nil, "", err
	}
	if _, err := GCSCSVMaxFileBytesFromProps(props); err != nil {
		return nil, "", err
	}
	delimiter := configString(props, "delimiter")
	if delimiter != "" && (!utf8.ValidString(delimiter) || len([]rune(delimiter)) != 1) {
		return nil, "", errors.New("gcs-csv delimiter must be exactly one character")
	}
	quote := configString(props, "quote_char")
	if quote != "" && quote != "\"" {
		return nil, "", errors.New("gcs-csv currently supports the standard double-quote CSV escape character")
	}
	if _, err := gcsCSVNonNegativeInt(props, "skip_header_rows"); err != nil {
		return nil, "", fmt.Errorf("gcs-csv skip_header_rows must be a non-negative integer: %w", err)
	}
	if _, err := gcsCSVBool(props, "has_header", true); err != nil {
		return nil, "", err
	}
	props["endpoint_url"] = endpoint
	props["region"] = region
	props["force_path_style"] = forcePathStyle
	normalized := *config
	normalized.AdditionalProps = props
	return &normalized, endpoint, nil
}

func ValidateGCSCSVConfig(config *model.DataSourceConfig) error {
	_, _, err := NormalizeGCSCSVConfig(config)
	return err
}

func gcsCSVEndpoint(props map[string]interface{}) (string, error) {
	raw := configString(props, "endpoint_url")
	if raw == "" {
		raw = defaultGCSCSVEndpoint
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("gcs-csv endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validGCSCSVRegion(region string) bool {
	if strings.TrimSpace(region) == "" || strings.TrimSpace(region) != region {
		return false
	}
	for _, char := range region {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && char != '-' {
			return false
		}
	}
	return true
}

func gcsCSVBool(props map[string]interface{}, key string, defaultValue bool) (bool, error) {
	value, exists := props[key]
	if !exists || value == nil {
		return defaultValue, nil
	}
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, fmt.Errorf("gcs-csv %s must be true or false", key)
	}
}

func gcsCSVNonNegativeInt(props map[string]interface{}, key string) (int, error) {
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

func GCSCSVMaxFileBytesFromProps(props map[string]interface{}) (int64, error) {
	value, exists := props["max_file_bytes"]
	if !exists || value == nil {
		return defaultGCSCSVMaxFileBytes, nil
	}
	parsed, err := parseCOSCSVInt64(value)
	if err != nil {
		return 0, errors.New("gcs-csv max_file_bytes must be an integer")
	}
	if parsed <= 0 || parsed > maxGCSCSVMaxFileBytes {
		return 0, fmt.Errorf("gcs-csv max_file_bytes must be between 1 and %d", maxGCSCSVMaxFileBytes)
	}
	return parsed, nil
}

func GCSCSVMaxFileBytes(config *model.DataSourceConfig) int64 {
	if config == nil {
		return defaultGCSCSVMaxFileBytes
	}
	maxBytes, err := GCSCSVMaxFileBytesFromProps(config.AdditionalProps)
	if err != nil {
		return defaultGCSCSVMaxFileBytes
	}
	return maxBytes
}

func validGCSCSVObjectKey(key string) bool {
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

func GCSCSVObjectKey(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if key := configString(config.AdditionalProps, "object_key"); key != "" {
		return key
	}
	return strings.TrimSpace(config.Host)
}

func GCSCSVTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "gcs-csv")
}

func GCSCSVDelimiter(config *model.DataSourceConfig) rune {
	value := configString(config.AdditionalProps, "delimiter")
	if value == "" {
		return ','
	}
	runes := []rune(value)
	if len(runes) != 1 {
		return ','
	}
	return runes[0]
}

func GCSCSVHasHeader(config *model.DataSourceConfig) bool {
	return configBool(config.AdditionalProps, "has_header", true)
}

func GCSCSVSkipHeaderRows(config *model.DataSourceConfig) int {
	rows, _ := gcsCSVNonNegativeInt(config.AdditionalProps, "skip_header_rows")
	return rows
}
