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
	defaultOSSJSONMaxFileBytes int64 = 16 << 20
	maxOSSJSONMaxFileBytes     int64 = 64 << 20
	defaultOSSJSONMaxRecords         = 100000
	maxOSSJSONMaxRecords             = 100000
)

// OSSJSONDriver exposes one bounded Alibaba OSS JSON object as a read-only SQL table.
type OSSJSONDriver struct{}

var _ drivers.Driver = (*OSSJSONDriver)(nil)

func (*OSSJSONDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("OSS JSON requires a configured read-only SQL adapter")
}

func (*OSSJSONDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	normalized, err := NormalizeOSSJSONConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := OSSJSONObjectKey(normalized)
	tableName, err := OSSJSONTableName(normalized)
	if err != nil {
		return nil, err
	}
	client, err := NewOSSClient(ctx, &OSSConfig{
		Endpoint:    configString(normalized.AdditionalProps, "endpoint_url"),
		AccessKeyID: normalized.Username, AccessKeySecret: normalized.Password,
		BucketName: strings.TrimSpace(normalized.Database), SecurityToken: ossCSVSecurityToken(normalized),
		ForcePathStyle: configBool(normalized.AdditionalProps, "force_path_style", false),
	})
	if err != nil {
		return nil, fmt.Errorf("create OSS JSON client: %w", err)
	}
	maxBytes, _ := ossJSONMaxFileBytes(normalized)
	maxRecords, _ := ossJSONMaxRecords(normalized)
	return NewJSONSQLDatabase(JSONSQLDatabaseConfig{
		DriverName: "oss-json", ObjectKey: objectKey, TableName: tableName,
		MaxFileBytes: maxBytes, MaxRecords: maxRecords,
		FlattenNested: configBool(normalized.AdditionalProps, "flatten_nested", false),
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetObjectMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect OSS JSON object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("OSS JSON object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string, limit int64) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, limit)
		if err != nil {
			return nil, fmt.Errorf("read OSS JSON object: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, nil)
}

func (*OSSJSONDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "oss-json" || strings.TrimSpace(parsed.Host) == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid OSS JSON URI; expected oss-json://bucket/path/file.json")
	}
	key, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || !validOSSJSONObjectKey(key) {
		return errors.New("invalid OSS JSON URI; expected a safe relative .json, .jsonl, or .ndjson object key")
	}
	return nil
}

func (*OSSJSONDriver) GetDefaultPort() int         { return 443 }
func (*OSSJSONDriver) GetDatabaseTypeName() string { return "oss_json" }
func (*OSSJSONDriver) GetDriverName() string       { return "oss-json" }
func (*OSSJSONDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*OSSJSONDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*OSSJSONDriver) ConfigureAuth(interface{}) error { return nil }
func (*OSSJSONDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("OSS JSON SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*OSSJSONDriver) TestConnectionContext(context.Context) error {
	return errors.New("OSS JSON connectivity must be checked with PingContext on the database opened from its configuration")
}
func (*OSSJSONDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return ApplyJSONBatchPagination(query, batchSize, offset)
}

func (*OSSJSONDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "oss-json://"
	}
	return (&url.URL{Scheme: "oss-json", Host: strings.TrimSpace(config.Database), Path: "/" + OSSJSONObjectKey(config)}).String()
}

// NormalizeOSSJSONConfig validates a single-object Alibaba OSS JSON profile.
func NormalizeOSSJSONConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("oss-json data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" || bucket == "." || bucket == ".." || strings.ContainsAny(bucket, "/\\") {
		return nil, errors.New("oss-json bucket must be one path-safe name in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("oss-json access key ID and secret are required")
	}
	endpoint, err := ossJSONEndpointFromConfig(config)
	if err != nil {
		return nil, err
	}
	if !validOSSJSONObjectKey(OSSJSONObjectKey(config)) {
		return nil, errors.New("oss-json object_key must be a safe relative .json, .jsonl, or .ndjson object key")
	}
	if _, err := ossJSONMaxFileBytes(config); err != nil {
		return nil, err
	}
	if _, err := ossJSONMaxRecords(config); err != nil {
		return nil, err
	}
	for _, key := range []string{"force_path_style", "flatten_nested"} {
		if value := strings.ToLower(configString(config.AdditionalProps, key)); value != "" && value != "true" && value != "false" && value != "1" && value != "0" {
			return nil, fmt.Errorf("oss-json %s must be true or false", key)
		}
	}
	normalized := *config
	normalized.AdditionalProps = make(map[string]interface{}, len(config.AdditionalProps))
	for key, value := range config.AdditionalProps {
		normalized.AdditionalProps[key] = value
	}
	normalized.AdditionalProps["endpoint_url"] = endpoint
	return &normalized, nil
}

func ossJSONEndpointFromConfig(config *model.DataSourceConfig) (string, error) {
	raw := configString(config.AdditionalProps, "endpoint_url")
	if raw == "" {
		raw = configString(config.AdditionalProps, "endpoint")
	}
	if raw == "" {
		return "", errors.New("oss-json endpoint_url is required in additionalProps")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("oss-json endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return parsed.String(), nil
}

func ValidateOSSJSONConfig(config *model.DataSourceConfig) error {
	_, err := NormalizeOSSJSONConfig(config)
	return err
}

func ossJSONMaxFileBytes(config *model.DataSourceConfig) (int64, error) {
	if config == nil || config.AdditionalProps["max_file_bytes"] == nil {
		return defaultOSSJSONMaxFileBytes, nil
	}
	value, err := parseOSSCSVInt64(config.AdditionalProps["max_file_bytes"])
	if err != nil {
		return 0, errors.New("oss-json max_file_bytes must be an integer")
	}
	if value <= 0 || value > maxOSSJSONMaxFileBytes {
		return 0, fmt.Errorf("oss-json max_file_bytes must be between 1 and %d", maxOSSJSONMaxFileBytes)
	}
	return value, nil
}

func ossJSONMaxRecords(config *model.DataSourceConfig) (int, error) {
	if config == nil || config.AdditionalProps["max_records"] == nil {
		return defaultOSSJSONMaxRecords, nil
	}
	value, err := parseOSSCSVInt64(config.AdditionalProps["max_records"])
	if err != nil || value <= 0 || value > maxOSSJSONMaxRecords {
		return 0, fmt.Errorf("oss-json max_records must be between 1 and %d", maxOSSJSONMaxRecords)
	}
	return int(value), nil
}

func validOSSJSONObjectKey(key string) bool {
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

func OSSJSONObjectKey(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if key := configString(config.AdditionalProps, "object_key"); key != "" {
		return key
	}
	return strings.TrimSpace(config.Host)
}

func OSSJSONTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "oss-json")
}
