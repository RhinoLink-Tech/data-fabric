package object_storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// AzureBlobParquetDriver implements Driver interface for querying Parquet files on Azure Blob Storage
type AzureBlobParquetDriver struct {
	azureClient *AzureBlobClient
	config      *AzureBlobParquetDriverConfig
	objectKey   string
	tableName   string
}

// AzureBlobParquetDriverConfig holds Azure Blob Parquet driver configuration
type AzureBlobParquetDriverConfig struct {
	AzureConfig *AzureBlobConfig
	BatchSize   int
}

// NewAzureBlobParquetDriver creates a new Azure Blob Parquet driver
func NewAzureBlobParquetDriver(ctx context.Context, config *AzureBlobParquetDriverConfig) (*AzureBlobParquetDriver, error) {
	if config == nil || config.AzureConfig == nil {
		return nil, fmt.Errorf("Azure Blob Parquet configuration is required")
	}
	configCopy := *config
	if configCopy.BatchSize <= 0 {
		configCopy.BatchSize = 1000
	}
	config = &configCopy
	azureClient, err := NewAzureBlobClient(ctx, config.AzureConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure Blob client: %w", err)
	}

	return &AzureBlobParquetDriver{
		azureClient: azureClient,
		config:      config,
	}, nil
}

// Open opens a connection (not applicable for Azure Blob Parquet)
func (d *AzureBlobParquetDriver) Open(dsn string) (*sql.DB, error) {
	return nil, fmt.Errorf("Azure Blob Parquet driver does not support standard database connections")
}

// ValidateDSN validates the connection string
func (d *AzureBlobParquetDriver) ValidateDSN(dsn string) error {
	if !strings.HasPrefix(dsn, "azure://") {
		return fmt.Errorf("invalid Azure Blob URI: %s", dsn)
	}
	container, blobName, found := strings.Cut(strings.TrimPrefix(dsn, "azure://"), "/")
	if !found || strings.TrimSpace(container) == "" || strings.TrimSpace(blobName) == "" {
		return fmt.Errorf("Azure Blob URI must include a container and blob name")
	}
	return nil
}

// GetDefaultPort returns the default Azure Blob port
func (d *AzureBlobParquetDriver) GetDefaultPort() int {
	return 443 // HTTPS
}

// BuildDSN builds a connection string from configuration
func (d *AzureBlobParquetDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "azure://"
	}
	objectKey := configString(config.AdditionalProps, "object_key")
	if objectKey == "" {
		objectKey = config.Host
	}
	return fmt.Sprintf("azure://%s/%s", config.Database, objectKey)
}

// GetDatabaseTypeName returns the database type name
func (d *AzureBlobParquetDriver) GetDatabaseTypeName() string {
	return "azure-parquet"
}

// TestConnection tests if the connection is working
func (d *AzureBlobParquetDriver) TestConnection(db *sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead")
}

// TestConnectionContext tests the Azure Blob connection
func (d *AzureBlobParquetDriver) TestConnectionContext(ctx context.Context) error {
	if d == nil || d.azureClient == nil {
		return fmt.Errorf("Azure Blob Parquet driver is not initialized")
	}
	_, err := d.azureClient.ListBlobs(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("failed to list Azure blobs: %w", err)
	}
	return nil
}

// GetDriverName returns the driver name
func (d *AzureBlobParquetDriver) GetDriverName() string {
	return "azure-parquet"
}

// GetCategory returns the driver category
func (d *AzureBlobParquetDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

// GetCapabilities returns driver capabilities
func (d *AzureBlobParquetDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     false,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       false,
	}
}

// ConfigureAuth configures authentication
func (d *AzureBlobParquetDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// Query executes a query against Parquet files on Azure Blob
func (d *AzureBlobParquetDriver) Query(ctx context.Context, name string) (*AzureBlobParquetResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if d == nil || d.azureClient == nil || d.config == nil {
		return nil, fmt.Errorf("Azure Blob Parquet driver is not initialized")
	}
	if name == "" {
		return nil, fmt.Errorf("Parquet blob name is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := d.azureClient.GetBlob(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get Parquet blob: %w", err)
	}
	fileReader, err := newParquetFileReader(newParquetMemoryFile(name, data), int64(d.config.BatchSize))
	if err != nil {
		return nil, fmt.Errorf("failed to open Parquet blob: %w", err)
	}
	defer fileReader.Close()

	schema, err := fileReader.GetSchema()
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]interface{}, 0, fileReader.GetNumRows())
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := fileReader.ReadNumRows(d.config.BatchSize)
		if err != nil {
			return nil, fmt.Errorf("failed to decode Parquet rows: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		rows = append(rows, batch...)
	}
	return &AzureBlobParquetResult{
		Rows: rows, Schema: schema, NumRows: int64(len(rows)), BytesRead: int64(len(data)),
	}, nil
}

// GetSchema reads one Azure Blob Parquet object and returns its flat schema.
func (d *AzureBlobParquetDriver) GetSchema(ctx context.Context, name string) (*ParquetSchema, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if d == nil || d.azureClient == nil {
		return nil, fmt.Errorf("Azure Blob Parquet driver is not initialized")
	}
	if name == "" {
		return nil, fmt.Errorf("Parquet blob name is required")
	}
	data, err := d.azureClient.GetBlob(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get Parquet blob: %w", err)
	}
	fileReader, err := newParquetFileReader(newParquetMemoryFile(name, data), 1)
	if err != nil {
		return nil, fmt.Errorf("failed to open Parquet blob: %w", err)
	}
	defer fileReader.Close()
	return fileReader.GetSchema()
}

// AzureBlobParquetResult represents query results
type AzureBlobParquetResult struct {
	Rows      []map[string]interface{}
	Schema    *ParquetSchema
	NumRows   int64
	BytesRead int64
}

// ListParquetFiles lists Parquet files in a container
func (d *AzureBlobParquetDriver) ListParquetFiles(ctx context.Context, prefix string) ([]AzureBlob, error) {
	return d.azureClient.ListBlobsByExtension(ctx, prefix, ".parquet")
}

// GetFileMetadata retrieves metadata for a Parquet file
func (d *AzureBlobParquetDriver) GetFileMetadata(ctx context.Context, name string) (*AzureFileMetadata, error) {
	metadata, err := d.azureClient.GetBlobMetadata(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get blob metadata: %w", err)
	}

	return &AzureFileMetadata{
		Name:      metadata.Name,
		Size:      metadata.ContentLength,
		UpdatedAt: metadata.LastModified,
		ETag:      metadata.ETag,
	}, nil
}

// AzureFileMetadata represents Azure file metadata
type AzureFileMetadata struct {
	Name      string
	Size      int64
	UpdatedAt time.Time
	ETag      string
}

// RegisterAzureBlobParquetDriver registers the Azure Blob Parquet driver globally
