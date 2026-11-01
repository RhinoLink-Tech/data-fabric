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

// COSParquetDriver implements Driver interface for querying Parquet files on Tencent Cloud COS
type COSParquetDriver struct {
	cosClient *COSClient
	config    *COSParquetDriverConfig
	objectKey string
	tableName string
}

// COSParquetDriverConfig holds COS Parquet driver configuration
type COSParquetDriverConfig struct {
	COSConfig *COSConfig
	BatchSize int
}

// NewCOSParquetDriver creates a new COS Parquet driver
func NewCOSParquetDriver(ctx context.Context, config *COSParquetDriverConfig) (*COSParquetDriver, error) {
	if config == nil || config.COSConfig == nil {
		return nil, fmt.Errorf("COS Parquet configuration is required")
	}
	configCopy := *config
	if configCopy.BatchSize <= 0 {
		configCopy.BatchSize = 1000
	}
	config = &configCopy
	cosClient, err := NewCOSClient(ctx, config.COSConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create COS client: %w", err)
	}

	return &COSParquetDriver{
		cosClient: cosClient,
		config:    config,
	}, nil
}

// Open opens a connection (not applicable for COS Parquet)
func (d *COSParquetDriver) Open(dsn string) (*sql.DB, error) {
	return nil, fmt.Errorf("COS Parquet driver does not support standard database connections")
}

// ValidateDSN validates the connection string
func (d *COSParquetDriver) ValidateDSN(dsn string) error {
	if !strings.HasPrefix(dsn, "cos://") {
		return fmt.Errorf("invalid COS URI: %s", dsn)
	}
	bucket, objectKey, found := strings.Cut(strings.TrimPrefix(dsn, "cos://"), "/")
	if !found || strings.TrimSpace(bucket) == "" || strings.TrimSpace(objectKey) == "" {
		return fmt.Errorf("COS URI must include a bucket and object key")
	}
	return nil
}

// GetDefaultPort returns the default COS port
func (d *COSParquetDriver) GetDefaultPort() int {
	return 443 // HTTPS
}

// BuildDSN builds a connection string from configuration
func (d *COSParquetDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "cos://"
	}
	objectKey := configString(config.AdditionalProps, "object_key")
	if objectKey == "" {
		objectKey = config.Host
	}
	return fmt.Sprintf("cos://%s/%s", config.Database, objectKey)
}

// GetDatabaseTypeName returns the database type name
func (d *COSParquetDriver) GetDatabaseTypeName() string {
	return "cos-parquet"
}

// TestConnection tests if the connection is working
func (d *COSParquetDriver) TestConnection(db *sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead")
}

// TestConnectionContext tests the COS connection
func (d *COSParquetDriver) TestConnectionContext(ctx context.Context) error {
	if d == nil || d.cosClient == nil {
		return fmt.Errorf("COS Parquet driver is not initialized")
	}
	_, err := d.cosClient.ListObjects(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("failed to list COS objects: %w", err)
	}
	return nil
}

// GetDriverName returns the driver name
func (d *COSParquetDriver) GetDriverName() string {
	return "cos-parquet"
}

// GetCategory returns the driver category
func (d *COSParquetDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

// GetCapabilities returns driver capabilities
func (d *COSParquetDriver) GetCapabilities() drivers.DriverCapabilities {
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
func (d *COSParquetDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// Query executes a query against Parquet files on COS
func (d *COSParquetDriver) Query(ctx context.Context, key string) (*COSParquetResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if d == nil || d.cosClient == nil || d.config == nil {
		return nil, fmt.Errorf("COS Parquet driver is not initialized")
	}
	if key == "" {
		return nil, fmt.Errorf("Parquet object key is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := d.cosClient.GetObject(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get Parquet file: %w", err)
	}
	fileReader, err := newParquetFileReader(newParquetMemoryFile(key, data), int64(d.config.BatchSize))
	if err != nil {
		return nil, fmt.Errorf("failed to open Parquet file: %w", err)
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
	return &COSParquetResult{
		Rows: rows, Schema: schema, NumRows: int64(len(rows)), BytesRead: int64(len(data)),
	}, nil
}

// GetSchema reads one configured COS Parquet object and returns its flat schema.
func (d *COSParquetDriver) GetSchema(ctx context.Context, key string) (*ParquetSchema, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if d == nil || d.cosClient == nil {
		return nil, fmt.Errorf("COS Parquet driver is not initialized")
	}
	if key == "" {
		return nil, fmt.Errorf("Parquet object key is required")
	}
	data, err := d.cosClient.GetObject(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get Parquet file: %w", err)
	}
	fileReader, err := newParquetFileReader(newParquetMemoryFile(key, data), 1)
	if err != nil {
		return nil, fmt.Errorf("failed to open Parquet file: %w", err)
	}
	defer fileReader.Close()
	return fileReader.GetSchema()
}

// COSParquetResult represents query results
type COSParquetResult struct {
	Rows      []map[string]interface{}
	Schema    *ParquetSchema
	NumRows   int64
	BytesRead int64
}

// ListParquetFiles lists Parquet files in a prefix
func (d *COSParquetDriver) ListParquetFiles(ctx context.Context, prefix string) ([]COSObject, error) {
	return d.cosClient.ListFilesByExtension(ctx, prefix, ".parquet")
}

// GetFileMetadata retrieves metadata for a Parquet file
func (d *COSParquetDriver) GetFileMetadata(ctx context.Context, key string) (*COSFileMetadata, error) {
	metadata, err := d.cosClient.GetObjectMetadata(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get object metadata: %w", err)
	}

	return &COSFileMetadata{
		Key:       metadata.Key,
		Size:      metadata.ContentLength,
		UpdatedAt: metadata.LastModified,
		ETag:      metadata.ETag,
	}, nil
}

// COSFileMetadata represents COS file metadata
type COSFileMetadata struct {
	Key       string
	Size      int64
	UpdatedAt time.Time
	ETag      string
}
