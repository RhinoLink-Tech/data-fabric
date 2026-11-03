package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	defaultCOSJSONMaxFileBytes int64 = 16 << 20
	maxCOSJSONMaxFileBytes     int64 = 64 << 20
	defaultCOSJSONMaxRecords         = 100000
	maxCOSJSONMaxRecords             = 100000
)

// COSJSONDriver exposes one bounded Tencent COS JSON object as a read-only SQL table.
type COSJSONDriver struct{}

var _ drivers.Driver = (*COSJSONDriver)(nil)

func (*COSJSONDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("COS JSON requires a configured read-only SQL adapter")
}

func (*COSJSONDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	normalized, err := NormalizeCOSJSONConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := COSJSONObjectKey(normalized)
	tableName, err := COSJSONTableName(normalized)
	if err != nil {
		return nil, err
	}
	client, err := NewCOSClient(ctx, &COSConfig{
		SecretID: normalized.Username, SecretKey: normalized.Password,
		Region: configString(normalized.AdditionalProps, "region"), BucketName: strings.TrimSpace(normalized.Database),
		HTTPS: configBool(normalized.AdditionalProps, "https", true), EndpointURL: configString(normalized.AdditionalProps, "endpoint_url"),
	})
	if err != nil {
		return nil, fmt.Errorf("create COS JSON client: %w", err)
	}
	maxBytes, _ := cosJSONMaxFileBytes(normalized)
	maxRecords, _ := cosJSONMaxRecords(normalized)
	return NewJSONSQLDatabase(JSONSQLDatabaseConfig{
		DriverName: "cos-json", ObjectKey: objectKey, TableName: tableName,
		MaxFileBytes: maxBytes, MaxRecords: maxRecords,
		FlattenNested: configBool(normalized.AdditionalProps, "flatten_nested", false),
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetObjectMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect COS JSON object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("COS JSON object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string, limit int64) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, limit)
		if err != nil {
			return nil, fmt.Errorf("read COS JSON object: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, nil)
}

func (*COSJSONDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "cos-json" || strings.TrimSpace(parsed.Host) == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid COS JSON URI; expected cos-json://bucket/path/file.json")
	}
	key, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || !validCOSJSONObjectKey(key) {
		return errors.New("invalid COS JSON URI; expected a safe relative .json, .jsonl, or .ndjson object key")
	}
	return nil
}

func (*COSJSONDriver) GetDefaultPort() int         { return 443 }
func (*COSJSONDriver) GetDatabaseTypeName() string { return "cos_json" }
func (*COSJSONDriver) GetDriverName() string       { return "cos-json" }
func (*COSJSONDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*COSJSONDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*COSJSONDriver) ConfigureAuth(interface{}) error { return nil }
func (*COSJSONDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("COS JSON SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*COSJSONDriver) TestConnectionContext(context.Context) error {
	return errors.New("COS JSON connectivity must be checked with PingContext on the database opened from its configuration")
}
func (*COSJSONDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return ApplyJSONBatchPagination(query, batchSize, offset)
}

func (*COSJSONDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "cos-json://"
	}
	return (&url.URL{Scheme: "cos-json", Host: strings.TrimSpace(config.Database), Path: "/" + COSJSONObjectKey(config)}).String()
}

// NormalizeCOSJSONConfig validates a single-object Tencent COS JSON profile.
func NormalizeCOSJSONConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("cos-json data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" || bucket == "." || bucket == ".." || strings.ContainsAny(bucket, "/\\") {
		return nil, errors.New("cos-json bucket must be one path-safe name in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("cos-json SecretId and SecretKey are required")
	}
	if configString(config.AdditionalProps, "region") == "" {
		return nil, errors.New("cos-json region is required in additionalProps")
	}
	if !validCOSJSONObjectKey(COSJSONObjectKey(config)) {
		return nil, errors.New("cos-json object_key must be a safe relative .json, .jsonl, or .ndjson object key")
	}
	if endpoint := configString(config.AdditionalProps, "endpoint_url"); endpoint != "" {
		if err := validateCOSEndpoint(endpoint); err != nil {
			return nil, err
		}
	}
	if _, err := cosJSONMaxFileBytes(config); err != nil {
		return nil, err
	}
	if _, err := cosJSONMaxRecords(config); err != nil {
		return nil, err
	}
	for _, key := range []string{"https", "flatten_nested"} {
		if value := strings.ToLower(configString(config.AdditionalProps, key)); value != "" && value != "true" && value != "false" && value != "1" && value != "0" {
			return nil, fmt.Errorf("cos-json %s must be true or false", key)
		}
	}
	normalized := *config
	normalized.AdditionalProps = make(map[string]interface{}, len(config.AdditionalProps))
	for key, value := range config.AdditionalProps {
		normalized.AdditionalProps[key] = value
	}
	return &normalized, nil
}

func ValidateCOSJSONConfig(config *model.DataSourceConfig) error {
	_, err := NormalizeCOSJSONConfig(config)
	return err
}

func cosJSONMaxFileBytes(config *model.DataSourceConfig) (int64, error) {
	if config == nil || config.AdditionalProps["max_file_bytes"] == nil {
		return defaultCOSJSONMaxFileBytes, nil
	}
	value, err := parseCOSCSVInt64(config.AdditionalProps["max_file_bytes"])
	if err != nil {
		return 0, errors.New("cos-json max_file_bytes must be an integer")
	}
	if value <= 0 || value > maxCOSJSONMaxFileBytes {
		return 0, fmt.Errorf("cos-json max_file_bytes must be between 1 and %d", maxCOSJSONMaxFileBytes)
	}
	return value, nil
}

func cosJSONMaxRecords(config *model.DataSourceConfig) (int, error) {
	if config == nil || config.AdditionalProps["max_records"] == nil {
		return defaultCOSJSONMaxRecords, nil
	}
	value, err := parseCOSCSVInt64(config.AdditionalProps["max_records"])
	if err != nil || value <= 0 || value > maxCOSJSONMaxRecords {
		return 0, fmt.Errorf("cos-json max_records must be between 1 and %d", maxCOSJSONMaxRecords)
	}
	return int(value), nil
}

func validCOSJSONObjectKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || path.IsAbs(key) || strings.Contains(key, "\\") {
		return false
	}
	extension := strings.ToLower(path.Ext(key))
	if extension != ".json" && extension != ".jsonl" && extension != ".ndjson" {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func COSJSONObjectKey(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if key := configString(config.AdditionalProps, "object_key"); key != "" {
		return key
	}
	return strings.TrimSpace(config.Host)
}

func COSJSONTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "cos-json")
}
