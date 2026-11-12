package object_storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// MinIOCSVDriver implements Driver interface for querying CSV files on MinIO
type MinIOCSVDriver struct {
	minioClient    *MinIOClient
	schemaDetector *CSVSchemadDetector
	config         *MinIOCSVDriverConfig
}

// MinIOCSVDriverConfig holds MinIO CSV driver configuration
type MinIOCSVDriverConfig struct {
	MinIOConfig    *MinIOConfig
	Delimiter      rune
	HasHeader      bool
	QuoteChar      rune
	SkipHeaderRows int
	NullValues     []string
}

// NewMinIOCSVDriver creates a new MinIO CSV driver
func NewMinIOCSVDriver(ctx context.Context, config *MinIOCSVDriverConfig) (*MinIOCSVDriver, error) {
	minioClient, err := NewMinIOClient(ctx, config.MinIOConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	detectionConfig := &CSVDetectionConfig{
		SampleSize:       1000,
		Delimiter:        config.Delimiter,
		HasHeader:        config.HasHeader,
		QuoteChar:        config.QuoteChar,
		HeaderRowsToSkip: config.SkipHeaderRows,
		NullValues:       config.NullValues,
	}

	schemaDetector := NewCSVSchemadDetector(detectionConfig)

	return &MinIOCSVDriver{
		minioClient:    minioClient,
		schemaDetector: schemaDetector,
		config:         config,
	}, nil
}

// Open opens a connection (not applicable for MinIO CSV)
func (d *MinIOCSVDriver) Open(dsn string) (*sql.DB, error) {
	return nil, fmt.Errorf("MinIO CSV driver does not support standard database connections")
}

// ValidateDSN validates the connection string
func (d *MinIOCSVDriver) ValidateDSN(dsn string) error {
	bucket, _, err := ParseS3URI(dsn) // Reuse S3 URI parser
	if err != nil {
		return fmt.Errorf("invalid MinIO URI: %w", err)
	}

	if bucket == "" {
		return fmt.Errorf("bucket is required in MinIO URI")
	}

	return nil
}

// GetDefaultPort returns the default MinIO port
func (d *MinIOCSVDriver) GetDefaultPort() int {
	return 9000
}

// BuildDSN builds a connection string from configuration
func (d *MinIOCSVDriver) BuildDSN(config *model.DataSourceConfig) string {
	return fmt.Sprintf("minio://%s/%s", config.Database, config.Host)
}

// GetDatabaseTypeName returns the database type name
func (d *MinIOCSVDriver) GetDatabaseTypeName() string {
	return "minio-csv"
}

// TestConnection tests if the connection is working
func (d *MinIOCSVDriver) TestConnection(db *sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead")
}

// TestConnectionContext tests the MinIO connection
func (d *MinIOCSVDriver) TestConnectionContext(ctx context.Context) error {
	_, err := d.minioClient.BucketExists(ctx, d.minioClient.bucket)
	if err != nil {
		return fmt.Errorf("failed to check MinIO bucket: %w", err)
	}
	return nil
}

// GetDriverName returns the driver name
func (d *MinIOCSVDriver) GetDriverName() string {
	return "minio-csv"
}

// GetCategory returns the driver category
func (d *MinIOCSVDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

// GetCapabilities returns driver capabilities
func (d *MinIOCSVDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             false,
		SupportsTransaction:     false,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       true,
	}
}

// ConfigureAuth configures authentication
func (d *MinIOCSVDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// =============================================================================
// MinIO CSV-Specific Methods
// =============================================================================

// Query executes a query against CSV files on MinIO
func (d *MinIOCSVDriver) Query(ctx context.Context, query *MinIOCSVQuery) (*MinIOCSVResult, error) {
	if query.Key == "" {
		return nil, fmt.Errorf("CSV file key is required")
	}

	// Download CSV file
	data, err := d.minioClient.GetObject(ctx, query.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to get CSV file: %w", err)
	}

	reader := csv.NewReader(bytes.NewReader(stripUTF8BOM(data)))
	reader.Comma = d.config.Delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	// Skip header rows
	for i := 0; i < d.config.SkipHeaderRows; i++ {
		_, err := reader.Read()
		if err != nil {
			return nil, fmt.Errorf("failed to skip header row: %w", err)
		}
	}

	// Read header if present
	var headerRow []string
	var err2 error

	if d.config.HasHeader {
		headerRow, err2 = reader.Read()
		if err2 != nil {
			return nil, fmt.Errorf("failed to read header row: %w", err2)
		}
	}

	// Reuse data records after reading the header into its own slice.
	reader.ReuseRecord = true

	// Read all rows
	var rows []map[string]interface{}
	rowCount := 0

	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read row: %w", err)
		}

		// Create map for row
		rowMap := make(map[string]interface{})

		if len(headerRow) > 0 {
			// Use header as column names
			for i, val := range row {
				if i < len(headerRow) {
					colName := headerRow[i]
					rowMap[colName] = d.parseValue(val)
				}
			}
		} else {
			// Use column indices as names
			for i, val := range row {
				colName := fmt.Sprintf("column_%d", i+1)
				rowMap[colName] = d.parseValue(val)
			}
		}

		rows = append(rows, rowMap)
		rowCount++
	}

	// Apply filters
	if len(query.Filters) > 0 {
		rows = d.filterRows(rows, query.Filters)
	}

	// Apply offset
	if query.Offset > 0 {
		if query.Offset >= len(rows) {
			rows = []map[string]interface{}{}
		} else {
			rows = rows[query.Offset:]
		}
	}

	// Apply limit
	if query.Limit > 0 && query.Limit < len(rows) {
		rows = rows[:query.Limit]
	}

	return &MinIOCSVResult{
		Rows:      rows,
		Columns:   headerRow,
		NumRows:   int64(len(rows)),
		BytesRead: int64(len(data)),
	}, nil
}

