package object_storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"io"
	"strings"
)

// S3JSONDriver implements Driver interface for querying JSON files on S3
type S3JSONDriver struct {
	s3Client      *S3Client
	jsonParser    *JSONParser
	selectHandler *S3SelectHandler
	config        *S3JSONDriverConfig
}

// S3JSONDriverConfig holds S3 JSON driver configuration
type S3JSONDriverConfig struct {
	S3Config        *S3Config
	EnableSelectAPI bool
	FlattenNested   bool
	DetectTypes     bool
}

// NewS3JSONDriver creates a new S3 JSON driver
func NewS3JSONDriver(ctx context.Context, config *S3JSONDriverConfig) (*S3JSONDriver, error) {
	if config == nil || config.S3Config == nil {
		return nil, fmt.Errorf("S3 JSON configuration is required")
	}
	s3Client, err := NewS3Client(ctx, config.S3Config)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	parserConfig := &JSONParserConfig{
		DetectTypes:   config.DetectTypes,
		FlattenNested: config.FlattenNested,
		Separator:     "_",
	}

	jsonParser := NewJSONParser(parserConfig)
	selectHandler := NewS3SelectHandler(s3Client)

	return &S3JSONDriver{
		s3Client:      s3Client,
		jsonParser:    jsonParser,
		selectHandler: selectHandler,
		config:        config,
	}, nil
}

// Open opens a connection (not applicable for S3 JSON)
func (d *S3JSONDriver) Open(dsn string) (*sql.DB, error) {
	return nil, fmt.Errorf("S3 JSON driver does not support standard database connections")
}

