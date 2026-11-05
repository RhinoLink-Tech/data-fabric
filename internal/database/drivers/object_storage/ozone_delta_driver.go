package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	defaultOzoneDeltaTableBytes int64 = 16 << 20
	maxOzoneDeltaTableBytes     int64 = 64 << 20
	maxOzoneDeltaLogFileBytes   int64 = 4 << 20
	maxOzoneDeltaLogBytes       int64 = 16 << 20
	ozoneDeltaMaxRows                 = 100_000
)

// OzoneDeltaDriver reads one bounded Delta Lake snapshot through the Ozone S3 Gateway.
type OzoneDeltaDriver struct {
	client    *S3Client
	tablePath string
	tableName string
	batchSize int
	maxBytes  int64
}

func (d *OzoneDeltaDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("Ozone Delta requires a configured read-only S3 Gateway SQL adapter")
}

func (*OzoneDeltaDriver) GetDefaultPort() int         { return 9878 }
func (*OzoneDeltaDriver) GetDatabaseTypeName() string { return "ozone-delta" }
func (*OzoneDeltaDriver) GetDriverName() string       { return "ozone-delta" }
func (*OzoneDeltaDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*OzoneDeltaDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*OzoneDeltaDriver) ConfigureAuth(interface{}) error { return nil }
func (*OzoneDeltaDriver) TestConnection(*sql.DB) error {
	return errors.New("use the configured Ozone Delta SQL adapter for connectivity checks")
}
func (*OzoneDeltaDriver) TestConnectionContext(context.Context) error {
	return errors.New("Ozone Delta connectivity requires the configured S3 Gateway adapter")
}

func (d *OzoneDeltaDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "ozone-s3" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid Ozone Delta URI; expected ozone-s3://bucket/table/path")
	}
	tablePath := strings.Trim(parsed.Path, "/")
	if !validOzoneDeltaTablePath(tablePath) {
		return errors.New("invalid Ozone Delta URI; expected ozone-s3://bucket/table/path")
	}
	return nil
}

func (d *OzoneDeltaDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "ozone-s3://"
	}
	tablePath := ozoneDeltaTablePath(config)
	return (&url.URL{Scheme: "ozone-s3", Host: strings.TrimSpace(config.Database), Path: "/" + tablePath}).String()
}

func (d *OzoneDeltaDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}

// OpenWithConfig exposes the configured latest Delta snapshot as a read-only SQL table.
func (d *OzoneDeltaDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateOzoneDeltaConfig(config); err != nil {
		return nil, err
	}
	endpoint, err := OzoneDeltaEndpointFromConfig(config)
	if err != nil {
		return nil, err
	}
	region := configString(config.AdditionalProps, "region")
	if region == "" {
		region = "us-east-1"
	}
	client, err := NewS3Client(ctx, &S3Config{
		Region: region, Bucket: strings.TrimSpace(config.Database),
		AccessKey: strings.TrimSpace(config.Username), SecretKey: config.Password,
		SessionToken: configString(config.AdditionalProps, "session_token"),
		EndpointURL:  endpoint, ForcePathStyle: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create Ozone S3 Gateway client: %w", err)
	}
	configured := &OzoneDeltaDriver{
		client: client, tablePath: ozoneDeltaTablePath(config), batchSize: configInt(config.AdditionalProps, "batch_size", 1000),
		maxBytes: ozoneDeltaMaxBytes(config),
	}
	if configured.batchSize <= 0 {
		configured.batchSize = 1000
	}
	if configured.batchSize > 10_000 {
		return nil, errors.New("ozone_delta batch_size cannot exceed 10000")
	}
	configured.tableName, err = OzoneDeltaTableName(config)
	if err != nil {
		return nil, err
	}
	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "ozone-delta", objectKey: configured.tablePath, tableName: configured.tableName,
		ping: func(ctx context.Context, key string) error {
			_, err := configured.resolveSnapshot(ctx, key)
			return err
		},
		getSchema: func(ctx context.Context, key string) (*objectSQLSchema, error) {
			snapshot, err := configured.resolveSnapshot(ctx, key)
			if err != nil {
				return nil, err
			}
			schema, err := readObjectDeltaSchema(ctx, snapshot, configured.batchSize, configured.getDataObject)
			if err != nil {
				return nil, err
			}
			return objectSQLSchemaFromParquet(schema), nil
		},
		readRows: func(ctx context.Context, key string) (*objectSQLSchema, []map[string]interface{}, error) {
			snapshot, err := configured.resolveSnapshot(ctx, key)
			if err != nil {
				return nil, nil, err
			}
			schema, rows, err := readObjectDeltaRows(ctx, snapshot, configured.batchSize, configured.maxBytes, configured.getDataObject)
			if err != nil {
				return nil, nil, err
			}
			if int64(len(rows)) > ozoneDeltaMaxRows {
				return nil, nil, fmt.Errorf("Ozone Delta snapshot exceeds the %d-row adapter limit", ozoneDeltaMaxRows)
			}
			return objectSQLSchemaFromParquet(schema), rows, nil
		},
	}), nil
}

