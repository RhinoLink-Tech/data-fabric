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

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	defaultCOSDeltaTableBytes int64 = 16 << 20
	maxCOSDeltaTableBytes     int64 = 64 << 20
	maxCOSDeltaLogFileBytes   int64 = 4 << 20
	maxCOSDeltaLogBytes       int64 = 16 << 20
	cosDeltaMaxRows                 = 100_000
)

// COSDeltaDriver reads one bounded Delta Lake snapshot stored in Tencent COS.
type COSDeltaDriver struct {
	client    *COSClient
	tablePath string
	tableName string
	batchSize int
	maxBytes  int64
}

func NewCOSDeltaDriver(ctx context.Context, config *COSConfig) (*COSDeltaDriver, error) {
	client, err := NewCOSClient(ctx, config)
	if err != nil {
		return nil, err
	}
	return &COSDeltaDriver{client: client, batchSize: 1000, maxBytes: defaultCOSDeltaTableBytes}, nil
}

func (*COSDeltaDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("COS Delta requires a configured read-only SQL adapter")
}

func (*COSDeltaDriver) GetDefaultPort() int         { return 443 }
func (*COSDeltaDriver) GetDatabaseTypeName() string { return "cos-delta" }
func (*COSDeltaDriver) GetDriverName() string       { return "cos-delta" }
func (*COSDeltaDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*COSDeltaDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL: true, SupportsSchemaDiscovery: true,
	}
}
func (*COSDeltaDriver) ConfigureAuth(interface{}) error { return nil }
func (*COSDeltaDriver) TestConnection(*sql.DB) error {
	return errors.New("use the configured COS Delta SQL adapter for connectivity checks")
}
func (d *COSDeltaDriver) TestConnectionContext(ctx context.Context) error {
	if d == nil || d.client == nil {
		return errors.New("COS Delta driver is not initialized")
	}
	_, err := d.client.ListObjects(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("failed to test Tencent COS connection: %w", err)
	}
	return nil
}

func (*COSDeltaDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "cos-delta" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid COS Delta URI; expected cos-delta://bucket/table/path")
	}
	tablePath := strings.TrimPrefix(parsed.Path, "/")
	if !validCOSDeltaTablePath(tablePath) {
		return errors.New("invalid COS Delta table path")
	}
	if _, err := cleanObjectDeltaTablePath(tablePath); err != nil {
		return fmt.Errorf("invalid COS Delta table path: %w", err)
	}
	return nil
}

func (*COSDeltaDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "cos-delta://"
	}
	return (&url.URL{
		Scheme: "cos-delta",
		Host:   strings.TrimSpace(config.Database),
		Path:   "/" + cosDeltaTablePath(config),
	}).String()
}

func (d *COSDeltaDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}

// OpenWithConfig exposes the latest supported Delta snapshot as a read-only SQL table.
func (d *COSDeltaDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateCOSDeltaConfig(config); err != nil {
		return nil, err
	}
	endpoint := configString(config.AdditionalProps, "endpoint_url")
	configured, err := NewCOSDeltaDriver(ctx, &COSConfig{
		SecretID: config.Username, SecretKey: config.Password,
		Region:      configString(config.AdditionalProps, "region"),
		BucketName:  strings.TrimSpace(config.Database),
		HTTPS:       configBool(config.AdditionalProps, "https", true),
		EndpointURL: endpoint,
	})
	if err != nil {
		return nil, fmt.Errorf("create Tencent COS client: %w", err)
	}
	configured.tablePath = cosDeltaTablePath(config)
	configured.batchSize = configInt(config.AdditionalProps, "batch_size", 1000)
	configured.maxBytes = cosDeltaMaxBytes(config)
	configured.tableName, err = COSDeltaTableName(config)
	if err != nil {
		return nil, err
	}
	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "cos-delta",
		objectKey:  configured.tablePath,
		tableName:  configured.tableName,
		ping: func(ctx context.Context, _ string) error {
			_, err := configured.resolveSnapshot(ctx, configured.tablePath)
			return err
		},
		getSchema: func(ctx context.Context, _ string) (*objectSQLSchema, error) {
			snapshot, err := configured.resolveSnapshot(ctx, configured.tablePath)
			if err != nil {
				return nil, err
			}
			schema, err := readObjectDeltaSchema(ctx, snapshot, configured.batchSize, configured.getDataObject)
			if err != nil {
				return nil, err
			}
			return objectSQLSchemaFromParquet(schema), nil
		},
		readRows: func(ctx context.Context, _ string) (*objectSQLSchema, []map[string]interface{}, error) {
			snapshot, err := configured.resolveSnapshot(ctx, configured.tablePath)
			if err != nil {
				return nil, nil, err
			}
			schema, rows, err := readObjectDeltaRows(ctx, snapshot, configured.batchSize, configured.maxBytes, configured.getDataObject)
			if err != nil {
				return nil, nil, err
			}
			if len(rows) > cosDeltaMaxRows {
				return nil, nil, fmt.Errorf("COS Delta snapshot exceeds the %d-row adapter limit", cosDeltaMaxRows)
			}
			return objectSQLSchemaFromParquet(schema), rows, nil
		},
	}), nil
}