// MinIOCSVQuery represents a query against MinIO CSV files
type MinIOCSVQuery struct {
	Key     string
	Columns []string
	Filters map[string]interface{}
	Limit   int
	Offset  int
}

// MinIOCSVResult represents query results
type MinIOCSVResult struct {
	Rows      []map[string]interface{}
	Columns   []string
	NumRows   int64
	BytesRead int64
}

// parseValue parses a CSV value and detects its type
func (d *MinIOCSVDriver) parseValue(value string) interface{} {
	value = strings.TrimSpace(value)

	// Check for null
	for _, nullVal := range d.config.NullValues {
		if value == nullVal {
			return nil
		}
	}

	// Try integer
	if intVal, err := strconv.ParseInt(value, 10, 64); err == nil {
		return intVal
	}

	// Try float
	if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
		return floatVal
	}

	// Try boolean
	if strings.ToLower(value) == "true" {
		return true
	}
	if strings.ToLower(value) == "false" {
		return false
	}

	// Default to string
	return value
}

// filterRows applies filters to rows
func (d *MinIOCSVDriver) filterRows(rows []map[string]interface{}, filters map[string]interface{}) []map[string]interface{} {
	filtered := make([]map[string]interface{}, 0, len(rows))

	for _, row := range rows {
		if d.matchesFilters(row, filters) {
			filtered = append(filtered, row)
		}
	}

	return filtered
}

// matchesFilters checks if a row matches all filters
func (d *MinIOCSVDriver) matchesFilters(row map[string]interface{}, filters map[string]interface{}) bool {
	for col, expectedVal := range filters {
		actualVal, exists := row[col]
		if !exists {
			return false
		}

		// Type-aware comparison
		if !d.compareValues(actualVal, expectedVal) {
			return false
		}
	}
	return true
}

