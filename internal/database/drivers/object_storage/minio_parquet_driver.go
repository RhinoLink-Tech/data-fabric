package object_storage

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// MinIOParquetDriver implements Driver interface for querying Parquet files on MinIO
type MinIOParquetDriver struct {
	minioClient     *MinIOClient
	parquetReader   *ParquetReader
	s3ParquetDriver *S3ParquetDriver // Reuse S3 driver logic
	config          *MinIOParquetDriverConfig
	objectKey       string
	tableName       string
}

// MinIOParquetDriverConfig holds MinIO Parquet driver configuration
type MinIOParquetDriverConfig struct {
	MinIOConfig    *MinIOConfig
	BatchSize      int
	EnablePushdown bool
}

// NewMinIOParquetDriver creates a new MinIO Parquet driver
func NewMinIOParquetDriver(ctx context.Context, config *MinIOParquetDriverConfig) (*MinIOParquetDriver, error) {
	if config == nil || config.MinIOConfig == nil {
		return nil, fmt.Errorf("MinIO Parquet configuration is required")
	}
	minioClient, err := NewMinIOClient(ctx, config.MinIOConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	// Note: We can't directly reuse S3Client because MinIO uses a different client
	// Instead, we create adapter methods

	return &MinIOParquetDriver{
		minioClient: minioClient,
		config:      config,
	}, nil
}

// Open opens a connection (not applicable for MinIO Parquet)
func (d *MinIOParquetDriver) Open(dsn string) (*sql.DB, error) {
	return nil, fmt.Errorf("MinIO Parquet driver does not support standard database connections")
}

// ValidateDSN validates the connection string
func (d *MinIOParquetDriver) ValidateDSN(dsn string) error {
	// The shared parser accepts the S3 scheme; translate MinIO's public DSN
	// scheme before applying its bucket/key validation.
	if strings.HasPrefix(dsn, "minio://") {
		dsn = "s3://" + strings.TrimPrefix(dsn, "minio://")
	}
	bucket, _, err := ParseS3URI(dsn)
	if err != nil {
		return fmt.Errorf("invalid MinIO URI: %w", err)
	}

	if bucket == "" {
		return fmt.Errorf("bucket is required in MinIO URI")
	}

	return nil
}

// GetDefaultPort returns the default MinIO port
func (d *MinIOParquetDriver) GetDefaultPort() int {
	return 9000 // Default MinIO port
}

// BuildDSN builds a connection string from configuration
func (d *MinIOParquetDriver) BuildDSN(config *model.DataSourceConfig) string {
	return fmt.Sprintf("minio://%s/%s", config.Database, config.Host)
}

// GetDatabaseTypeName returns the database type name
func (d *MinIOParquetDriver) GetDatabaseTypeName() string {
	return "minio-parquet"
}

// TestConnection tests if the connection is working
func (d *MinIOParquetDriver) TestConnection(db *sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead")
}

// TestConnectionContext tests the MinIO connection
func (d *MinIOParquetDriver) TestConnectionContext(ctx context.Context) error {
	if d == nil || d.minioClient == nil {
		return fmt.Errorf("MinIO Parquet driver is not initialized")
	}
	// Try to check bucket existence
	_, err := d.minioClient.BucketExists(ctx, d.minioClient.bucket)
	if err != nil {
		return fmt.Errorf("failed to check MinIO bucket: %w", err)
	}
	return nil
}

// GetDriverName returns the driver name
func (d *MinIOParquetDriver) GetDriverName() string {
	return "minio-parquet"
}

// GetCategory returns the driver category
func (d *MinIOParquetDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

// GetCapabilities returns driver capabilities
func (d *MinIOParquetDriver) GetCapabilities() drivers.DriverCapabilities {
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
func (d *MinIOParquetDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// =============================================================================
// MinIO Parquet-Specific Methods
// =============================================================================

// Query executes a query against Parquet files on MinIO
func (d *MinIOParquetDriver) Query(ctx context.Context, query *MinIOParquetQuery) (*MinIOParquetResult, error) {
	if query == nil {
		return nil, fmt.Errorf("MinIO Parquet query is required")
	}
	if d == nil || d.minioClient == nil {
		return nil, fmt.Errorf("MinIO Parquet driver is not initialized")
	}
	if query.Key == "" {
		return nil, fmt.Errorf("Parquet file key is required")
	}

	// Create a seekable reader for the Parquet file
	reader := NewMinIOParquetReader(d.minioClient, query.Key)
	if d.config != nil && d.config.BatchSize > 0 {
		reader.batchSize = int64(d.config.BatchSize)
	}

	fileReader, err := reader.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read Parquet file: %w", err)
	}
	defer fileReader.Close()

	schema, err := fileReader.GetSchema()
	if err != nil {
		return nil, fmt.Errorf("failed to read Parquet schema: %w", err)
	}
	resultSchema := schema
	if len(query.Columns) > 0 {
		available := make(map[string]ParquetColumn, len(schema.Columns))
		for _, column := range schema.Columns {
			available[column.Name] = column
		}
		resultSchema = &ParquetSchema{Columns: make([]ParquetColumn, 0, len(query.Columns))}
		for _, name := range query.Columns {
			column, ok := available[name]
			if !ok {
				return nil, fmt.Errorf("Parquet column %q does not exist", name)
			}
			resultSchema.Columns = append(resultSchema.Columns, column)
		}
	}

	rows, err := fileReader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read rows: %w", err)
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
	if len(query.Columns) > 0 {
		projected := make([]map[string]interface{}, 0, len(rows))
		for _, row := range rows {
			projectedRow := make(map[string]interface{}, len(query.Columns))
			for _, name := range query.Columns {
				projectedRow[name] = row[name]
			}
			projected = append(projected, projectedRow)
		}
		rows = projected
	}
	return &MinIOParquetResult{
		Rows:      rows,
		Schema:    resultSchema,
		NumRows:   int64(len(rows)),
		BytesRead: fileReader.BytesRead(),
	}, nil
}

// MinIOParquetQuery represents a query against MinIO Parquet files
type MinIOParquetQuery struct {
	Key     string
	Columns []string
	Filters map[string]interface{}
	Limit   int
	Offset  int
}

// MinIOParquetResult represents query results
type MinIOParquetResult struct {
	Rows      []map[string]interface{}
	Schema    *ParquetSchema
	NumRows   int64
	BytesRead int64
}

// filterRows applies filters to rows
func (d *MinIOParquetDriver) filterRows(rows []map[string]interface{}, filters map[string]interface{}) []map[string]interface{} {
	filtered := make([]map[string]interface{}, 0, len(rows))

	for _, row := range rows {
		if d.matchesFilters(row, filters) {
			filtered = append(filtered, row)
		}
	}

	return filtered
}

// matchesFilters checks if a row matches all filters
func (d *MinIOParquetDriver) matchesFilters(row map[string]interface{}, filters map[string]interface{}) bool {
	for col, expectedVal := range filters {
		actualVal, exists := row[col]
		if !exists || !reflect.DeepEqual(actualVal, expectedVal) {
			return false
		}
	}
	return true
}

// ListParquetFiles lists Parquet files in a prefix
func (d *MinIOParquetDriver) ListParquetFiles(ctx context.Context, prefix string) ([]MinIOObject, error) {
	return d.minioClient.ListFilesByExtension(ctx, prefix, ".parquet")
}

// GetFileMetadata retrieves metadata for a Parquet file
func (d *MinIOParquetDriver) GetFileMetadata(ctx context.Context, key string) (*MinIOFileMetadata, error) {
	objMetadata, err := d.minioClient.GetObjectMetadata(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get object metadata: %w", err)
	}

	return &MinIOFileMetadata{
		Key:       key,
		Size:      objMetadata.ContentLength,
		UpdatedAt: objMetadata.LastModified,
		ETag:      objMetadata.ETag,
	}, nil
}

// MinIOFileMetadata represents MinIO file metadata
type MinIOFileMetadata struct {
	Key       string
	Size      int64
	UpdatedAt time.Time
	ETag      string
}

// StreamQuery streams query results using callback
func (d *MinIOParquetDriver) StreamQuery(ctx context.Context, key string, callback func(map[string]interface{}) error) error {
	reader := NewMinIOParquetReader(d.minioClient, key)

	fileReader, err := reader.Read(ctx)
	if err != nil {
		return fmt.Errorf("failed to open Parquet file: %w", err)
	}
	defer fileReader.Close()

	// Read all rows (streaming implementation would use iterator)
	rows, err := fileReader.ReadAll()
	if err != nil {
		return err
	}

	for _, row := range rows {
		if err := callback(row); err != nil {
			return err
		}
	}

	return nil
}

// GetSchema retrieves schema for a Parquet file
func (d *MinIOParquetDriver) GetSchema(ctx context.Context, key string) (*ParquetSchema, error) {
	if d == nil || d.minioClient == nil {
		return nil, fmt.Errorf("MinIO Parquet driver is not initialized")
	}
	reader := NewMinIOParquetReader(d.minioClient, key)
	fileReader, err := reader.Read(ctx)
	if err != nil {
		return nil, err
	}
	defer fileReader.Close()

	return fileReader.GetSchema()
}

// GetColumnNames returns column names for a Parquet file
func (d *MinIOParquetDriver) GetColumnNames(ctx context.Context, key string) ([]string, error) {
	schema, err := d.GetSchema(ctx, key)
	if err != nil {
		return nil, err
	}

	columns := make([]string, 0, len(schema.Columns))
	for _, col := range schema.Columns {
		columns = append(columns, col.Name)
	}

	return columns, nil
}

// GetFileCount counts Parquet files in a prefix
func (d *MinIOParquetDriver) GetFileCount(ctx context.Context, prefix string) (int, error) {
	files, err := d.ListParquetFiles(ctx, prefix)
	if err != nil {
		return 0, err
	}
	return len(files), nil
}

// GetTotalSize calculates total size of Parquet files in a prefix
func (d *MinIOParquetDriver) GetTotalSize(ctx context.Context, prefix string) (int64, error) {
	files, err := d.ListParquetFiles(ctx, prefix)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, file := range files {
		total += file.Size
	}

	return total, nil
}

// ValidateFile checks if a Parquet file is valid
func (d *MinIOParquetDriver) ValidateFile(ctx context.Context, key string) error {
	reader := NewMinIOParquetReader(d.minioClient, key)
	fileReader, err := reader.Read(ctx)
	if err != nil {
		return err
	}
	defer fileReader.Close()

	// Try to read schema
	_, err = fileReader.GetSchema()
	return err
}

// ConvertToStandardSchema converts Parquet schema to standard schema
func (d *MinIOParquetDriver) ConvertToStandardSchema(parquetSchema *ParquetSchema) model.TableSchema {
	stdSchema := model.TableSchema{}
	if parquetSchema == nil {
		return stdSchema
	}
	stdSchema.Columns = make([]model.ColumnInfo, 0, len(parquetSchema.Columns))
	for _, col := range parquetSchema.Columns {
		standardType := col.StandardType
		if standardType == "" {
			standardType = "unknown"
		}
		stdSchema.Columns = append(stdSchema.Columns, model.ColumnInfo{
			Name:     col.Name,
			Type:     model.StandardizedType(standardType),
			Nullable: col.RepetitionType == "OPTIONAL",
		})
	}
	return stdSchema
}

// MinIOParquetReader reads Parquet files from MinIO
type MinIOParquetReader struct {
	minioClient *MinIOClient
	key         string
	batchSize   int64
}

// NewMinIOParquetReader creates a new MinIO Parquet reader
func NewMinIOParquetReader(minioClient *MinIOClient, key string) *MinIOParquetReader {
	return &MinIOParquetReader{
		minioClient: minioClient,
		key:         key,
		batchSize:   1000,
	}
}

// Read opens a real Parquet reader backed by ranged MinIO object reads.
func (r *MinIOParquetReader) Read(ctx context.Context) (*ParquetFileReader, error) {
	if r == nil || r.minioClient == nil {
		return nil, fmt.Errorf("MinIO client is required to read Parquet files")
	}
	if r.key == "" {
		return nil, fmt.Errorf("Parquet file key is required")
	}
	file, err := NewMinIOParquetFile(ctx, r.minioClient, r.key)
	if err != nil {
		return nil, err
	}
	fileReader, err := newParquetFileReader(file, r.batchSize)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("failed to open MinIO Parquet object %q: %w", r.key, err)
	}
	return fileReader, nil
}

// MemoryFile represents an in-memory file
type MemoryFile struct {
	data []byte
	name string
	pos  int64
}

// Read implements io.Reader
func (f *MemoryFile) Read(p []byte) (n int, err error) {
	if f.pos >= int64(len(f.data)) {
		return 0, io.EOF
	}

	n = copy(p, f.data[f.pos:])
	f.pos += int64(n)

	return n, nil
}

// Seek implements io.Seeker
func (f *MemoryFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.pos = offset
	case io.SeekCurrent:
		f.pos += offset
	case io.SeekEnd:
		f.pos = int64(len(f.data)) + offset
	default:
		return 0, fmt.Errorf("invalid whence: %d", whence)
	}

	if f.pos < 0 {
		f.pos = 0
	} else if f.pos > int64(len(f.data)) {
		f.pos = int64(len(f.data))
	}

	return f.pos, nil
}

// Close implements io.Closer
func (f *MemoryFile) Close() error {
	return nil
}

// GetSize returns the file size
func (f *MemoryFile) GetSize() int64 {
	return int64(len(f.data))
}
