package object_storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"io"
	"strconv"
	"strings"
)

// S3CSVDriver implements Driver interface for querying CSV files on S3
type S3CSVDriver struct {
	s3Client        *S3Client
	selectHandler   *S3SelectHandler
	schemaDetector  *CSVSchemadDetector
	config          *S3CSVDriverConfig
	objectKey       string
	tableName       string
	driverName      string
	readObjectFunc  func(context.Context, string) ([]byte, error)
	pingObjectFunc  func(context.Context, string) error
	closeObjectFunc func() error
	maxRows         int
}

// S3CSVDriverConfig holds S3 CSV driver configuration
type S3CSVDriverConfig struct {
	S3Config        *S3Config
	Delimiter       rune
	HasHeader       bool
	EnableSelectAPI bool
	QuoteChar       rune
	SkipHeaderRows  int
	NullValues      []string
}

// NewS3CSVDriver creates a new S3 CSV driver
func NewS3CSVDriver(ctx context.Context, config *S3CSVDriverConfig) (*S3CSVDriver, error) {
	s3Client, err := NewS3Client(ctx, config.S3Config)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	selectHandler := NewS3SelectHandler(s3Client)

	detectionConfig := &CSVDetectionConfig{
		SampleSize:       1000,
		Delimiter:        config.Delimiter,
		HasHeader:        config.HasHeader,
		QuoteChar:        config.QuoteChar,
		HeaderRowsToSkip: config.SkipHeaderRows,
		NullValues:       config.NullValues,
	}

	schemaDetector := NewCSVSchemadDetector(detectionConfig)

	return &S3CSVDriver{
		s3Client:       s3Client,
		selectHandler:  selectHandler,
		schemaDetector: schemaDetector,
		config:         config,
	}, nil
}

// Open opens a connection (not applicable for S3 CSV)
func (d *S3CSVDriver) Open(dsn string) (*sql.DB, error) {
	return nil, fmt.Errorf("S3 CSV driver does not support standard database connections")
}

