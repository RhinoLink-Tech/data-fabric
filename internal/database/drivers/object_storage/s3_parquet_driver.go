package object_storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// S3ParquetDriver implements Driver interface for querying Parquet files on S3
type S3ParquetDriver struct {
	s3Client      *S3Client
	parquetReader *ParquetReader
	selectHandler *S3SelectHandler
	config        *S3ParquetDriverConfig
	objectKey     string
	tableName     string
}

// S3ParquetDriverConfig holds S3 Parquet driver configuration
type S3ParquetDriverConfig struct {
	S3Config        *S3Config
	BatchSize       int
	EnableSelectAPI bool
	EnablePushdown  bool
}

// NewS3ParquetDriver creates a new S3 Parquet driver
func NewS3ParquetDriver(ctx context.Context, config *S3ParquetDriverConfig) (*S3ParquetDriver, error) {
	if config == nil || config.S3Config == nil {
		return nil, fmt.Errorf("S3 Parquet configuration is required")
	}
	s3Client, err := NewS3Client(ctx, config.S3Config)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	parquetConfig := &ParquetReaderConfig{
		BatchSize: config.BatchSize,
		Pushdown:  config.EnablePushdown,
	}

	parquetReader := NewParquetReader(s3Client, parquetConfig)

	selectHandler := NewS3SelectHandler(s3Client)

	return &S3ParquetDriver{
		s3Client:      s3Client,
		parquetReader: parquetReader,
		selectHandler: selectHandler,
		config:        config,
	}, nil
}

// Open opens a connection (not applicable for S3 Parquet)
func (d *S3ParquetDriver) Open(dsn string) (*sql.DB, error) {
	return nil, fmt.Errorf("S3 Parquet driver does not support standard database connections")
}

// ValidateDSN validates the connection string
func (d *S3ParquetDriver) ValidateDSN(dsn string) error {
	// Parse S3 URI
	bucket, _, err := ParseS3URI(dsn)
	if err != nil {
		return fmt.Errorf("invalid S3 URI: %w", err)
	}

	if bucket == "" {
		return fmt.Errorf("bucket is required in S3 URI")
	}

	return nil
}

// GetDefaultPort returns the default S3 port
func (d *S3ParquetDriver) GetDefaultPort() int {
	return 443 // HTTPS
}

// BuildDSN builds a connection string from configuration
func (d *S3ParquetDriver) BuildDSN(config *model.DataSourceConfig) string {
	return fmt.Sprintf("s3://%s/%s", config.Database, config.Host)
}

// GetDatabaseTypeName returns the database type name
func (d *S3ParquetDriver) GetDatabaseTypeName() string {
	return "s3-parquet"
}

// TestConnection tests if the connection is working
func (d *S3ParquetDriver) TestConnection(db *sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead")
}

// TestConnectionContext tests the S3 connection
func (d *S3ParquetDriver) TestConnectionContext(ctx context.Context) error {
	// Try to list objects in bucket
	_, err := d.s3Client.ListObjects(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("failed to list S3 objects: %w", err)
	}
	return nil
}

// GetDriverName returns the driver name
func (d *S3ParquetDriver) GetDriverName() string {
	return "s3-parquet"
}