func (d *COSDeltaDriver) resolveSnapshot(ctx context.Context, tablePath string) (*objectDeltaSnapshot, error) {
	if d == nil || d.client == nil {
		return nil, errors.New("COS Delta driver is not initialized")
	}
	logSizes := make(map[string]int64)
	var declaredLogBytes int64
	var readLogBytes int64
	return loadObjectDeltaSnapshot(ctx, tablePath,
		func(ctx context.Context, prefix string, max int) ([]deltaListedObject, error) {
			objects, err := d.client.ListObjects(ctx, prefix, max)
			if err != nil {
				return nil, err
			}
			result := make([]deltaListedObject, len(objects))
			for i, object := range objects {
				if object.Size < 0 || object.Size > maxCOSDeltaLogFileBytes {
					return nil, fmt.Errorf("Delta log object %q exceeds the %d-byte limit", object.Key, maxCOSDeltaLogFileBytes)
				}
				declaredLogBytes += object.Size
				if declaredLogBytes > maxCOSDeltaLogBytes {
					return nil, fmt.Errorf("Delta transaction logs exceed the %d-byte limit", maxCOSDeltaLogBytes)
				}
				logSizes[object.Key] = object.Size
				result[i] = deltaListedObject{key: object.Key, size: object.Size}
			}
			return result, nil
		},
		func(ctx context.Context, key string) ([]byte, error) {
			if _, isLog := logSizes[key]; isLog {
				data, err := d.client.GetObjectLimited(ctx, key, maxCOSDeltaLogFileBytes)
				if err != nil {
					return nil, err
				}
				readLogBytes += int64(len(data))
				if readLogBytes > maxCOSDeltaLogBytes {
					return nil, fmt.Errorf("Delta transaction logs exceed the %d-byte limit", maxCOSDeltaLogBytes)
				}
				return data, nil
			}
			return d.getDataObject(ctx, key)
		},
	)
}

func (d *COSDeltaDriver) getDataObject(ctx context.Context, key string) ([]byte, error) {
	if d == nil || d.client == nil || d.maxBytes <= 0 {
		return nil, errors.New("COS Delta driver is not initialized with a valid byte limit")
	}
	return d.client.GetObjectLimited(ctx, key, d.maxBytes)
}

func ValidateCOSDeltaConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return errors.New("cos_delta data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" || strings.ContainsAny(bucket, "/\\") || bucket == "." || bucket == ".." {
		return errors.New("cos_delta bucket is required in database and must be one path-safe name")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return errors.New("cos_delta secret ID and secret key are required")
	}
	if configString(config.AdditionalProps, "region") == "" {
		return errors.New("cos_delta region is required in additionalProps.region")
	}
	if !validCOSDeltaTablePath(cosDeltaTablePath(config)) {
		return errors.New("cos_delta table_path must be a safe non-empty relative object-store path")
	}
	if _, err := cleanObjectDeltaTablePath(cosDeltaTablePath(config)); err != nil {
		return fmt.Errorf("cos_delta table_path must be a safe non-empty relative object-store path: %w", err)
	}
	if _, err := COSDeltaTableName(config); err != nil {
		return err
	}
	if endpoint := configString(config.AdditionalProps, "endpoint_url"); endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			(parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("cos_delta endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
		}
	}
	if value, ok := config.AdditionalProps["max_table_bytes"]; ok {
		limit, err := parseCOSDeltaInt64(value)
		if err != nil || limit < 1 || limit > maxCOSDeltaTableBytes {
			return fmt.Errorf("cos_delta max_table_bytes must be between 1 and %d", maxCOSDeltaTableBytes)
		}
	}
	if value, ok := config.AdditionalProps["batch_size"]; ok {
		batchSize, err := parseCOSDeltaInt(value)
		if err != nil || batchSize < 1 || batchSize > 10_000 {
			return errors.New("cos_delta batch_size must be between 1 and 10000")
		}
	}
	return nil
}

func validCOSDeltaTablePath(value string) bool {
	if strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	value = strings.TrimRight(value, "/")
	if value == "" {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func cosDeltaTablePath(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if value := configString(config.AdditionalProps, "table_path"); value != "" {
		return value
	}
	return strings.TrimSpace(config.Host)
}

func cosDeltaMaxBytes(config *model.DataSourceConfig) int64 {
	if config != nil {
		if value, ok := config.AdditionalProps["max_table_bytes"]; ok {
			parsed, err := parseCOSDeltaInt64(value)
			if err == nil {
				return parsed
			}
		}
	}
	return defaultCOSDeltaTableBytes
}

func COSDeltaTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("cos_delta data source config is required")
	}
	if name := configString(config.AdditionalProps, "table_name"); name != "" {
		return name, nil
	}
	tablePath := cosDeltaTablePath(config)
	if tablePath == "" {
		return "", errors.New("cos_delta table_path is required in additionalProps or host")
	}
	return path.Base(strings.TrimRight(tablePath, "/")), nil
}

func parseCOSDeltaInt64(value interface{}) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case int64:
		return typed, nil
	case float64:
		if typed != float64(int64(typed)) {
			return 0, errors.New("value is not an integer")
		}
		return int64(typed), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unsupported integer type %T", value)
	}
}

func parseCOSDeltaInt(value interface{}) (int, error) {
	parsed, err := parseCOSDeltaInt64(value)
	if err != nil || int64(int(parsed)) != parsed {
		return 0, errors.New("value is not an integer")
	}
	return int(parsed), nil
}
