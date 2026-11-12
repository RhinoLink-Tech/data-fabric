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
	defaultCOSORCMaxFileBytes int64 = 16 << 20
	maxCOSORCMaxFileBytes     int64 = 64 << 20
	defaultCOSORCMaxRows            = 100000
	maxCOSORCMaxRows                = 100000
)

// COSORCDriver exposes one bounded COS ORC object as a read-only SQL table.
type COSORCDriver struct{}

var _ drivers.Driver = (*COSORCDriver)(nil)

func (*COSORCDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("COS ORC requires a configured read-only SQL adapter")
}

func (*COSORCDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	normalized, err := NormalizeCOSORCConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := COSORCObjectKey(normalized)
	tableName, err := COSORCTableName(normalized)
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
		return nil, fmt.Errorf("create COS ORC client: %w", err)
	}
	maxBytes, _ := cosORCMaxFileBytes(normalized)
	maxRows, _ := cosORCMaxRows(normalized)
	reader := NewORCReaderWithReadFunc(&ORCReaderConfig{BatchSize: 1000}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read COS ORC object: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if int64(len(data)) > maxBytes {
			return nil, fmt.Errorf("COS ORC object is %d bytes; configured maximum is %d bytes", len(data), maxBytes)
		}
		return data, nil
	})
	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "cos-orc",
		objectKey:  objectKey,
		tableName:  tableName,
		ping: func(ctx context.Context, key string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			metadata, err := client.GetObjectMetadata(ctx, key)
			if err != nil {
				return fmt.Errorf("inspect COS ORC object: %w", err)
			}
			if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
				return fmt.Errorf("COS ORC object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
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
				return nil, nil, fmt.Errorf("COS ORC row limit exceeded: maximum %d", maxRows)
			}
			objectSchema, err := objectSQLSchemaFromORC(schema)
			if err != nil {
				return nil, nil, err
			}
			return objectSchema, rows, nil
		},
	}), nil
}

func (*COSORCDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "cos-orc" || strings.TrimSpace(parsed.Host) == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid COS ORC URI; expected cos-orc://bucket/path/file.orc")
	}
	key, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || !validCOSORCObjectKey(key) {
		return errors.New("invalid COS ORC URI; expected a safe relative .orc object key")
	}
	return nil
}

func (*COSORCDriver) GetDefaultPort() int         { return 443 }
func (*COSORCDriver) GetDatabaseTypeName() string { return "cos_orc" }
func (*COSORCDriver) GetDriverName() string       { return "cos-orc" }
func (*COSORCDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*COSORCDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*COSORCDriver) ConfigureAuth(interface{}) error { return nil }
func (*COSORCDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("COS ORC SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*COSORCDriver) TestConnectionContext(context.Context) error {
	return errors.New("COS ORC connectivity must be checked with PingContext on the database opened from its configuration")
}
func (*COSORCDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}

func (*COSORCDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "cos-orc://"
	}
	return (&url.URL{
		Scheme: "cos-orc", Host: strings.TrimSpace(config.Database),
		Path: "/" + COSORCObjectKey(config),
	}).String()
}

// NormalizeCOSORCConfig validates one configured Tencent COS ORC object.
func NormalizeCOSORCConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("cos-orc data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" || bucket == "." || bucket == ".." || strings.ContainsAny(bucket, "/\\\\") {
		return nil, errors.New("cos-orc bucket must be one path-safe name in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("cos-orc SecretId and SecretKey are required")
	}
	if strings.TrimSpace(configString(config.AdditionalProps, "region")) == "" {
		return nil, errors.New("cos-orc region is required in additionalProps")
	}
	if !validCOSORCObjectKey(COSORCObjectKey(config)) {
		return nil, errors.New("cos-orc object_key must be a safe relative .orc object key")
	}
	if endpoint := configString(config.AdditionalProps, "endpoint_url"); endpoint != "" {
		if err := validateCOSEndpoint(endpoint); err != nil {
			return nil, err
		}
	}
	if value := strings.ToLower(configString(config.AdditionalProps, "https")); value != "" && value != "true" && value != "false" && value != "1" && value != "0" {
		return nil, errors.New("cos-orc https must be true or false")
	}
	if _, err := cosORCMaxFileBytes(config); err != nil {
		return nil, err
	}
	if _, err := cosORCMaxRows(config); err != nil {
		return nil, err
	}
	normalized := *config
	normalized.AdditionalProps = make(map[string]interface{}, len(config.AdditionalProps))
	for key, value := range config.AdditionalProps {
		normalized.AdditionalProps[key] = value
	}
	return &normalized, nil
}

func ValidateCOSORCConfig(config *model.DataSourceConfig) error {
	_, err := NormalizeCOSORCConfig(config)
	return err
}

func cosORCMaxFileBytes(config *model.DataSourceConfig) (int64, error) {
	if config == nil || config.AdditionalProps["max_file_bytes"] == nil {
		return defaultCOSORCMaxFileBytes, nil
	}
	value, err := parseCOSCSVInt64(config.AdditionalProps["max_file_bytes"])
	if err != nil {
		return 0, errors.New("cos-orc max_file_bytes must be an integer")
	}
	if value <= 0 || value > maxCOSORCMaxFileBytes {
		return 0, fmt.Errorf("cos-orc max_file_bytes must be between 1 and %d", maxCOSORCMaxFileBytes)
	}
	return value, nil
}

func cosORCMaxRows(config *model.DataSourceConfig) (int, error) {
	if config == nil || config.AdditionalProps["max_rows"] == nil {
		return defaultCOSORCMaxRows, nil
	}
	value, err := parseCOSCSVInt64(config.AdditionalProps["max_rows"])
	if err != nil || value <= 0 || value > maxCOSORCMaxRows {
		return 0, fmt.Errorf("cos-orc max_rows must be between 1 and %d", maxCOSORCMaxRows)
	}
	return int(value), nil
}

func validCOSORCObjectKey(key string) bool {
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

func COSORCObjectKey(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if key := configString(config.AdditionalProps, "object_key"); key != "" {
		return key
	}
	return strings.TrimSpace(config.Host)
}

func COSORCTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "cos-orc")
}