// ValidateDSN validates the connection string
func (d *S3CSVDriver) ValidateDSN(dsn string) error {
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
func (d *S3CSVDriver) GetDefaultPort() int {
	return 443 // HTTPS
}

// BuildDSN builds a connection string from configuration
func (d *S3CSVDriver) BuildDSN(config *model.DataSourceConfig) string {
	return fmt.Sprintf("s3://%s/%s", config.Database, config.Host)
}

// GetDatabaseTypeName returns the database type name
func (d *S3CSVDriver) GetDatabaseTypeName() string {
	return "s3-csv"
}

// TestConnection tests if the connection is working
func (d *S3CSVDriver) TestConnection(db *sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead")
}

// TestConnectionContext tests the S3 connection
func (d *S3CSVDriver) TestConnectionContext(ctx context.Context) error {
	_, err := d.s3Client.ListObjects(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("failed to list S3 objects: %w", err)
	}
	return nil
}

// GetDriverName returns the driver name
func (d *S3CSVDriver) GetDriverName() string {
	if d.driverName != "" {
		return d.driverName
	}
	return "s3-csv"
}

// GetCategory returns the driver category
func (d *S3CSVDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

// GetCapabilities returns driver capabilities
func (d *S3CSVDriver) GetCapabilities() drivers.DriverCapabilities {
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
func (d *S3CSVDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// ApplyBatchPagination applies pagination to a SQL query
func (d *S3CSVDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	if batchSize <= 0 {
		return "", fmt.Errorf("batch size must be positive")
	}
	query := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(sql), ";"))
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	// The outer query pages the complete result set, preserving an optional
	// caller LIMIT/OFFSET inside the derived table.
	return fmt.Sprintf("SELECT * FROM (%s) AS __data_fabric_page LIMIT %d OFFSET %d", query, batchSize, maxInt64(offset, 0)), nil
}

func maxInt64(value, minimum int64) int64 {
	if value < minimum {
		return minimum
	}
	return value
}

// =============================================================================
// S3 CSV-Specific Methods
// =============================================================================

// Query executes a query against CSV files
func (d *S3CSVDriver) Query(ctx context.Context, query *S3CSVQuery) (*S3CSVResult, error) {
	if query.Key == "" {
		return nil, fmt.Errorf("CSV file key is required")
	}

	// Use S3 Select if enabled
	if d.config.EnableSelectAPI && query.UseSelectAPI {
		return d.queryWithSelect(ctx, query)
	}

	// Otherwise use CSV reader
	return d.queryWithReader(ctx, query)
}

// S3CSVQuery represents a query against S3 CSV files
type S3CSVQuery struct {
	Key          string
	Columns      []string
	Filters      map[string]interface{}
	Limit        int
	Offset       int
	OrderBy      string
	Ascending    bool
	UseSelectAPI bool
	SelectSQL    string
}

// S3CSVResult represents query results
type S3CSVResult struct {
	Rows      []map[string]interface{}
	Columns   []string
	Schema    *DetectedSchema
	BytesRead int64
	NumRows   int64
}

// queryWithSelect uses S3 Select API
func (d *S3CSVDriver) queryWithSelect(ctx context.Context, query *S3CSVQuery) (*S3CSVResult, error) {
	sql := query.SelectSQL
	if sql == "" {
		sql = BuildCSVQuery(query.Key, "", query.Columns)
	}

	result, err := d.selectHandler.QueryCSV(ctx, query.Key, sql, d.config.HasHeader, string(d.config.Delimiter))
	if err != nil {
		return nil, fmt.Errorf("S3 Select query failed: %w", err)
	}

	return &S3CSVResult{
		Rows:      result.Records,
		NumRows:   int64(len(result.Records)),
		BytesRead: result.BytesProcessed,
	}, nil
}

// queryWithReader uses CSV reader
func (d *S3CSVDriver) queryWithReader(ctx context.Context, query *S3CSVQuery) (*S3CSVResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Download CSV file
	data, err := d.readObject(ctx, query.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to get CSV file: %w", err)
	}

	reader := csv.NewReader(bytes.NewReader(stripUTF8BOM(data)))
	reader.Comma = d.config.Delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.ReuseRecord = false

	// Skip header rows
	for i := 0; i < d.config.SkipHeaderRows; i++ {
		_, err := reader.Read()
		if err != nil {
			return nil, fmt.Errorf("failed to skip header row: %w", err)
		}
	}

	// Read header if present
	var headerRow []string
	if d.config.HasHeader {
		headerRow, err = reader.Read()
		if err != nil {
			return nil, fmt.Errorf("failed to read header row: %w", err)
		}
	}

	// Read all rows
	var rows []map[string]interface{}
	rowCount := 0

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read row: %w", err)
		}

		if d.maxRows > 0 && rowCount >= d.maxRows {
			return nil, fmt.Errorf("CSV object exceeds configured row limit of %d", d.maxRows)
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
	if len(headerRow) == 0 && len(rows) > 0 {
		for index := 1; ; index++ {
			column := fmt.Sprintf("column_%d", index)
			if _, exists := rows[0][column]; !exists {
				break
			}
			headerRow = append(headerRow, column)
		}
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

	// Apply sorting
	if query.OrderBy != "" {
		rows = d.sortRows(rows, query.OrderBy, query.Ascending)
	}

	return &S3CSVResult{
		Rows:      rows,
		Columns:   headerRow,
		NumRows:   int64(len(rows)),
		BytesRead: int64(len(data)),
	}, nil
}

// parseValue parses a CSV value and detects its type
func (d *S3CSVDriver) parseValue(value string) interface{} {
	value = d.TrimSpace(value)

	// Check for null
	if d.isNull(value) {
		return nil
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

// TrimSpace trims whitespace from a value
func (d *S3CSVDriver) TrimSpace(value string) string {
	return strings.TrimSpace(value)
}

// isNull checks if a value should be treated as null
func (d *S3CSVDriver) isNull(value string) bool {
	for _, nullVal := range d.config.NullValues {
		if value == nullVal {
			return true
		}
	}
	return false
}

// filterRows applies filters to rows
func (d *S3CSVDriver) filterRows(rows []map[string]interface{}, filters map[string]interface{}) []map[string]interface{} {
	filtered := make([]map[string]interface{}, 0, len(rows))

	for _, row := range rows {
		if d.matchesFilters(row, filters) {
			filtered = append(filtered, row)
		}
	}

	return filtered
}

// matchesFilters checks if a row matches all filters
func (d *S3CSVDriver) matchesFilters(row map[string]interface{}, filters map[string]interface{}) bool {
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
func (d *S3CSVDriver) compareValues(a, b interface{}) bool {
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

// sortRows sorts rows by column
func (d *S3CSVDriver) sortRows(rows []map[string]interface{}, column string, ascending bool) []map[string]interface{} {
	// Simple bubble sort (for small datasets)
	// Would use more efficient sorting for large datasets
	sorted := make([]map[string]interface{}, len(rows))
	copy(sorted, rows)

	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			val1 := sorted[i][column]
			val2 := sorted[j][column]

			if d.compareValuesForSort(val1, val2, ascending) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted
}

// compareValuesForSort compares two values for sorting
func (d *S3CSVDriver) compareValuesForSort(a, b interface{}, ascending bool) bool {
	if ascending {
		return d.lessThan(b, a)
	}
	return d.lessThan(a, b)
}

// lessThan compares two values
func (d *S3CSVDriver) lessThan(a, b interface{}) bool {
	// Try numeric comparison
	switch aVal := a.(type) {
	case int64:
		switch bVal := b.(type) {
		case int64:
			return aVal < bVal
		case float64:
			return float64(aVal) < bVal
		}
	case float64:
		switch bVal := b.(type) {
		case int64:
			return aVal < float64(bVal)
		case float64:
			return aVal < bVal
		}
	}

	// Default to string comparison
	return fmt.Sprintf("%v", a) < fmt.Sprintf("%v", b)
}

func (d *S3CSVDriver) readObject(ctx context.Context, key string) ([]byte, error) {
	if d.readObjectFunc != nil {
		return d.readObjectFunc(ctx, key)
	}
	if d.s3Client == nil {
		return nil, fmt.Errorf("CSV object reader is not configured")
	}
	return d.s3Client.GetObject(ctx, key)
}

func (d *S3CSVDriver) pingConfiguredObject(ctx context.Context) error {
	if d.pingObjectFunc != nil {
		return d.pingObjectFunc(ctx, d.objectKey)
	}
	if d.s3Client == nil {
		return fmt.Errorf("CSV object health check is not configured")
	}
	_, err := d.s3Client.GetObjectMetadata(ctx, d.objectKey)
	return err
}

// DetectSchema detects schema from a CSV file
func (d *S3CSVDriver) DetectSchema(ctx context.Context, key string) (*DetectedSchema, error) {
	data, err := d.readObject(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get CSV file: %w", err)
	}

	return d.schemaDetector.DetectSchema(data)
}

// ListCSVFiles lists CSV files in a prefix
func (d *S3CSVDriver) ListCSVFiles(ctx context.Context, prefix string) ([]S3Object, error) {
	return d.s3Client.ListFilesByExtension(ctx, prefix, ".csv")
}

// GetFileMetadata retrieves metadata for a CSV file
func (d *S3CSVDriver) GetFileMetadata(ctx context.Context, key string) (*CSVFileMetadata, error) {
	objMetadata, err := d.s3Client.GetObjectMetadata(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get object metadata: %w", err)
	}

	schema, err := d.DetectSchema(ctx, key)
	if err != nil {
		return nil, err
	}

	return &CSVFileMetadata{
		Key:         key,
		Size:        objMetadata.ContentLength,
		RowCount:    schema.RowCount,
		ColumnCount: len(schema.Columns),
		Delimiter:   schema.Delimiter,
		HasHeader:   schema.HasHeader,
		Columns:     schema.Columns,
	}, nil
}

// CSVFileMetadata represents CSV file metadata
type CSVFileMetadata struct {
	Key         string
	Size        int64
	RowCount    int
	ColumnCount int
	Delimiter   rune
	HasHeader   bool
	Columns     []DetectedColumn
}

// StreamQuery streams query results using callback
func (d *S3CSVDriver) StreamQuery(ctx context.Context, key string, callback func(map[string]interface{}) error) error {
	data, err := d.s3Client.GetObject(ctx, key)
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

// GetColumnStatistics retrieves statistics for a column
func (d *S3CSVDriver) GetColumnStatistics(ctx context.Context, key, columnName string) (*ColumnStatistics, error) {
	// Get column index from schema
	schema, err := d.DetectSchema(ctx, key)
	if err != nil {
		return nil, err
	}

	colIdx := -1
	for i, col := range schema.Columns {
		if col.Name == columnName {
			colIdx = i
			break
		}
	}

	if colIdx == -1 {
		return nil, fmt.Errorf("column not found: %s", columnName)
	}

	data, err := d.s3Client.GetObject(ctx, key)
	if err != nil {
		return nil, err
	}

	return d.schemaDetector.AnalyzeStatistics(data, colIdx)
}

// ConvertToStandardSchema converts detected CSV schema to standard schema
func (d *S3CSVDriver) ConvertToStandardSchema(csvSchema *DetectedSchema) model.TableSchema {
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

// mapCSVTypeToStandard maps CSV type to standard type
func mapCSVTypeToStandard(csvType string) model.StandardizedType {
	switch csvType {
	case "integer":
		return model.StandardizedTypeInt64
	case "float":
		return model.StandardizedTypeFloat64
	case "boolean":
		return model.StandardizedTypeBoolean
	case "date":
		return model.StandardizedTypeDate
	case "datetime":
		return model.StandardizedTypeDateTime
	case "string":
		return model.StandardizedTypeString
	default:
		return model.StandardizedTypeString
	}
}