// GetCategory returns the driver category
func (d *S3ParquetDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

// GetCapabilities returns driver capabilities
func (d *S3ParquetDriver) GetCapabilities() drivers.DriverCapabilities {
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
func (d *S3ParquetDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// =============================================================================
// S3 Parquet-Specific Methods
// =============================================================================

// Query executes a query against Parquet files
func (d *S3ParquetDriver) Query(ctx context.Context, query *S3ParquetQuery) (*S3ParquetResult, error) {
	if query.Key == "" {
		return nil, fmt.Errorf("Parquet file key is required")
	}

	// Use S3 Select if enabled and applicable
	if d.config.EnableSelectAPI && query.UseSelectAPI {
		return d.queryWithSelect(ctx, query)
	}

	// Otherwise use Parquet reader
	return d.queryWithReader(ctx, query)
}

// S3ParquetQuery represents a query against S3 Parquet files
type S3ParquetQuery struct {
	Key          string
	Columns      []string
	Filters      map[string]interface{}
	Limit        int
	Offset       int
	UseSelectAPI bool
	SelectSQL    string // Custom SQL for S3 Select
}

// S3ParquetResult represents query results
type S3ParquetResult struct {
	Rows      []map[string]interface{}
	Schema    *ParquetSchema
	Metadata  *ParquetMetadata
	BytesRead int64
	NumRows   int64
}

// queryWithSelect uses S3 Select API
func (d *S3ParquetDriver) queryWithSelect(ctx context.Context, query *S3ParquetQuery) (*S3ParquetResult, error) {
	sql := query.SelectSQL
	if sql == "" {
		sql = BuildCSVQuery(query.Key, "", query.Columns)
	}

	result, err := d.selectHandler.QueryParquet(ctx, query.Key, sql)
	if err != nil {
		return nil, fmt.Errorf("S3 Select query failed: %w", err)
	}

	return &S3ParquetResult{
		Rows:      result.Records,
		NumRows:   int64(len(result.Records)),
		BytesRead: result.BytesProcessed,
	}, nil
}

// queryWithReader uses Parquet reader
func (d *S3ParquetDriver) queryWithReader(ctx context.Context, query *S3ParquetQuery) (*S3ParquetResult, error) {
	// Configure reader with column selection
	readerConfig := &ParquetReaderConfig{
		BatchSize: d.config.BatchSize,
		Pushdown:  d.config.EnablePushdown,
		Columns:   query.Columns,
	}

	reader := NewParquetReader(d.s3Client, readerConfig)
	fileReader, err := reader.Read(ctx, query.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to open Parquet file: %w", err)
	}
	defer fileReader.Close()

	// Read all rows
	rows, err := fileReader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read Parquet file: %w", err)
	}

	// Apply filters if specified
	if len(query.Filters) > 0 {
		rows = d.filterRows(rows, query.Filters)
	}

	// Apply offset and limit
	if query.Offset > 0 {
		if query.Offset >= len(rows) {
			rows = []map[string]interface{}{}
		} else {
			rows = rows[query.Offset:]
		}
	}

	if query.Limit > 0 && query.Limit < len(rows) {
		rows = rows[:query.Limit]
	}

	// Get metadata
	metadata, err := reader.GetMetadata(ctx, query.Key)
	if err != nil {
		metadata = nil
	}

	// Get schema
	schema, err := fileReader.GetSchema()
	if err != nil {
		schema = nil
	}

	return &S3ParquetResult{
		Rows:     rows,
		Schema:   schema,
		Metadata: metadata,
		NumRows:  int64(len(rows)),
	}, nil
}

// filterRows applies filters to rows
func (d *S3ParquetDriver) filterRows(rows []map[string]interface{}, filters map[string]interface{}) []map[string]interface{} {
	filtered := make([]map[string]interface{}, 0, len(rows))

	for _, row := range rows {
		if d.matchesFilters(row, filters) {
			filtered = append(filtered, row)
		}
	}

	return filtered
}

// matchesFilters checks if a row matches all filters
func (d *S3ParquetDriver) matchesFilters(row map[string]interface{}, filters map[string]interface{}) bool {
	for col, expectedVal := range filters {
		actualVal, exists := row[col]
		if !exists || actualVal != expectedVal {
			return false
		}
	}
	return true
}

// ApplyBatchPagination applies pagination to a SQL query
func (d *S3ParquetDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	return applyParquetBatchPagination(sql, batchSize, offset)
}

// GetSchema reads the configured object's footer without decoding its rows.
func (d *S3ParquetDriver) GetSchema(ctx context.Context, key string) (*ParquetSchema, error) {
	if d == nil || d.s3Client == nil {
		return nil, fmt.Errorf("S3 Parquet driver is not initialized")
	}
	batchSize := 1000
	if d.config != nil && d.config.BatchSize > 0 {
		batchSize = d.config.BatchSize
	}
	reader := NewParquetReader(d.s3Client, &ParquetReaderConfig{BatchSize: batchSize})
	fileReader, err := reader.Read(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to read Parquet schema: %w", err)
	}
	defer fileReader.Close()
	return fileReader.GetSchema()
}
