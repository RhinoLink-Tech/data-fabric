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
	defaultOSSORCMaxFileBytes int64 = 16 << 20
	maxOSSORCMaxFileBytes     int64 = 64 << 20
	defaultOSSORCMaxRows            = 100000
	maxOSSORCMaxRows                = 100000
)

// OSSORCDriver exposes one bounded OSS ORC object as a read-only SQL table.
type OSSORCDriver struct{}

var _ drivers.Driver = (*OSSORCDriver)(nil)

func (*OSSORCDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("OSS ORC requires a configured read-only SQL adapter")
}

func (*OSSORCDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	normalized, err := NormalizeOSSORCConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := OSSORCObjectKey(normalized)
	tableName, err := OSSORCTableName(normalized)
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
		return nil, fmt.Errorf("create OSS ORC client: %w", err)
	}
	maxBytes, _ := ossORCMaxFileBytes(normalized)
	maxRows, _ := ossORCMaxRows(normalized)
	reader := NewORCReaderWithReadFunc(&ORCReaderConfig{BatchSize: 1000}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read OSS ORC object: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if int64(len(data)) > maxBytes {
			return nil, fmt.Errorf("OSS ORC object is %d bytes; configured maximum is %d bytes", len(data), maxBytes)
		}
		return data, nil
	})
	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "oss-orc",
		objectKey:  objectKey,
		tableName:  tableName,
		ping: func(ctx context.Context, key string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			metadata, err := client.GetObjectMetadata(ctx, key)
			if err != nil {
				return fmt.Errorf("inspect OSS ORC object: %w", err)
			}
			if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
				return fmt.Errorf("OSS ORC object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
			}
			return nil
		},
		getSchema: func(ctx context.Context, key string) (*objectSQLSchema, error) {
			file, err := reader.Read(ctx, key)
			if err != nil {
				return nil, err
			}
			defer file.Close()
			schema, err := file.GetSchema()
			if err != nil {
				return nil, err
			}
			return objectSQLSchemaFromORC(schema)
		},
		readRows: func(ctx context.Context, key string) (*objectSQLSchema, []map[string]interface{}, error) {
			file, err := reader.Read(ctx, key)
			if err != nil {
				return nil, nil, err
			}
			defer file.Close()
			schema, err := file.GetSchema()
			if err != nil {
				return nil, nil, err
			}
			rows, err := file.ReadNumRows(maxRows + 1)
			if err != nil {
				return nil, nil, err
			}
			if len(rows) > maxRows {
				return nil, nil, fmt.Errorf("OSS ORC row limit exceeded: maximum %d", maxRows)
			}
			objectSchema, err := objectSQLSchemaFromORC(schema)
			if err != nil {
				return nil, nil, err
			}
			return objectSchema, rows, nil
		},
	}), nil
}

func (*OSSORCDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "oss-orc" || strings.TrimSpace(parsed.Host) == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid OSS ORC URI; expected oss-orc://container/path/file.orc")
	}
	key, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || !validOSSORCObjectKey(key) {
		return errors.New("invalid OSS ORC URI; expected a safe relative .orc object key")
	}
	return nil
}

func (*OSSORCDriver) GetDefaultPort() int         { return 443 }
func (*OSSORCDriver) GetDatabaseTypeName() string { return "oss_orc" }
func (*OSSORCDriver) GetDriverName() string       { return "oss-orc" }
func (*OSSORCDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*OSSORCDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*OSSORCDriver) ConfigureAuth(interface{}) error { return nil }
func (*OSSORCDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("OSS ORC SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*OSSORCDriver) TestConnectionContext(context.Context) error {
	return errors.New("OSS ORC connectivity must be checked with PingContext on the database opened from its configuration")
}
func (*OSSORCDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}

func (*OSSORCDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "oss-orc://"
	}
	return (&url.URL{
		Scheme: "oss-orc", Host: strings.TrimSpace(config.Database),
		Path: "/" + OSSORCObjectKey(config),
	}).String()
}

// NormalizeOSSORCConfig validates a single-object Alibaba OSS ORC profile.
func NormalizeOSSORCConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("oss-orc data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" || bucket == "." || bucket == ".." || strings.ContainsAny(bucket, "/\\") {
		return nil, errors.New("oss-orc bucket must be one path-safe name in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("oss-orc access key ID and secret are required")
	}
	endpoint, err := ossORCEndpointFromConfig(config)
	if err != nil {
		return nil, err
	}
	if !validOSSORCObjectKey(OSSORCObjectKey(config)) {
		return nil, errors.New("oss-orc object_key must be a safe relative .orc object key")
	}
	if _, err := ossORCMaxFileBytes(config); err != nil {
		return nil, err
	}
	if _, err := ossORCMaxRows(config); err != nil {
		return nil, err
	}
	if value := strings.ToLower(configString(config.AdditionalProps, "force_path_style")); value != "" && value != "true" && value != "false" && value != "1" && value != "0" {
		return nil, errors.New("oss-orc force_path_style must be true or false")
	}
	normalized := *config
	normalized.AdditionalProps = make(map[string]interface{}, len(config.AdditionalProps)+1)
	for key, value := range config.AdditionalProps {
		normalized.AdditionalProps[key] = value
	}
	normalized.AdditionalProps["endpoint_url"] = endpoint
	return &normalized, nil
}

func ossORCEndpointFromConfig(config *model.DataSourceConfig) (string, error) {
	raw := configString(config.AdditionalProps, "endpoint_url")
	if raw == "" {
		raw = configString(config.AdditionalProps, "endpoint")
	}
	if raw == "" {
		return "", errors.New("oss-orc endpoint_url is required in additionalProps")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("oss-orc endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return parsed.String(), nil
}

func ValidateOSSORCConfig(config *model.DataSourceConfig) error {
	_, err := NormalizeOSSORCConfig(config)
	return err
}

func ossORCMaxFileBytes(config *model.DataSourceConfig) (int64, error) {
	if config == nil || config.AdditionalProps["max_file_bytes"] == nil {
		return defaultOSSORCMaxFileBytes, nil
	}
	value, err := parseOSSCSVInt64(config.AdditionalProps["max_file_bytes"])
	if err != nil {
		return 0, errors.New("oss-orc max_file_bytes must be an integer")
	}
	if value <= 0 || value > maxOSSORCMaxFileBytes {
		return 0, fmt.Errorf("oss-orc max_file_bytes must be between 1 and %d", maxOSSORCMaxFileBytes)
	}
	return value, nil
}

func ossORCMaxRows(config *model.DataSourceConfig) (int, error) {
	if config == nil || config.AdditionalProps["max_rows"] == nil {
		return defaultOSSORCMaxRows, nil
	}
	value, err := parseOSSCSVInt64(config.AdditionalProps["max_rows"])
	if err != nil || value <= 0 || value > maxOSSORCMaxRows {
		return 0, fmt.Errorf("oss-orc max_rows must be between 1 and %d", maxOSSORCMaxRows)
	}
	return int(value), nil
}

func validOSSORCObjectKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || path.IsAbs(key) || strings.Contains(key, "\\") || strings.ToLower(path.Ext(key)) != ".orc" {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func OSSORCObjectKey(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if key := configString(config.AdditionalProps, "object_key"); key != "" {
		return key
	}
	return strings.TrimSpace(config.Host)
}

func OSSORCTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "oss-orc")
}
