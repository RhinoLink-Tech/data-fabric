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
	defaultAzureBlobORCMaxFileBytes int64 = 16 << 20
	maxAzureBlobORCMaxFileBytes     int64 = 64 << 20
	defaultAzureBlobORCMaxRows            = 100000
	maxAzureBlobORCMaxRows                = 100000
)

// AzureBlobORCDriver exposes one bounded Azure Blob ORC object as a read-only SQL table.
type AzureBlobORCDriver struct{}

var _ drivers.Driver = (*AzureBlobORCDriver)(nil)

func (*AzureBlobORCDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("Azure Blob ORC requires a configured read-only SQL adapter")
}

func (*AzureBlobORCDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	normalized, err := NormalizeAzureBlobORCConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := AzureBlobORCObjectKey(normalized)
	tableName, err := AzureBlobORCTableName(normalized)
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
		return nil, fmt.Errorf("create Azure Blob ORC client: %w", err)
	}
	maxBytes, _ := azureBlobORCMaxFileBytes(normalized)
	maxRows, _ := azureBlobORCMaxRows(normalized)
	reader := NewORCReaderWithReadFunc(&ORCReaderConfig{BatchSize: 1000}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetBlobLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read Azure Blob ORC object: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if int64(len(data)) > maxBytes {
			return nil, fmt.Errorf("Azure Blob ORC object is %d bytes; configured maximum is %d bytes", len(data), maxBytes)
		}
		return data, nil
	})
	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "azure-blob-orc",
		objectKey:  objectKey,
		tableName:  tableName,
		ping: func(ctx context.Context, key string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			metadata, err := client.GetBlobMetadata(ctx, key)
			if err != nil {
				return fmt.Errorf("inspect Azure Blob ORC object: %w", err)
			}
			if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
				return fmt.Errorf("Azure Blob ORC object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
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
				return nil, nil, fmt.Errorf("Azure Blob ORC row limit exceeded: maximum %d", maxRows)
			}
			objectSchema, err := objectSQLSchemaFromORC(schema)
			if err != nil {
				return nil, nil, err
			}
			return objectSchema, rows, nil
		},
	}), nil
}

func (*AzureBlobORCDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "azure-orc" || strings.TrimSpace(parsed.Host) == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid Azure Blob ORC URI; expected azure-orc://container/path/file.orc")
	}
	key, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || !validAzureBlobORCObjectKey(key) {
		return errors.New("invalid Azure Blob ORC URI; expected a safe relative .orc object key")
	}
	return nil
}

func (*AzureBlobORCDriver) GetDefaultPort() int         { return 443 }
func (*AzureBlobORCDriver) GetDatabaseTypeName() string { return "azure_blob_orc" }
func (*AzureBlobORCDriver) GetDriverName() string       { return "azure-blob-orc" }
func (*AzureBlobORCDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*AzureBlobORCDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*AzureBlobORCDriver) ConfigureAuth(interface{}) error { return nil }
func (*AzureBlobORCDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("Azure Blob ORC SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*AzureBlobORCDriver) TestConnectionContext(context.Context) error {
	return errors.New("Azure Blob ORC connectivity must be checked with PingContext on the database opened from its configuration")
}
func (*AzureBlobORCDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}

func (*AzureBlobORCDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "azure-orc://"
	}
	return (&url.URL{
		Scheme: "azure-orc", Host: strings.TrimSpace(config.Database),
		Path: "/" + AzureBlobORCObjectKey(config),
	}).String()
}

// NormalizeAzureBlobORCConfig validates a single-object Azure Blob ORC profile.
func NormalizeAzureBlobORCConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("azure-blob-orc data source config is required")
	}
	container := strings.TrimSpace(config.Database)
	if container == "" || container == "." || container == ".." || strings.ContainsAny(container, "/\\") {
		return nil, errors.New("azure-blob-orc container must be one path-safe name in database")
	}
	if strings.TrimSpace(config.Username) == "" {
		return nil, errors.New("azure-blob-orc account name is required in username")
	}
	sasToken := configString(config.AdditionalProps, "sas_token")
	if sasToken == "" && strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("azure-blob-orc account key or sas_token is required")
	}
	if sasToken != "" {
		parsed, err := url.ParseQuery(strings.TrimPrefix(sasToken, "?"))
		if err != nil || len(parsed) == 0 {
			return nil, errors.New("azure-blob-orc sas_token must be a valid query string")
		}
	}
	if !validAzureBlobORCObjectKey(AzureBlobORCObjectKey(config)) {
		return nil, errors.New("azure-blob-orc object_key must be a safe relative .orc object key")
	}
	if endpoint := configString(config.AdditionalProps, "endpoint_url"); endpoint != "" {
		if err := validateAzureBlobEndpoint(endpoint); err != nil {
			return nil, err
		}
	}
	if _, err := azureBlobORCMaxFileBytes(config); err != nil {
		return nil, err
	}
	if _, err := azureBlobORCMaxRows(config); err != nil {
		return nil, err
	}
	normalized := *config
	normalized.AdditionalProps = make(map[string]interface{}, len(config.AdditionalProps))
	for key, value := range config.AdditionalProps {
		normalized.AdditionalProps[key] = value
	}
	return &normalized, nil
}

func ValidateAzureBlobORCConfig(config *model.DataSourceConfig) error {
	_, err := NormalizeAzureBlobORCConfig(config)
	return err
}

func azureBlobORCMaxFileBytes(config *model.DataSourceConfig) (int64, error) {
	if config == nil || config.AdditionalProps["max_file_bytes"] == nil {
		return defaultAzureBlobORCMaxFileBytes, nil
	}
	value, err := parseAzureBlobConfigInt64(config.AdditionalProps["max_file_bytes"])
	if err != nil {
		return 0, errors.New("azure-blob-orc max_file_bytes must be an integer")
	}
	if value <= 0 || value > maxAzureBlobORCMaxFileBytes {
		return 0, fmt.Errorf("azure-blob-orc max_file_bytes must be between 1 and %d", maxAzureBlobORCMaxFileBytes)
	}
	return value, nil
}

func azureBlobORCMaxRows(config *model.DataSourceConfig) (int, error) {
	if config == nil || config.AdditionalProps["max_rows"] == nil {
		return defaultAzureBlobORCMaxRows, nil
	}
	value, err := parseAzureBlobConfigInt64(config.AdditionalProps["max_rows"])
	if err != nil || value <= 0 || value > maxAzureBlobORCMaxRows {
		return 0, fmt.Errorf("azure-blob-orc max_rows must be between 1 and %d", maxAzureBlobORCMaxRows)
	}
	return int(value), nil
}

func validAzureBlobORCObjectKey(key string) bool {
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

func AzureBlobORCObjectKey(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if key := configString(config.AdditionalProps, "object_key"); key != "" {
		return key
	}
	return strings.TrimSpace(config.Host)
}

func AzureBlobORCTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "azure-blob-orc")
}