// ValidateDSN validates the connection string
func (d *S3JSONDriver) ValidateDSN(dsn string) error {
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
func (d *S3JSONDriver) GetDefaultPort() int {
	return 443 // HTTPS
}

// BuildDSN builds a connection string from configuration
func (d *S3JSONDriver) BuildDSN(config *model.DataSourceConfig) string {
	return fmt.Sprintf("s3://%s/%s", config.Database, config.Host)
}

// GetDatabaseTypeName returns the database type name
func (d *S3JSONDriver) GetDatabaseTypeName() string {
	return "s3-json"
}

// TestConnection tests if the connection is working
func (d *S3JSONDriver) TestConnection(db *sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead")
}

// TestConnectionContext tests the S3 connection
func (d *S3JSONDriver) TestConnectionContext(ctx context.Context) error {
	_, err := d.s3Client.ListObjects(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("failed to list S3 objects: %w", err)
	}
	return nil
}

// GetDriverName returns the driver name
func (d *S3JSONDriver) GetDriverName() string {
	return "s3-json"
}

// GetCategory returns the driver category
func (d *S3JSONDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

// GetCapabilities returns driver capabilities
func (d *S3JSONDriver) GetCapabilities() drivers.DriverCapabilities {
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
func (d *S3JSONDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// ApplyBatchPagination applies pagination to the read-only SQL adapter.
func (d *S3JSONDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}

// =============================================================================
// S3 JSON-Specific Methods
// =============================================================================

// Query executes a query against JSON files
func (d *S3JSONDriver) Query(ctx context.Context, query *S3JSONQuery) (*S3JSONResult, error) {
	if d == nil || d.s3Client == nil || d.jsonParser == nil {
		return nil, fmt.Errorf("S3 JSON driver is not initialized")
	}
	if query == nil || strings.TrimSpace(query.Key) == "" {
		return nil, fmt.Errorf("JSON file key is required")
	}

	// Use S3 Select if enabled and applicable
	if d.config.EnableSelectAPI && query.UseSelectAPI {
		return d.queryWithSelect(ctx, query)
	}

	// Otherwise use JSON parser
	return d.queryWithParser(ctx, query)
}

// S3JSONQuery represents a query against S3 JSON files
type S3JSONQuery struct {
	Key           string
	Filters       map[string]interface{}
	Fields        []string // Specific fields to extract
	Limit         int
	Offset        int
	FlattenNested bool
	UseSelectAPI  bool
	SelectSQL     string
}

// S3JSONResult represents query results
type S3JSONResult struct {
	Rows      []map[string]interface{}
	Schema    *JSONSchema
	Fields    []string
	BytesRead int64
	NumRows   int64
}

// queryWithSelect uses S3 Select API
func (d *S3JSONDriver) queryWithSelect(ctx context.Context, query *S3JSONQuery) (*S3JSONResult, error) {
	sql := query.SelectSQL
	if sql == "" {
		sql = fmt.Sprintf("SELECT * FROM s3object%s", buildWhereClause(query.Filters))
	}

	// Determine JSON type
	_, err := d.s3Client.GetObjectMetadata(ctx, query.Key)
	jsonType := "LINES"
	if strings.HasSuffix(query.Key, ".jsonl") || strings.HasSuffix(query.Key, ".ndjson") {
		jsonType = "LINES"
	} else {
		// Try to detect format
		data, err := d.s3Client.GetObject(ctx, query.Key)
		if err == nil {
			detectedType, _ := d.jsonParser.GetJSONType(data)
			if detectedType == "jsonlines" {
				jsonType = "LINES"
			} else if detectedType == "array" {
				jsonType = "DOCUMENT"
			}
		}
	}

	result, err := d.selectHandler.QueryJSON(ctx, query.Key, sql, jsonType)
	if err != nil {
		return nil, fmt.Errorf("S3 Select query failed: %w", err)
	}

	return &S3JSONResult{
		Rows:      result.Records,
		NumRows:   int64(len(result.Records)),
		BytesRead: result.BytesProcessed,
	}, nil
}

// queryWithParser uses JSON parser
func (d *S3JSONDriver) queryWithParser(ctx context.Context, query *S3JSONQuery) (*S3JSONResult, error) {
	// Download JSON file
	data, err := d.s3Client.GetObject(ctx, query.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to get JSON file: %w", err)
	}

	// Detect JSON format
	parsed, _, err := d.jsonParser.ParseJSONDataContext(ctx, data)
	if err != nil {
		return nil, fmt.Errorf("failed to detect or parse JSON format: %w", err)
	}

	// Apply field selection
	if len(query.Fields) > 0 {
		parsed.Data = d.selectFields(parsed.Data, query.Fields)
	}

	// Apply filters
	if len(query.Filters) > 0 {
		parsed.Data = d.filterRows(parsed.Data, query.Filters)
	}

	// Apply offset
	if query.Offset > 0 {
		if query.Offset >= len(parsed.Data) {
			parsed.Data = []map[string]interface{}{}
		} else {
			parsed.Data = parsed.Data[query.Offset:]
		}
	}

	// Apply limit
	if query.Limit > 0 && query.Limit < len(parsed.Data) {
		parsed.Data = parsed.Data[:query.Limit]
	}

	// Get all fields
	fields := d.jsonParser.MergeFields(parsed.Data)

	return &S3JSONResult{
		Rows:      parsed.Data,
		Schema:    &parsed.Schema,
		Fields:    fields,
		NumRows:   int64(len(parsed.Data)),
		BytesRead: int64(len(data)),
	}, nil
}

// selectFields selects specific fields from rows
func (d *S3JSONDriver) selectFields(rows []map[string]interface{}, fields []string) []map[string]interface{} {
	result := make([]map[string]interface{}, len(rows))

	for i, row := range rows {
		newRow := make(map[string]interface{})
		for _, field := range fields {
			if value, exists := row[field]; exists {
				newRow[field] = value
			}
		}
		result[i] = newRow
	}

	return result
}

// filterRows applies filters to rows
func (d *S3JSONDriver) filterRows(rows []map[string]interface{}, filters map[string]interface{}) []map[string]interface{} {
	filtered := make([]map[string]interface{}, 0, len(rows))

	for _, row := range rows {
		if d.jsonParser.matchesFilters(row, filters) {
			filtered = append(filtered, row)
		}
	}

	return filtered
}

// DetectSchema detects schema from a JSON file
func (d *S3JSONDriver) DetectSchema(ctx context.Context, key string) (*JSONSchema, error) {
	data, err := d.s3Client.GetObject(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get JSON file: %w", err)
	}

	return d.jsonParser.DetectSchemaContext(ctx, data)
}

// ListJSONFiles lists JSON files in a prefix
func (d *S3JSONDriver) ListJSONFiles(ctx context.Context, prefix string) ([]S3Object, error) {
	// List files with .json, .jsonl, .ndjson extensions
	extensions := []string{".json", ".jsonl", ".ndjson"}
	return d.s3Client.ListFilesByExtensions(ctx, prefix, extensions)
}

// GetFileMetadata retrieves metadata for a JSON file
func (d *S3JSONDriver) GetFileMetadata(ctx context.Context, key string) (*JSONFileMetadata, error) {
	objMetadata, err := d.s3Client.GetObjectMetadata(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get object metadata: %w", err)
	}

	data, err := d.s3Client.GetObject(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get JSON file: %w", err)
	}

	parsed, jsonType, err := d.jsonParser.ParseJSONDataContext(ctx, data)
	if err != nil {
		return nil, err
	}
	return &JSONFileMetadata{
		Key: key, Size: objMetadata.ContentLength, Format: jsonType,
		IsArray: parsed.Schema.IsArray, FieldCount: len(parsed.Schema.Fields),
		Fields: parsed.Schema.Fields,
	}, nil
}

// JSONFileMetadata represents JSON file metadata
type JSONFileMetadata struct {
	Key        string
	Size       int64
	Format     string
	IsArray    bool
	FieldCount int
	Fields     []JSONField
}

// StreamQuery streams query results using callback
func (d *S3JSONDriver) StreamQuery(ctx context.Context, key string, callback func(map[string]interface{}) error) error {
	reader, err := d.s3Client.GetObjectReader(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to get JSON file: %w", err)
	}
	defer reader.Close()

	// Stream JSON Lines format
	decoder := json.NewDecoder(reader)

	for {
		var record map[string]interface{}
		err := decoder.Decode(&record)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to decode JSON: %w", err)
		}

		if d.config.FlattenNested {
			record = d.jsonParser.flattenRecord(record, "")
		}

		if err := callback(record); err != nil {
			return err
		}
	}

	return nil
}

// QueryNested queries nested JSON structures
func (d *S3JSONDriver) QueryNested(ctx context.Context, key string, path string) ([]interface{}, error) {
	data, err := d.s3Client.GetObject(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get JSON file: %w", err)
	}

	var document interface{}
	err = json.Unmarshal(data, &document)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Extract values at path
	results := make([]interface{}, 0)
	d.extractValuesAtPath(document, path, &results)

	return results, nil
}

// extractValuesAtPath recursively extracts values at a path
func (d *S3JSONDriver) extractValuesAtPath(value interface{}, path string, results *[]interface{}) {
	if path == "" {
		*results = append(*results, value)
		return
	}

	parts := strings.Split(path, ".")
	current := parts[0]
	remaining := ""
	if len(parts) > 1 {
		// Join the remaining parts back together with dots
		remaining = strings.Join(parts[1:], ".")
	}

	switch val := value.(type) {
	case map[string]interface{}:
		child, exists := val[current]
		if exists {
			if remaining == "" {
				*results = append(*results, child)
			} else {
				d.extractValuesAtPath(child, remaining, results)
			}
		}
	case []interface{}:
		for _, item := range val {
			d.extractValuesAtPath(item, path, results)
		}
	}
}

// GetNestedValue retrieves a nested value using dot notation
func (d *S3JSONDriver) GetNestedValue(ctx context.Context, key, path string) (interface{}, error) {
	data, err := d.s3Client.GetObject(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get JSON file: %w", err)
	}

	var document map[string]interface{}
	err = json.Unmarshal(data, &document)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	value, exists := d.jsonParser.GetNestedValue(document, path)
	if !exists {
		return nil, fmt.Errorf("path not found: %s", path)
	}

	return value, nil
}

// ExtractPaths extracts all paths from a JSON file
func (d *S3JSONDriver) ExtractPaths(ctx context.Context, key string) ([]string, error) {
	data, err := d.s3Client.GetObject(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get JSON file: %w", err)
	}

	return d.jsonParser.ExtractPaths(data)
}

// ConvertToStandardSchema converts JSON schema to standard schema
func (d *S3JSONDriver) ConvertToStandardSchema(jsonSchema *JSONSchema) model.TableSchema {
	stdSchema := model.TableSchema{
		Columns: make([]model.ColumnInfo, 0, len(jsonSchema.Fields)),
	}

	for _, field := range jsonSchema.Fields {
		stdCol := model.ColumnInfo{
			Name:     field.Name,
			Type:     mapJSONTypeToStandard(field.Type),
			Nullable: field.Nullable,
		}

		// Note: model.ColumnInfo does not have a NestedPath field
		// If field.Path is significant, we would need to handle it differently
		// depending on the specific requirements

		stdSchema.Columns = append(stdSchema.Columns, stdCol)
	}

	return stdSchema
}

// mapJSONTypeToStandard maps JSON type to standard type
func mapJSONTypeToStandard(jsonType string) model.StandardizedType {
	switch jsonType {
	case "boolean":
		return model.StandardizedTypeBoolean
	case "integer":
		return model.StandardizedTypeInt64
	case "float", "double":
		return model.StandardizedTypeFloat64
	case "string":
		return model.StandardizedTypeString
	case "array":
		return model.StandardizedTypeArray
	case "object":
		return model.StandardizedTypeStruct
	default:
		if strings.HasPrefix(jsonType, "array<") {
			return model.StandardizedTypeArray
		}
		return model.StandardizedTypeString
	}
}

// buildWhereClause builds SQL WHERE clause from filters
func buildWhereClause(filters map[string]interface{}) string {
	if len(filters) == 0 {
		return ""
	}

	conditions := make([]string, 0, len(filters))
	for key, value := range filters {
		switch v := value.(type) {
		case string:
			conditions = append(conditions, fmt.Sprintf("%s = '%s'", key, v))
		case int, int64, float64:
			conditions = append(conditions, fmt.Sprintf("%s = %v", key, v))
		case bool:
			conditions = append(conditions, fmt.Sprintf("%s = %t", key, v))
		default:
			conditions = append(conditions, fmt.Sprintf("%s = '%v'", key, v))
		}
	}

	return " WHERE " + strings.Join(conditions, " AND ")
}
