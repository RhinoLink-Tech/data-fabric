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
	defaultAzureBlobJSONMaxFileBytes int64 = 16 << 20
	maxAzureBlobJSONMaxFileBytes     int64 = 64 << 20
	defaultAzureBlobJSONMaxRecords         = 100000
	maxAzureBlobJSONMaxRecords             = 100000
)

// AzureBlobJSONDriver exposes one bounded Azure Blob JSON document as a read-only SQL table.
type AzureBlobJSONDriver struct{}

var _ drivers.Driver = (*AzureBlobJSONDriver)(nil)

func (*AzureBlobJSONDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("Azure Blob JSON requires a configured read-only SQL adapter")
}

func (*AzureBlobJSONDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	normalized, err := NormalizeAzureBlobJSONConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := AzureBlobJSONObjectKey(normalized)
	tableName, err := AzureBlobJSONTableName(normalized)
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
		return nil, fmt.Errorf("create Azure Blob JSON client: %w", err)
	}
	maxBytes, _ := azureBlobJSONMaxFileBytes(normalized)
	maxRecords, _ := azureBlobJSONMaxRecords(normalized)
	return NewJSONSQLDatabase(JSONSQLDatabaseConfig{
		DriverName: "azure-blob-json", ObjectKey: objectKey, TableName: tableName,
		MaxFileBytes: maxBytes, MaxRecords: maxRecords,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetBlobMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect Azure Blob JSON object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("Azure Blob JSON object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string, limit int64) ([]byte, error) {
		data, err := client.GetBlobLimited(ctx, key, limit)
		if err != nil {
			return nil, fmt.Errorf("read Azure Blob JSON object: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, nil)
}

func (*AzureBlobJSONDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "azure-json" || strings.TrimSpace(parsed.Host) == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid Azure Blob JSON URI; expected azure-json://container/path/file.json")
	}
	if key, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/")); err != nil || !validAzureBlobJSONObjectKey(key) {
		return errors.New("invalid Azure Blob JSON URI; expected a safe relative .json, .jsonl, or .ndjson object key")
	}
	return nil
}

func (*AzureBlobJSONDriver) GetDefaultPort() int         { return 443 }
func (*AzureBlobJSONDriver) GetDatabaseTypeName() string { return "azure_blob_json" }
func (*AzureBlobJSONDriver) GetDriverName() string       { return "azure-blob-json" }
func (*AzureBlobJSONDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*AzureBlobJSONDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*AzureBlobJSONDriver) ConfigureAuth(interface{}) error { return nil }
func (*AzureBlobJSONDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("Azure Blob JSON SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*AzureBlobJSONDriver) TestConnectionContext(context.Context) error {
	return errors.New("Azure Blob JSON connectivity must be checked with PingContext on the database opened from its configuration")
}
func (*AzureBlobJSONDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return ApplyJSONBatchPagination(query, batchSize, offset)
}

func (*AzureBlobJSONDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "azure-json://"
	}
	return (&url.URL{
		Scheme: "azure-json", Host: strings.TrimSpace(config.Database),
		Path: "/" + AzureBlobJSONObjectKey(config),
	}).String()
}

// NormalizeAzureBlobJSONConfig validates a single-object Azure Blob JSON profile.
func NormalizeAzureBlobJSONConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("azure-blob-json data source config is required")
	}
	container := strings.TrimSpace(config.Database)
	if container == "" || container == "." || container == ".." || strings.ContainsAny(container, "/\\") {
		return nil, errors.New("azure-blob-json container must be one path-safe name in database")
	}
	if strings.TrimSpace(config.Username) == "" {
		return nil, errors.New("azure-blob-json account name is required in username")
	}
	sasToken := configString(config.AdditionalProps, "sas_token")
	if sasToken == "" && strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("azure-blob-json account key or sas_token is required")
	}
	if sasToken != "" {
		parsed, err := url.ParseQuery(strings.TrimPrefix(sasToken, "?"))
		if err != nil || len(parsed) == 0 {
			return nil, errors.New("azure-blob-json sas_token must be a valid query string")
		}
	}
	if !validAzureBlobJSONObjectKey(AzureBlobJSONObjectKey(config)) {
		return nil, errors.New("azure-blob-json object_key must be a safe relative .json, .jsonl, or .ndjson object key")
	}
	if endpoint := configString(config.AdditionalProps, "endpoint_url"); endpoint != "" {
		if err := validateAzureBlobEndpoint(endpoint); err != nil {
			return nil, err
		}
	}
	if _, err := azureBlobJSONMaxFileBytes(config); err != nil {
		return nil, err
	}
	if _, err := azureBlobJSONMaxRecords(config); err != nil {
		return nil, err
	}
	normalized := *config
	normalized.AdditionalProps = make(map[string]interface{}, len(config.AdditionalProps))
	for key, value := range config.AdditionalProps {
		normalized.AdditionalProps[key] = value
	}
	return &normalized, nil
}

func ValidateAzureBlobJSONConfig(config *model.DataSourceConfig) error {
	_, err := NormalizeAzureBlobJSONConfig(config)
	return err
}

func azureBlobJSONMaxFileBytes(config *model.DataSourceConfig) (int64, error) {
	if config == nil || config.AdditionalProps["max_file_bytes"] == nil {
		return defaultAzureBlobJSONMaxFileBytes, nil
	}
	value, err := parseAzureBlobConfigInt64(config.AdditionalProps["max_file_bytes"])
	if err != nil {
		return 0, errors.New("azure-blob-json max_file_bytes must be an integer")
	}
	if value <= 0 || value > maxAzureBlobJSONMaxFileBytes {
		return 0, fmt.Errorf("azure-blob-json max_file_bytes must be between 1 and %d", maxAzureBlobJSONMaxFileBytes)
	}
	return value, nil
}

func azureBlobJSONMaxRecords(config *model.DataSourceConfig) (int, error) {
	if config == nil || config.AdditionalProps["max_records"] == nil {
		return defaultAzureBlobJSONMaxRecords, nil
	}
	value, err := parseAzureBlobConfigInt64(config.AdditionalProps["max_records"])
	if err != nil || value <= 0 || value > maxAzureBlobJSONMaxRecords {
		return 0, fmt.Errorf("azure-blob-json max_records must be between 1 and %d", maxAzureBlobJSONMaxRecords)
	}
	return int(value), nil
}

func validAzureBlobJSONObjectKey(key string) bool {
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

func AzureBlobJSONObjectKey(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if key := configString(config.AdditionalProps, "object_key"); key != "" {
		return key
	}
	return strings.TrimSpace(config.Host)
}

func AzureBlobJSONTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "azure-blob-json")
}