func (d *OzoneDeltaDriver) resolveSnapshot(ctx context.Context, tablePath string) (*objectDeltaSnapshot, error) {
	if d == nil || d.client == nil {
		return nil, errors.New("Ozone Delta driver is not initialized")
	}
	logSizes := make(map[string]int64)
	var declaredLogBytes int64
	var readLogBytes int64
	return loadObjectDeltaSnapshot(ctx, tablePath,
		func(ctx context.Context, prefix string, max int) ([]deltaListedObject, error) {
			objects, err := d.client.ListObjects(ctx, prefix, int32(max))
			if err != nil {
				return nil, err
			}
			result := make([]deltaListedObject, len(objects))
			for i, object := range objects {
				if object.Size < 0 || object.Size > maxOzoneDeltaLogFileBytes {
					return nil, fmt.Errorf("Delta log object %q exceeds the %d-byte limit", object.Key, maxOzoneDeltaLogFileBytes)
				}
				declaredLogBytes += object.Size
				if declaredLogBytes > maxOzoneDeltaLogBytes {
					return nil, fmt.Errorf("Delta transaction logs exceed the %d-byte limit", maxOzoneDeltaLogBytes)
				}
				logSizes[object.Key] = object.Size
				result[i] = deltaListedObject{key: object.Key, size: object.Size}
			}
			return result, nil
		}, func(ctx context.Context, key string) ([]byte, error) {
			if _, isLog := logSizes[key]; isLog {
				data, err := d.client.GetObjectLimited(ctx, key, maxOzoneDeltaLogFileBytes)
				if err != nil {
					return nil, err
				}
				readLogBytes += int64(len(data))
				if readLogBytes > maxOzoneDeltaLogBytes {
					return nil, fmt.Errorf("Delta transaction logs exceed the %d-byte limit", maxOzoneDeltaLogBytes)
				}
				return data, nil
			}
			return d.client.GetObjectLimited(ctx, key, d.maxBytes)
		},
	)
}

func (d *OzoneDeltaDriver) getDataObject(ctx context.Context, key string) ([]byte, error) {
	if d == nil || d.client == nil || d.maxBytes <= 0 {
		return nil, errors.New("Ozone Delta driver is not initialized with a valid byte limit")
	}
	return d.client.GetObjectLimited(ctx, key, d.maxBytes)
}

func ozoneDeltaTablePath(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if value := configString(config.AdditionalProps, "table_path"); value != "" {
		return value
	}
	return strings.TrimSpace(config.Host)
}

func validOzoneDeltaTablePath(value string) bool {
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

func ozoneDeltaMaxBytes(config *model.DataSourceConfig) int64 {
	if value, ok := config.AdditionalProps["max_table_bytes"]; ok {
		parsed, err := parseOzoneDeltaInt64(value)
		if err == nil {
			return parsed
		}
	}
	return defaultOzoneDeltaTableBytes
}

func OzoneDeltaTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("ozone_delta data source config is required")
	}
	if name := configString(config.AdditionalProps, "table_name"); name != "" {
		return name, nil
	}
	tablePath := ozoneDeltaTablePath(config)
	if tablePath == "" {
		return "", errors.New("ozone_delta table_path is required in additionalProps or host")
	}
	return path.Base(tablePath), nil
}

func OzoneDeltaEndpointFromConfig(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("ozone_delta data source config is required")
	}
	endpoint, err := url.Parse(configString(config.AdditionalProps, "endpoint_url"))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return "", errors.New("ozone_delta endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	port := config.Port
	if port == 0 {
		port = 9878
	}
	if port < 1 || port > 65535 {
		return "", errors.New("ozone_delta port must be between 1 and 65535")
	}
	if endpoint.Port() == "" {
		endpoint.Host = net.JoinHostPort(endpoint.Hostname(), strconv.Itoa(port))
	} else if parsed, parseErr := strconv.Atoi(endpoint.Port()); parseErr != nil || parsed < 1 || parsed > 65535 {
		return "", errors.New("ozone_delta endpoint port must be between 1 and 65535")
	}
	endpoint.Path = ""
	endpoint.RawPath = ""
	return endpoint.String(), nil
}

func ValidateOzoneDeltaConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return errors.New("ozone_delta data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" || strings.ContainsAny(bucket, "/\\") || bucket == "." || bucket == ".." {
		return errors.New("ozone_delta bucket is required in database and must be one path-safe name")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return errors.New("ozone_delta S3 access key and secret key are required")
	}
	if !validOzoneDeltaTablePath(ozoneDeltaTablePath(config)) {
		return errors.New("ozone_delta table_path must be a safe non-empty relative object-store path")
	}
	if _, err := OzoneDeltaTableName(config); err != nil {
		return err
	}
	if _, err := OzoneDeltaEndpointFromConfig(config); err != nil {
		return err
	}
	if value, ok := config.AdditionalProps["max_table_bytes"]; ok {
		limit, err := parseOzoneDeltaInt64(value)
		if err != nil || limit < 1 || limit > maxOzoneDeltaTableBytes {
			return fmt.Errorf("ozone_delta max_table_bytes must be between 1 and %d", maxOzoneDeltaTableBytes)
		}
	}
	if value, ok := config.AdditionalProps["batch_size"]; ok {
		batchSize, err := parseOzoneDeltaInt(value)
		if err != nil || batchSize < 1 || batchSize > 10_000 {
			return errors.New("ozone_delta batch_size must be between 1 and 10000")
		}
	}
	return nil
}

func parseOzoneDeltaInt64(value interface{}) (int64, error) {
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

func parseOzoneDeltaInt(value interface{}) (int, error) {
	parsed, err := parseOzoneDeltaInt64(value)
	if err != nil || int64(int(parsed)) != parsed {
		return 0, errors.New("value is not an integer")
	}
	return int(parsed), nil
}
