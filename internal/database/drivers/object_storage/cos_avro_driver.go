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
	defaultCOSAvroMaxFileBytes int64 = 16 << 20
	maxCOSAvroMaxFileBytes     int64 = 64 << 20
	defaultCOSAvroMaxRows            = 100000
	maxCOSAvroMaxRows                = 100000
)

// COSAvroDriver exposes one bounded Tencent COS Avro OCF object as a read-only SQL table.
type COSAvroDriver struct{}

var _ drivers.Driver = (*COSAvroDriver)(nil)

func (*COSAvroDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("COS Avro requires a configured read-only SQL adapter")
}

func (*COSAvroDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	normalized, err := NormalizeCOSAvroConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := COSAvroObjectKey(normalized)
	tableName, err := COSAvroTableName(normalized)
	if err != nil {
		return nil, err
	}
	client, err := NewCOSClient(ctx, &COSConfig{
		SecretID: normalized.Username, SecretKey: normalized.Password,
		Region:      configString(normalized.AdditionalProps, "region"),
		BucketName:  strings.TrimSpace(normalized.Database),
		HTTPS:       configBool(normalized.AdditionalProps, "https", true),
		EndpointURL: configString(normalized.AdditionalProps, "endpoint_url"),
	})
	if err != nil {
		return nil, fmt.Errorf("create COS Avro client: %w", err)
	}
	maxBytes, _ := cosAvroMaxFileBytes(normalized)
	maxRows, _ := cosAvroMaxRows(normalized)
	return NewAvroSQLDatabase(AvroSQLDatabaseConfig{
		DriverName: "cos-avro", ObjectKey: objectKey, TableName: tableName,
		BatchSize: 1000, MaxFileBytes: maxBytes, MaxRows: maxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetObjectMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect COS Avro object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("COS Avro object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read COS Avro object: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, nil)
}

func (*COSAvroDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "cos-avro" || strings.TrimSpace(parsed.Host) == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid COS Avro URI; expected cos-avro://bucket/path/file.avro")
	}
	key, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || !validCOSAvroObjectKey(key) {
		return errors.New("invalid COS Avro URI; expected a safe relative .avro object key")
	}
	return nil
}

func (*COSAvroDriver) GetDefaultPort() int         { return 443 }
func (*COSAvroDriver) GetDatabaseTypeName() string { return "cos_avro" }
func (*COSAvroDriver) GetDriverName() string       { return "cos-avro" }
func (*COSAvroDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*COSAvroDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*COSAvroDriver) ConfigureAuth(interface{}) error { return nil }
func (*COSAvroDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("COS Avro SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*COSAvroDriver) TestConnectionContext(context.Context) error {
	return errors.New("COS Avro connectivity must be checked with PingContext on the database opened from its configuration")
}
func (*COSAvroDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}

func (*COSAvroDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "cos-avro://"
	}
	return (&url.URL{Scheme: "cos-avro", Host: strings.TrimSpace(config.Database), Path: "/" + COSAvroObjectKey(config)}).String()
}

// NormalizeCOSAvroConfig validates a single-object Tencent COS Avro profile.
func NormalizeCOSAvroConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("cos-avro data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" || bucket == "." || bucket == ".." || strings.ContainsAny(bucket, "/\\") {
		return nil, errors.New("cos-avro bucket must be one path-safe name in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("cos-avro SecretId and SecretKey are required")
	}
	if strings.TrimSpace(configString(config.AdditionalProps, "region")) == "" {
		return nil, errors.New("cos-avro region is required in additionalProps")
	}
	if !validCOSAvroObjectKey(COSAvroObjectKey(config)) {
		return nil, errors.New("cos-avro object_key must be a safe relative .avro object key")
	}
	if endpoint := configString(config.AdditionalProps, "endpoint_url"); endpoint != "" {
		if err := validateCOSEndpoint(endpoint); err != nil {
			return nil, err
		}
	}
	if value := strings.ToLower(configString(config.AdditionalProps, "https")); value != "" && value != "true" && value != "false" && value != "1" && value != "0" {
		return nil, errors.New("cos-avro https must be true or false")
	}
	if _, err := cosAvroMaxFileBytes(config); err != nil {
		return nil, err
	}
	if _, err := cosAvroMaxRows(config); err != nil {
		return nil, err
	}
	normalized := *config
	normalized.AdditionalProps = make(map[string]interface{}, len(config.AdditionalProps))
	for key, value := range config.AdditionalProps {
		normalized.AdditionalProps[key] = value
	}
	return &normalized, nil
}
func ValidateCOSAvroConfig(config *model.DataSourceConfig) error {
	_, err := NormalizeCOSAvroConfig(config)
	return err
}

func cosAvroMaxFileBytes(config *model.DataSourceConfig) (int64, error) {
	if config == nil || config.AdditionalProps["max_file_bytes"] == nil {
		return defaultCOSAvroMaxFileBytes, nil
	}
	value, err := parseCOSCSVInt64(config.AdditionalProps["max_file_bytes"])
	if err != nil {
		return 0, errors.New("cos-avro max_file_bytes must be an integer")
	}
	if value <= 0 || value > maxCOSAvroMaxFileBytes {
		return 0, fmt.Errorf("cos-avro max_file_bytes must be between 1 and %d", maxCOSAvroMaxFileBytes)
	}
	return value, nil
}

func cosAvroMaxRows(config *model.DataSourceConfig) (int, error) {
	if config == nil || config.AdditionalProps["max_rows"] == nil {
		return defaultCOSAvroMaxRows, nil
	}
	value, err := parseCOSCSVInt64(config.AdditionalProps["max_rows"])
	if err != nil || value <= 0 || value > maxCOSAvroMaxRows {
		return 0, fmt.Errorf("cos-avro max_rows must be between 1 and %d", maxCOSAvroMaxRows)
	}
	return int(value), nil
}

func validCOSAvroObjectKey(key string) bool {
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

func COSAvroObjectKey(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if key := configString(config.AdditionalProps, "object_key"); key != "" {
		return key
	}
	return strings.TrimSpace(config.Host)
}

func COSAvroTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "cos-avro")
}
