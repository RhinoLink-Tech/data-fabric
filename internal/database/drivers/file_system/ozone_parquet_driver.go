package file_system

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
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	defaultOzoneParquetMaxFileBytes int64 = 16 << 20
	maxOzoneParquetMaxFileBytes     int64 = 64 << 20
	ozoneParquetMaxRows                   = 100000
)

// OzoneParquetDriver reads one Parquet object through the Apache Ozone S3 Gateway.
type OzoneParquetDriver struct{}

func (*OzoneParquetDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("Ozone Parquet requires a configured S3 Gateway read-only SQL adapter")
}

func (d *OzoneParquetDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, endpoint, err := NormalizeOzoneParquetConfig(config)
	if err != nil {
		return nil, err
	}
	region := configPropString(normalized, "region")
	if region == "" {
		region = "us-east-1"
	}
	objectKey := OzoneParquetObjectKey(normalized)
	tableName, err := OzoneParquetTableName(normalized)
	if err != nil {
		return nil, err
	}
	client, err := object_storage.NewS3Client(ctx, &object_storage.S3Config{
		Region: region, Bucket: strings.TrimSpace(normalized.Database),
		AccessKey: normalized.Username, SecretKey: normalized.Password,
		SessionToken: configPropString(normalized, "session_token"),
		EndpointURL:  endpoint, ForcePathStyle: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create Ozone S3 Gateway client: %w", err)
	}
	maxBytes := OzoneParquetMaxFileBytes(normalized)
	return object_storage.NewParquetSQLDatabase(object_storage.ParquetSQLDatabaseConfig{
		DriverName: "ozone-parquet", ObjectKey: objectKey, TableName: tableName,
		BatchSize: 1000, MaxFileBytes: maxBytes, MaxRows: ozoneParquetMaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetObjectMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect Ozone Parquet object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("Ozone Parquet object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetObjectLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read Ozone Parquet object: %w", err)
		}
		return data, nil
	}, nil)
}

