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
	defaultOSSAvroMaxFileBytes int64 = 16 << 20
	maxOSSAvroMaxFileBytes     int64 = 64 << 20
	defaultOSSAvroMaxRows            = 100000
	maxOSSAvroMaxRows                = 100000
)

// OSSAvroDriver exposes one bounded Alibaba OSS Avro OCF object as a read-only SQL table.
type OSSAvroDriver struct{}

var _ drivers.Driver = (*OSSAvroDriver)(nil)

func (*OSSAvroDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("OSS Avro requires a configured read-only SQL adapter")
}

func (*OSSAvroDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	normalized, err := NormalizeOSSAvroConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := OSSAvroObjectKey(normalized)
	tableName, err := OSSAvroTableName(normalized)
	if err != nil {
		return nil, err
	}
	client, err := NewOSSClient(ctx, &OSSConfig{
		Endpoint: configString(normalized.AdditionalProps, "endpoint_url"), AccessKeyID: normalized.Username,
		AccessKeySecret: normalized.Password, BucketName: strings.TrimSpace(normalized.Database),
		SecurityToken:  ossCSVSecurityToken(normalized),
		ForcePathStyle: configBool(normalized.AdditionalProps, "force_path_style", false),
	})
	if err != nil {
		return nil, fmt.Errorf("create OSS Avro client: %w", err)
	}
	maxBytes, _ := ossAvroMaxFileBytes(normalized)
	maxRows, _ := ossAvroMaxRows(normalized)
	return NewAvroSQLDatabase(AvroSQLDatabaseConfig{
		DriverName: "oss-avro", ObjectKey: objectKey, TableName: tableName,
		BatchSize: 1000, MaxFileBytes: maxBytes, MaxRows: maxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetObjectMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect OSS Avro object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("OSS Avro object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read OSS Avro object: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, nil)
}

func (*OSSAvroDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "oss-avro" || strings.TrimSpace(parsed.Host) == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid OSS Avro URI; expected oss-avro://bucket/path/file.avro")
	}
	key, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || !validOSSAvroObjectKey(key) {
		return errors.New("invalid OSS Avro URI; expected a safe relative .avro object key")
	}
	return nil
}

func (*OSSAvroDriver) GetDefaultPort() int         { return 443 }
func (*OSSAvroDriver) GetDatabaseTypeName() string { return "oss_avro" }
func (*OSSAvroDriver) GetDriverName() string       { return "oss-avro" }
func (*OSSAvroDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*OSSAvroDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*OSSAvroDriver) ConfigureAuth(interface{}) error { return nil }
func (*OSSAvroDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("OSS Avro SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*OSSAvroDriver) TestConnectionContext(context.Context) error {
	return errors.New("OSS Avro connectivity must be checked with PingContext on the database opened from its configuration")
}
func (*OSSAvroDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}

func (*OSSAvroDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "oss-avro://"
	}
	return (&url.URL{Scheme: "oss-avro", Host: strings.TrimSpace(config.Database), Path: "/" + OSSAvroObjectKey(config)}).String()
}

// NormalizeOSSAvroConfig validates a single-object Alibaba OSS Avro profile.
func NormalizeOSSAvroConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("oss-avro data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" || bucket == "." || bucket == ".." || strings.ContainsAny(bucket, "/\\") {
		return nil, errors.New("oss-avro bucket must be one path-safe name in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("oss-avro access key ID and secret are required")
	}
	endpoint, err := ossAvroEndpointFromConfig(config)
	if err != nil {
		return nil, err
	}
	if !validOSSAvroObjectKey(OSSAvroObjectKey(config)) {
		return nil, errors.New("oss-avro object_key must be a safe relative .avro object key")
	}
	if _, err := ossAvroMaxFileBytes(config); err != nil {
		return nil, err
	}
	if _, err := ossAvroMaxRows(config); err != nil {
		return nil, err
	}
	if value := strings.ToLower(configString(config.AdditionalProps, "force_path_style")); value != "" && value != "true" && value != "false" && value != "1" && value != "0" {
		return nil, errors.New("oss-avro force_path_style must be true or false")
	}
	normalized := *config
	normalized.AdditionalProps = make(map[string]interface{}, len(config.AdditionalProps)+1)
	for key, value := range config.AdditionalProps {
		normalized.AdditionalProps[key] = value
	}
	normalized.AdditionalProps["endpoint_url"] = endpoint
	return &normalized, nil
}

func ossAvroEndpointFromConfig(config *model.DataSourceConfig) (string, error) {
	raw := configString(config.AdditionalProps, "endpoint_url")
	if raw == "" {
		raw = configString(config.AdditionalProps, "endpoint")
	}
	if raw == "" {
		return "", errors.New("oss-avro endpoint_url is required in additionalProps")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("oss-avro endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return parsed.String(), nil
}

func ValidateOSSAvroConfig(config *model.DataSourceConfig) error {
	_, err := NormalizeOSSAvroConfig(config)
	return err
}

func ossAvroMaxFileBytes(config *model.DataSourceConfig) (int64, error) {
	if config == nil || config.AdditionalProps["max_file_bytes"] == nil {
		return defaultOSSAvroMaxFileBytes, nil
	}
	value, err := parseOSSCSVInt64(config.AdditionalProps["max_file_bytes"])
	if err != nil {
		return 0, errors.New("oss-avro max_file_bytes must be an integer")
	}
	if value <= 0 || value > maxOSSAvroMaxFileBytes {
		return 0, fmt.Errorf("oss-avro max_file_bytes must be between 1 and %d", maxOSSAvroMaxFileBytes)
	}
	return value, nil
}

func ossAvroMaxRows(config *model.DataSourceConfig) (int, error) {
	if config == nil || config.AdditionalProps["max_rows"] == nil {
		return defaultOSSAvroMaxRows, nil
	}
	value, err := parseOSSCSVInt64(config.AdditionalProps["max_rows"])
	if err != nil || value <= 0 || value > maxOSSAvroMaxRows {
		return 0, fmt.Errorf("oss-avro max_rows must be between 1 and %d", maxOSSAvroMaxRows)
	}
	return int(value), nil
}

func validOSSAvroObjectKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || path.IsAbs(key) || strings.Contains(key, "\\") || strings.ToLower(path.Ext(key)) != ".avro" {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func OSSAvroObjectKey(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if key := configString(config.AdditionalProps, "object_key"); key != "" {
		return key
	}
	return strings.TrimSpace(config.Host)
}

func OSSAvroTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "oss-avro")
}