// compareValues compares two values
func (d *MinIOCSVDriver) compareValues(a, b interface{}) bool {
	// Handle type conversions
	switch aVal := a.(type) {
	case int64:
		switch bVal := b.(type) {
		case int64:
			return aVal == bVal
		case float64:
			return float64(aVal) == bVal
		case int:
			return aVal == int64(bVal)
		}
	case float64:
		switch bVal := b.(type) {
		case int64:
			return aVal == float64(bVal)
		case float64:
			return aVal == bVal
		case int:
			return aVal == float64(bVal)
		}
	case string:
		if bStr, ok := b.(string); ok {
			return aVal == bStr
		}
	case bool:
		if bBool, ok := b.(bool); ok {
			return aVal == bBool
		}
	}

	// Default to string comparison
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// DetectSchema detects schema from a CSV file
func (d *MinIOCSVDriver) DetectSchema(ctx context.Context, key string) (*DetectedSchema, error) {
	data, err := d.minioClient.GetObject(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get CSV file: %w", err)
	}

	return d.schemaDetector.DetectSchema(data)
}

// ListCSVFiles lists CSV files in a prefix
func (d *MinIOCSVDriver) ListCSVFiles(ctx context.Context, prefix string) ([]MinIOObject, error) {
	return d.minioClient.ListFilesByExtension(ctx, prefix, ".csv")
}

// GetFileMetadata retrieves metadata for a CSV file
func (d *MinIOCSVDriver) GetFileMetadata(ctx context.Context, key string) (*CSVFileMetadataMinIO, error) {
	objMetadata, err := d.minioClient.GetObjectMetadata(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get object metadata: %w", err)
	}

	schema, err := d.DetectSchema(ctx, key)
	if err != nil {
		return nil, err
	}

	return &CSVFileMetadataMinIO{
		Key:          key,
		Size:         objMetadata.ContentLength,
		LastModified: objMetadata.LastModified,
		ColumnCount:  len(schema.Columns),
		Delimiter:    schema.Delimiter,
		HasHeader:    schema.HasHeader,
		Columns:      schema.Columns,
	}, nil
}

// CSVFileMetadataMinIO represents CSV file metadata from MinIO
type CSVFileMetadataMinIO struct {
	Key          string
	Size         int64
	LastModified time.Time
	ColumnCount  int
	Delimiter    rune
	HasHeader    bool
	Columns      []DetectedColumn
}

// StreamQuery streams query results using callback
func (d *MinIOCSVDriver) StreamQuery(ctx context.Context, key string, callback func(map[string]interface{}) error) error {
	data, err := d.minioClient.GetObject(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to get CSV file: %w", err)
	}

	reader := csv.NewReader(bytes.NewReader(stripUTF8BOM(data)))
	reader.Comma = d.config.Delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	// Skip header rows
	for i := 0; i < d.config.SkipHeaderRows; i++ {
		_, err := reader.Read()
		if err != nil {
			return fmt.Errorf("failed to skip header row: %w", err)
		}
	}

	// Read header
	var headerRow []string
	if d.config.HasHeader {
		headerRow, err = reader.Read()
		if err != nil {
			return fmt.Errorf("failed to read header row: %w", err)
		}
	}

	reader.ReuseRecord = true

	// Stream rows
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read row: %w", err)
		}

		rowMap := make(map[string]interface{})

		if len(headerRow) > 0 {
			for i, val := range row {
				if i < len(headerRow) {
					rowMap[headerRow[i]] = d.parseValue(val)
				}
			}
		} else {
			for i, val := range row {
				rowMap[fmt.Sprintf("column_%d", i+1)] = d.parseValue(val)
			}
		}

		if err := callback(rowMap); err != nil {
			return err
		}
	}

	return nil
}

// ConvertToStandardSchema converts detected CSV schema to standard schema
func (d *MinIOCSVDriver) ConvertToStandardSchema(csvSchema *DetectedSchema) model.TableSchema {
	stdSchema := model.TableSchema{
		Columns: make([]model.ColumnInfo, 0, len(csvSchema.Columns)),
	}

	for _, col := range csvSchema.Columns {
		stdCol := model.ColumnInfo{
			Name:     col.Name,
			Type:     mapCSVTypeToStandard(col.Type),
			Nullable: col.Nullable,
		}
		stdSchema.Columns = append(stdSchema.Columns, stdCol)
	}

	return stdSchema
}

// ApplyBatchPagination adds pagination to SQL query
func (d *MinIOCSVDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	// For MinIO CSV files, pagination is typically not supported in the same way as traditional databases
	// We return the original SQL as-is since CSV files don't support LIMIT/OFFSET in the same way
	// The pagination is usually handled at the application level
	return sql, nil
}