func (*OzoneParquetDriver) GetDefaultPort() int         { return 9878 }
func (*OzoneParquetDriver) GetDatabaseTypeName() string { return "ozone-parquet" }
func (*OzoneParquetDriver) GetDriverName() string       { return "ozone-parquet" }
func (*OzoneParquetDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*OzoneParquetDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*OzoneParquetDriver) ConfigureAuth(interface{}) error { return nil }
func (*OzoneParquetDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("Ozone Parquet SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*OzoneParquetDriver) TestConnectionContext(context.Context) error {
	return errors.New("Ozone Parquet connectivity must be checked with PingContext on the database opened from its S3 Gateway configuration")
}

func (*OzoneParquetDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	if batchSize <= 0 {
		return "", errors.New("batch size must be positive")
	}
	query = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	if query == "" {
		return "", errors.New("query is required")
	}
	if offset < 0 {
		offset = 0
	}
	return fmt.Sprintf("SELECT * FROM (%s) AS __data_fabric_page LIMIT %d OFFSET %d", query, batchSize, offset), nil
}

func (d *OzoneParquetDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "ozone-s3" || parsed.Host == "" || !path.IsAbs(parsed.Path) {
		return errors.New("invalid Ozone Parquet URI; expected ozone-s3://gateway/bucket/path/file.parquet")
	}
	parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || !validOzoneParquetKey(strings.Join(parts[1:], "/")) {
		return errors.New("invalid Ozone Parquet URI; expected ozone-s3://gateway/bucket/path/file.parquet")
	}
	return nil
}

func (d *OzoneParquetDriver) BuildDSN(config *model.DataSourceConfig) string {
	normalized, endpoint, err := NormalizeOzoneParquetConfig(config)
	if err != nil {
		return "ozone-s3://"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "ozone-s3://"
	}
	return (&url.URL{Scheme: "ozone-s3", Host: parsed.Host, Path: "/" + strings.TrimSpace(normalized.Database) + "/" + OzoneParquetObjectKey(normalized)}).String()
}

// NormalizeOzoneParquetConfig validates the S3 Gateway origin and copies the
// data-source config without mutating its AdditionalProps map.
func NormalizeOzoneParquetConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, string, error) {
	if config == nil {
		return nil, "", errors.New("Ozone Parquet data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" {
		return nil, "", errors.New("Ozone S3 bucket is required in database")
	}
	if strings.ContainsAny(bucket, "/\\") || bucket == "." || bucket == ".." {
		return nil, "", errors.New("Ozone S3 bucket must be one path-safe bucket name")
	}
	key := OzoneParquetObjectKey(config)
	if !validOzoneParquetKey(key) {
		return nil, "", errors.New("Ozone Parquet object_key must be a safe relative .parquet key")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, "", errors.New("Ozone S3 access key and secret key are required")
	}
	if _, present := configProp(config, "max_file_bytes"); present {
		if _, err := configPropInt64(config, "max_file_bytes"); err != nil {
			return nil, "", errors.New("Ozone Parquet max_file_bytes must be an integer")
		}
	}
	maxBytes := OzoneParquetMaxFileBytes(config)
	if maxBytes <= 0 || maxBytes > maxOzoneParquetMaxFileBytes {
		return nil, "", fmt.Errorf("Ozone Parquet max_file_bytes must be between 1 and %d", maxOzoneParquetMaxFileBytes)
	}

	rawEndpoint := configPropString(config, "endpoint_url")
	if rawEndpoint == "" {
		return nil, "", errors.New("Ozone S3 Gateway endpoint_url is required in additionalProps")
	}
	endpoint, err := url.Parse(rawEndpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || (endpoint.Path != "" && endpoint.Path != "/") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, "", errors.New("Ozone S3 Gateway endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	port := config.Port
	if port < 0 {
		return nil, "", errors.New("Ozone S3 Gateway port must be between 1 and 65535")
	}
	if port == 0 {
		port = 9878
	}
	if port > 65535 {
		return nil, "", errors.New("Ozone S3 Gateway port must be between 1 and 65535")
	}
	if endpoint.Port() == "" {
		endpoint.Host = net.JoinHostPort(endpoint.Hostname(), strconv.Itoa(port))
	} else {
		parsedPort, parseErr := strconv.Atoi(endpoint.Port())
		if parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return nil, "", errors.New("Ozone S3 Gateway endpoint port must be between 1 and 65535")
		}
	}
	endpoint.Path = ""
	endpoint.RawPath = ""

	props := make(map[string]interface{}, len(config.AdditionalProps)+3)
	for name, value := range config.AdditionalProps {
		props[name] = value
	}
	props["endpoint_url"] = endpoint.String()
	if strings.TrimSpace(fmt.Sprint(props["region"])) == "" || props["region"] == nil {
		props["region"] = "us-east-1"
	}
	props["force_path_style"] = true
	normalized := *config
	normalized.AdditionalProps = props
	return &normalized, endpoint.String(), nil
}

func ValidateOzoneParquetConfig(config *model.DataSourceConfig) error {
	_, _, err := NormalizeOzoneParquetConfig(config)
	return err
}

func OzoneParquetObjectKey(config *model.DataSourceConfig) string {
	if value := configPropString(config, "object_key"); value != "" {
		return value
	}
	if config == nil {
		return ""
	}
	return strings.TrimSpace(config.Host)
}

func OzoneParquetTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("Ozone Parquet data source config is required")
	}
	if name := configPropString(config, "table_name"); name != "" {
		return name, nil
	}
	key := OzoneParquetObjectKey(config)
	if key == "" {
		return "", errors.New("Ozone Parquet object_key is required in additionalProps or host")
	}
	name := strings.TrimSuffix(path.Base(key), path.Ext(key))
	if name == "" || name == "." {
		return "", errors.New("Ozone Parquet table_name could not be inferred; set additionalProps.table_name")
	}
	return name, nil
}

func OzoneParquetMaxFileBytes(config *model.DataSourceConfig) int64 {
	if _, present := configProp(config, "max_file_bytes"); present {
		parsed, err := configPropInt64(config, "max_file_bytes")
		if err != nil {
			return 0
		}
		return parsed
	}
	return defaultOzoneParquetMaxFileBytes
}

func validOzoneParquetKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || path.IsAbs(key) || strings.Contains(key, "\\") || !strings.EqualFold(path.Ext(key), ".parquet") {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// Legacy object methods now fail explicitly instead of returning success-shaped placeholders.
func (*OzoneParquetDriver) ListParquetFiles(context.Context, string) ([]OzoneFileInfo, error) {
	return nil, errors.New("Ozone Parquet file listing requires a configured S3 Gateway data source")
}
func (*OzoneParquetDriver) QueryParquetFile(context.Context, string) (*OzoneParquetResult, error) {
	return nil, errors.New("use OpenWithConfig and the read-only SQL adapter for Ozone Parquet queries")
}
func (*OzoneParquetDriver) GetCapacity(context.Context) (*OzoneCapacity, error) {
	return nil, errors.New("Ozone cluster capacity is not exposed by the S3 Gateway adapter")
}

type OzoneParquetResult struct {
	Rows      []map[string]interface{}
	NumRows   int64
	BytesRead int64
	Key       string
}
