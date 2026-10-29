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

// OSSParquetDriver implements Driver interface for querying Parquet files on Alibaba Cloud OSS
type OSSParquetDriver struct {
	ossClient *OSSClient
	config    *OSSParquetDriverConfig
	objectKey string
	tableName string
}

// OSSParquetDriverConfig holds OSS Parquet driver configuration
type OSSParquetDriverConfig struct {
	OSSConfig      *OSSConfig
	BatchSize      int
	EnablePushdown bool
}

// NewOSSParquetDriver creates a new OSS Parquet driver
func NewOSSParquetDriver(ctx context.Context, config *OSSParquetDriverConfig) (*OSSParquetDriver, error) {
	if config == nil || config.OSSConfig == nil {
		return nil, fmt.Errorf("OSS Parquet configuration is required")
	}
	configCopy := *config
	if configCopy.BatchSize <= 0 {
		configCopy.BatchSize = 1000
	}
	config = &configCopy
	ossClient, err := NewOSSClient(ctx, config.OSSConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create OSS client: %w", err)
	}

	return &OSSParquetDriver{
		ossClient: ossClient,
		config:    config,
	}, nil
}

// Open opens a connection (not applicable for OSS Parquet)
func (d *OSSParquetDriver) Open(dsn string) (*sql.DB, error) {
	return nil, fmt.Errorf("OSS Parquet driver does not support standard database connections")
}

// ValidateDSN validates the connection string
func (d *OSSParquetDriver) ValidateDSN(dsn string) error {
	if !strings.HasPrefix(dsn, "oss://") {
		return fmt.Errorf("invalid OSS URI: %s", dsn)
	}
	bucket, objectKey, found := strings.Cut(strings.TrimPrefix(dsn, "oss://"), "/")
	if !found || strings.TrimSpace(bucket) == "" || strings.TrimSpace(objectKey) == "" {
		return fmt.Errorf("OSS URI must include a bucket and object key")
	}
	return nil
}

// GetDefaultPort returns the default OSS port
func (d *OSSParquetDriver) GetDefaultPort() int {
	return 443 // HTTPS
}

// BuildDSN builds a connection string from configuration
func (d *OSSParquetDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "oss://"
	}
	objectKey := configString(config.AdditionalProps, "object_key")
	if objectKey == "" {
		objectKey = config.Host
	}
	return fmt.Sprintf("oss://%s/%s", config.Database, objectKey)
}

// GetDatabaseTypeName returns the database type name
func (d *OSSParquetDriver) GetDatabaseTypeName() string {
	return "oss-parquet"
}

// TestConnection tests if the connection is working
func (d *OSSParquetDriver) TestConnection(db *sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead")
}

// TestConnectionContext tests the OSS connection
func (d *OSSParquetDriver) TestConnectionContext(ctx context.Context) error {
	if d == nil || d.ossClient == nil {
		return fmt.Errorf("OSS Parquet driver is not initialized")
	}
	_, err := d.ossClient.ListObjects(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("failed to list OSS objects: %w", err)
	}
	return nil
}

// GetDriverName returns the driver name
func (d *OSSParquetDriver) GetDriverName() string {
	return "oss-parquet"
}

// GetCategory returns the driver category
func (d *OSSParquetDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

// GetCapabilities returns driver capabilities
func (d *OSSParquetDriver) GetCapabilities() drivers.DriverCapabilities {
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
func (d *OSSParquetDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// Query executes a query against Parquet files on OSS
func (d *OSSParquetDriver) Query(ctx context.Context, key string) (*OSSParquetResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if d == nil || d.ossClient == nil || d.config == nil {
		return nil, fmt.Errorf("OSS Parquet driver is not initialized")
	}
	if key == "" {
		return nil, fmt.Errorf("Parquet object key is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := d.ossClient.GetObject(ctx, key)
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

	return &OSSParquetResult{
		Rows:      rows,
		Schema:    schema,
		NumRows:   int64(len(rows)),
		BytesRead: int64(len(data)),
	}, nil
}

// GetSchema reads the configured OSS Parquet object and returns its flat schema.
func (d *OSSParquetDriver) GetSchema(ctx context.Context, key string) (*ParquetSchema, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if d == nil || d.ossClient == nil {
		return nil, fmt.Errorf("OSS Parquet driver is not initialized")
	}
	if key == "" {
		return nil, fmt.Errorf("Parquet object key is required")
	}
	data, err := d.ossClient.GetObject(ctx, key)
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

// OSSParquetResult represents query results
type OSSParquetResult struct {
	Rows      []map[string]interface{}
	Schema    *ParquetSchema
	NumRows   int64
	BytesRead int64
}

// ListParquetFiles lists Parquet files in a prefix
func (d *OSSParquetDriver) ListParquetFiles(ctx context.Context, prefix string) ([]OSSObject, error) {
	return d.ossClient.ListFilesByExtension(ctx, prefix, ".parquet")
}

// GetFileMetadata retrieves metadata for a Parquet file
func (d *OSSParquetDriver) GetFileMetadata(ctx context.Context, key string) (*OSSFileMetadata, error) {
	metadata, err := d.ossClient.GetObjectMetadata(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get object metadata: %w", err)
	}

	return &OSSFileMetadata{
		Key:       key,
		Size:      metadata.ContentLength,
		UpdatedAt: metadata.LastModified,
		ETag:      metadata.ETag,
	}, nil
}

// OSSFileMetadata represents OSS file metadata
type OSSFileMetadata struct {
	Key       string
	Size      int64
	UpdatedAt time.Time
	ETag      string
}

// ApplyBatchPagination adds pagination to SQL query
func (d *OSSParquetDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	// For OSS Parquet files, pagination is typically not supported in the same way as traditional databases
	// We return the original SQL as-is since Parquet files don't support LIMIT/OFFSET in the same way
	// The pagination is usually handled at the application level
	return sql, nil
}
