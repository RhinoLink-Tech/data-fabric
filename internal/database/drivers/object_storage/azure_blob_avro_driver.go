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
	defaultAzureBlobAvroMaxFileBytes int64 = 16 << 20
	maxAzureBlobAvroMaxFileBytes     int64 = 64 << 20
	defaultAzureBlobAvroMaxRows            = 100000
	maxAzureBlobAvroMaxRows                = 100000
)

// AzureBlobAvroDriver exposes one bounded Azure Blob Avro OCF as a read-only SQL table.
type AzureBlobAvroDriver struct{}

var _ drivers.Driver = (*AzureBlobAvroDriver)(nil)

func (*AzureBlobAvroDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("Azure Blob Avro requires a configured read-only SQL adapter")
}

func (*AzureBlobAvroDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	normalized, err := NormalizeAzureBlobAvroConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := AzureBlobAvroObjectKey(normalized)
	tableName, err := AzureBlobAvroTableName(normalized)
	if err != nil {
		return nil, err
	}
	client, err := NewAzureBlobClient(ctx, &AzureBlobConfig{
		AccountName: strings.TrimSpace(normalized.Username), AccountKey: normalized.Password,
		SASToken:      configString(normalized.AdditionalProps, "sas_token"),
		ContainerName: strings.TrimSpace(normalized.Database),
		Endpoint:      configString(normalized.AdditionalProps, "endpoint_url"),
	})
	if err != nil {
		return nil, fmt.Errorf("create Azure Blob Avro client: %w", err)
	}
	maxBytes, _ := azureBlobAvroMaxFileBytes(normalized)
	maxRows, _ := azureBlobAvroMaxRows(normalized)
	return NewAvroSQLDatabase(AvroSQLDatabaseConfig{
		DriverName: "azure-blob-avro", ObjectKey: objectKey, TableName: tableName,
		BatchSize: 1000, MaxFileBytes: maxBytes, MaxRows: maxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetBlobMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect Azure Blob Avro object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("Azure Blob Avro object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetBlobLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read Azure Blob Avro object: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, nil)
}

func (*AzureBlobAvroDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "azure-avro" || strings.TrimSpace(parsed.Host) == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid Azure Blob Avro URI; expected azure-avro://container/path/file.avro")
	}
	key, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || !validAzureBlobAvroObjectKey(key) {
		return errors.New("invalid Azure Blob Avro URI; expected a safe relative .avro object key")
	}
	return nil
}

func (*AzureBlobAvroDriver) GetDefaultPort() int         { return 443 }
func (*AzureBlobAvroDriver) GetDatabaseTypeName() string { return "azure_blob_avro" }
func (*AzureBlobAvroDriver) GetDriverName() string       { return "azure-blob-avro" }
func (*AzureBlobAvroDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*AzureBlobAvroDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*AzureBlobAvroDriver) ConfigureAuth(interface{}) error { return nil }
func (*AzureBlobAvroDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("Azure Blob Avro SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*AzureBlobAvroDriver) TestConnectionContext(context.Context) error {
	return errors.New("Azure Blob Avro connectivity must be checked with PingContext on the database opened from its configuration")
}
func (*AzureBlobAvroDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}

func (*AzureBlobAvroDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "azure-avro://"
	}
	return (&url.URL{
		Scheme: "azure-avro", Host: strings.TrimSpace(config.Database),
		Path: "/" + AzureBlobAvroObjectKey(config),
	}).String()
}

// NormalizeAzureBlobAvroConfig validates a single-object Azure Blob Avro profile.
func NormalizeAzureBlobAvroConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("azure-blob-avro data source config is required")
	}
	container := strings.TrimSpace(config.Database)
	if container == "" || container == "." || container == ".." || strings.ContainsAny(container, "/\\") {
		return nil, errors.New("azure-blob-avro container must be one path-safe name in database")
	}
	if strings.TrimSpace(config.Username) == "" {
		return nil, errors.New("azure-blob-avro account name is required in username")
	}
	sasToken := configString(config.AdditionalProps, "sas_token")
	if sasToken == "" && strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("azure-blob-avro account key or sas_token is required")
	}
	if sasToken != "" {
		parsed, err := url.ParseQuery(strings.TrimPrefix(sasToken, "?"))
		if err != nil || len(parsed) == 0 {
			return nil, errors.New("azure-blob-avro sas_token must be a valid query string")
		}
	}
	if !validAzureBlobAvroObjectKey(AzureBlobAvroObjectKey(config)) {
		return nil, errors.New("azure-blob-avro object_key must be a safe relative .avro object key")
	}
	if endpoint := configString(config.AdditionalProps, "endpoint_url"); endpoint != "" {
		if err := validateAzureBlobEndpoint(endpoint); err != nil {
			return nil, err
		}
	}
	if _, err := azureBlobAvroMaxFileBytes(config); err != nil {
		return nil, err
	}
	if _, err := azureBlobAvroMaxRows(config); err != nil {
		return nil, err
	}
	normalized := *config
	normalized.AdditionalProps = make(map[string]interface{}, len(config.AdditionalProps))
	for key, value := range config.AdditionalProps {
		normalized.AdditionalProps[key] = value
	}
	return &normalized, nil
}

func ValidateAzureBlobAvroConfig(config *model.DataSourceConfig) error {
	_, err := NormalizeAzureBlobAvroConfig(config)
	return err
}

func azureBlobAvroMaxFileBytes(config *model.DataSourceConfig) (int64, error) {
	if config == nil || config.AdditionalProps["max_file_bytes"] == nil {
		return defaultAzureBlobAvroMaxFileBytes, nil
	}
	value, err := parseAzureBlobConfigInt64(config.AdditionalProps["max_file_bytes"])
	if err != nil {
		return 0, errors.New("azure-blob-avro max_file_bytes must be an integer")
	}
	if value <= 0 || value > maxAzureBlobAvroMaxFileBytes {
		return 0, fmt.Errorf("azure-blob-avro max_file_bytes must be between 1 and %d", maxAzureBlobAvroMaxFileBytes)
	}
	return value, nil
}

func azureBlobAvroMaxRows(config *model.DataSourceConfig) (int, error) {
	if config == nil || config.AdditionalProps["max_rows"] == nil {
		return defaultAzureBlobAvroMaxRows, nil
	}
	value, err := parseAzureBlobConfigInt64(config.AdditionalProps["max_rows"])
	if err != nil || value <= 0 || value > maxAzureBlobAvroMaxRows {
		return 0, fmt.Errorf("azure-blob-avro max_rows must be between 1 and %d", maxAzureBlobAvroMaxRows)
	}
	return int(value), nil
}

func validAzureBlobAvroObjectKey(key string) bool {
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

func AzureBlobAvroObjectKey(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if key := configString(config.AdditionalProps, "object_key"); key != "" {
		return key
	}
	return strings.TrimSpace(config.Host)
}

func AzureBlobAvroTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "azure-blob-avro")
}
