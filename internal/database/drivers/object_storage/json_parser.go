package object_storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// JSONParser handles JSON file parsing with nested structure support
type JSONParser struct {
	config *JSONParserConfig
}

// JSONParserConfig holds JSON parser configuration
type JSONParserConfig struct {
	DetectTypes   bool   // Auto-detect types from values
	FlattenNested bool   // Flatten nested structures
	Separator     string // Separator for flattened keys (default: "_")
	MaxRecords    int    // Maximum records to decode; zero means unlimited
}

// NewJSONParser creates a new JSON parser
func NewJSONParser(config *JSONParserConfig) *JSONParser {
	if config == nil {
		config = &JSONParserConfig{
			DetectTypes: true, FlattenNested: false, Separator: "_",
		}
	} else {
		copy := *config
		if copy.Separator == "" {
			copy.Separator = "_"
		}
		config = &copy
	}
	return &JSONParser{config: config}
}

// JSONSchema represents detected JSON schema
type JSONSchema struct {
	Fields     []JSONField
	IsArray    bool
	ArrayDepth int
}

// JSONField represents a JSON field definition
type JSONField struct {
	Name             string
	Path             string // Dot-notation path for nested fields
	Type             string
	Nullable         bool
	NestedFields     []JSONField // For object types
	ArrayElementType *JSONField  // For array types
}

// ParsedJSON represents parsed JSON data with schema
type ParsedJSON struct {
	Data   []map[string]interface{}
	Schema JSONSchema
}

// ParseJSONLines parses JSON Lines format (newline-delimited JSON).
func (p *JSONParser) ParseJSONLines(data []byte) (*ParsedJSON, error) {
	return p.ParseJSONLinesContext(context.Background(), data)
}

// ParseJSONLinesContext parses JSON Lines and checks cancellation between records.
func (p *JSONParser) ParseJSONLinesContext(ctx context.Context, data []byte) (*ParsedJSON, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	records := make([]map[string]interface{}, 0)
	lineNumber, offset := 0, 0
	for offset < len(data) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lineNumber++
		lineEnd := bytes.IndexByte(data[offset:], '\n')
		var line []byte
		if lineEnd < 0 {
			line = data[offset:]
			offset = len(data)
		} else {
			line = data[offset : offset+lineEnd]
			offset += lineEnd + 1
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var value interface{}
		if err := decodeJSONValue(line, &value); err != nil {
			return nil, fmt.Errorf("failed to parse line %d: %w", lineNumber, err)
		}
		record, ok := value.(map[string]interface{})
		if !ok || record == nil {
			return nil, fmt.Errorf("JSON Lines record on line %d must be an object", lineNumber)
		}
		if p.config.FlattenNested {
			var err error
			record, err = p.flattenRecordChecked(record, "")
			if err != nil {
				return nil, fmt.Errorf("flatten JSON Lines record on line %d: %w", lineNumber, err)
			}
		}
		if p.config.MaxRecords > 0 && len(records) >= p.config.MaxRecords {
			return nil, fmt.Errorf("JSON Lines record limit exceeded: maximum %d", p.config.MaxRecords)
		}
		records = append(records, record)
	}
	schema := p.InferSchemaFromRecords(records)
	schema.IsArray = false
	return &ParsedJSON{Data: records, Schema: *schema}, nil
}

// ParseJSONArray parses an array of JSON objects.
func (p *JSONParser) ParseJSONArray(data []byte) (*ParsedJSON, error) {
	return p.ParseJSONArrayContext(context.Background(), data)
}

// ParseJSONArrayContext decodes an array one record at a time and checks
// cancellation between records.
func (p *JSONParser) ParseJSONArrayContext(ctx context.Context, data []byte) (*ParsedJSON, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	opening, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSON array: %w", err)
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '[' {
		return nil, fmt.Errorf("JSON root must be an array of objects")
	}
	records := make([]map[string]interface{}, 0)
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var value interface{}
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("failed to decode JSON array element %d: %w", len(records), err)
		}
		value, err = normalizeJSONNumbers(value)
		if err != nil {
			return nil, err
		}
		record, ok := value.(map[string]interface{})
		if !ok || record == nil {
			return nil, fmt.Errorf("JSON array element %d must be an object", len(records))
		}
		if p.config.FlattenNested {
			record, err = p.flattenRecordChecked(record, "")
			if err != nil {
				return nil, fmt.Errorf("flatten JSON array element %d: %w", len(records), err)
			}
		}
		if p.config.MaxRecords > 0 && len(records) >= p.config.MaxRecords {
			return nil, fmt.Errorf("JSON array record limit exceeded: maximum %d", p.config.MaxRecords)
		}
		records = append(records, record)
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to close JSON array: %w", err)
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != ']' {
		return nil, fmt.Errorf("JSON array is not terminated")
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("unexpected trailing JSON data: %w", err)
		}
		return nil, fmt.Errorf("multiple JSON values are not a single document")
	}
	schema := p.InferSchemaFromRecords(records)
	schema.IsArray = true
	return &ParsedJSON{Data: records, Schema: *schema}, nil
}

// ParseJSONDocument parses one JSON object.
func (p *JSONParser) ParseJSONDocument(data []byte) (*ParsedJSON, error) {
	return p.ParseJSONDocumentContext(context.Background(), data)
}

// ParseJSONDocumentContext parses one JSON object with cancellation checks.
func (p *JSONParser) ParseJSONDocumentContext(ctx context.Context, data []byte) (*ParsedJSON, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var value interface{}
	if err := decodeJSONValue(data, &value); err != nil {
		return nil, fmt.Errorf("failed to parse JSON document: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	document, ok := value.(map[string]interface{})
	if !ok || document == nil {
		return nil, fmt.Errorf("JSON document root must be an object")
	}
	if p.config.FlattenNested {
		var err error
		document, err = p.flattenRecordChecked(document, "")
		if err != nil {
			return nil, fmt.Errorf("flatten JSON object: %w", err)
		}
	}
	schema := p.InferSchemaFromRecords([]map[string]interface{}{document})
	schema.IsArray = false
	return &ParsedJSON{Data: []map[string]interface{}{document}, Schema: *schema}, nil
}

// ParseJSONData detects and parses one object, an object array, or JSON Lines.
func (p *JSONParser) ParseJSONData(data []byte) (*ParsedJSON, string, error) {
	return p.ParseJSONDataContext(context.Background(), data)
}

// ParseJSONDataContext detects and parses a supported document with
// cancellation checks between JSON Lines or array records.
func (p *JSONParser) ParseJSONDataContext(ctx context.Context, data []byte) (*ParsedJSON, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, "", fmt.Errorf("JSON document is empty")
	}
	if trimmed[0] == '[' {
		parsed, err := p.ParseJSONArrayContext(ctx, trimmed)
		return parsed, "array", err
	}
	if trimmed[0] != '{' {
		return nil, "", fmt.Errorf("JSON root must be an object or array")
	}
	if parsed, err := p.ParseJSONDocumentContext(ctx, trimmed); err == nil {
		return parsed, "object", nil
	} else if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, "", ctxErr
	}
	parsed, err := p.ParseJSONLinesContext(ctx, trimmed)
	if err != nil {
		return nil, "", fmt.Errorf("input is neither one JSON object nor JSON Lines: %w", err)
	}
	return parsed, "jsonlines", nil
}

// decodeJSONValue decodes one JSON value, preserves integer precision within
// int64, and rejects trailing documents.
func decodeJSONValue(data []byte, target interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return fmt.Errorf("unexpected trailing JSON data: %w", err)
		}
		return fmt.Errorf("multiple JSON values are not a single document")
	}
	normalized, err := normalizeJSONNumbers(unwrapJSONTarget(target))
	if err != nil {
		return err
	}
	if pointer, ok := target.(*interface{}); ok {
		*pointer = normalized
	}
	return nil
}

func unwrapJSONTarget(value interface{}) interface{} {
	switch pointer := value.(type) {
	case *interface{}:
		return *pointer
	case *[]interface{}:
		return *pointer
	default:
		return value
	}
}

func normalizeJSONNumbers(value interface{}) (interface{}, error) {
	switch current := value.(type) {
	case json.Number:
		if integer, err := current.Int64(); err == nil {
			return integer, nil
		}
		if !strings.ContainsAny(current.String(), ".eE") {
			return nil, fmt.Errorf("JSON integer %q is outside the supported int64 range", current)
		}
		floating, err := current.Float64()
		if err != nil {
			return nil, fmt.Errorf("JSON number %q is outside the supported numeric range: %w", current, err)
		}
		return floating, nil
	case []interface{}:
		for i := range current {
			normalized, err := normalizeJSONNumbers(current[i])
			if err != nil {
				return nil, err
			}
			current[i] = normalized
		}
		return current, nil
	case map[string]interface{}:
		for key, item := range current {
			normalized, err := normalizeJSONNumbers(item)
			if err != nil {
				return nil, err
			}
			current[key] = normalized
		}
		return current, nil
	default:
		return value, nil
	}
}

// DetectSchema detects schema from one JSON object, object array, or JSON Lines document.
func (p *JSONParser) DetectSchema(data []byte) (*JSONSchema, error) {
	return p.DetectSchemaContext(context.Background(), data)
}

// DetectSchemaContext detects schema with cancellation checks while reading records.
func (p *JSONParser) DetectSchemaContext(ctx context.Context, data []byte) (*JSONSchema, error) {
	parsed, _, err := p.ParseJSONDataContext(ctx, data)
	if err != nil {
		return nil, err
	}
	return &parsed.Schema, nil
}

func (p *JSONParser) flattenRecordChecked(record map[string]interface{}, prefix string) (map[string]interface{}, error) {
	flattened := make(map[string]interface{})
	keys := make([]string, 0, len(record))
	for key := range record {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		name := key
		if prefix != "" {
			name = prefix + p.config.Separator + key
		}
		value := record[key]
		if nested, ok := value.(map[string]interface{}); ok {
			children, err := p.flattenRecordChecked(nested, name)
			if err != nil {
				return nil, err
			}
			for childName, childValue := range children {
				if _, exists := flattened[childName]; exists {
					return nil, fmt.Errorf("flattened field name %q collides", childName)
				}
				flattened[childName] = childValue
			}
			continue
		}
		if _, exists := flattened[name]; exists {
			return nil, fmt.Errorf("flattened field name %q collides", name)
		}
		flattened[name] = value
	}
	return flattened, nil
}

// flattenRecord flattens a nested record
func (p *JSONParser) flattenRecord(record map[string]interface{}, prefix string) map[string]interface{} {
	flattened := make(map[string]interface{})

	for key, value := range record {
		newKey := key
		if prefix != "" {
			newKey = prefix + p.config.Separator + key
		}

		switch val := value.(type) {
		case map[string]interface{}:
			nested := p.flattenRecord(val, newKey)
			for nk, nv := range nested {
				flattened[nk] = nv
			}
		case []interface{}:
			// For arrays, we can either flatten or keep as-is
			// Here we keep as-is but could convert to indexed keys
			flattened[newKey] = p.flattenArray(val, newKey)
		default:
			flattened[newKey] = value
		}
	}

	return flattened
}

// flattenValue flattens a single value
func (p *JSONParser) flattenValue(value interface{}, prefix string) interface{} {
	switch val := value.(type) {
	case map[string]interface{}:
		return p.flattenRecord(val, prefix)
	case []interface{}:
		return p.flattenArray(val, prefix)
	default:
		return value
	}
}

// flattenArray flattens an array
func (p *JSONParser) flattenArray(array []interface{}, prefix string) interface{} {
	// Option 1: Keep array as-is
	// Option 2: Convert to indexed keys (e.g., field_0, field_1)
	// Here we keep as-is for simplicity
	return array
}

// detectValueType maps a JSON value to the schema type used by the SQL adapter.
func (p *JSONParser) detectValueType(value interface{}) string {
	if value == nil {
		return "null"
	}
	switch v := value.(type) {
	case bool:
		return "boolean"
	case int, int32, int64:
		return "integer"
	case float32:
		return "float"
	case float64:
		return "double"
	case string:
		return "string"
	case []interface{}:
		if len(v) > 0 {
			return "array<" + p.detectValueType(v[0]) + ">"
		}
		return "array"
	case map[string]interface{}:
		return "object"
	default:
		return "string"
	}
}

// GetNestedValue retrieves a nested value using dot notation
func (p *JSONParser) GetNestedValue(record map[string]interface{}, path string) (interface{}, bool) {
	if path == "" {
		return nil, false
	}

	parts := strings.Split(path, ".")
	current := interface{}(record)

	for _, part := range parts {
		switch curr := current.(type) {
		case map[string]interface{}:
			val, exists := curr[part]
			if !exists {
				return nil, false
			}
			current = val
		case []interface{}:
			// Handle array indexing (e.g., items[0])
			// This would require parsing array indices
			return nil, false
		default:
			return nil, false
		}
	}

	return current, true
}

// QueryJSON queries JSON data with simple filtering
func (p *JSONParser) QueryJSON(data []byte, filters map[string]interface{}) ([]map[string]interface{}, error) {
	parsed, err := p.ParseJSONArray(data)
	if err != nil {
		// Try JSON Lines
		parsed, err = p.ParseJSONLines(data)
		if err != nil {
			return nil, err
		}
	}

	// Apply filters
	filtered := make([]map[string]interface{}, 0)
	for _, record := range parsed.Data {
		if p.matchesFilters(record, filters) {
			filtered = append(filtered, record)
		}
	}

	return filtered, nil
}

// matchesFilters checks if a record matches all filters
func (p *JSONParser) matchesFilters(record map[string]interface{}, filters map[string]interface{}) bool {
	for key, expectedValue := range filters {
		actualValue, exists := record[key]
		if !exists {
			return false
		}

		if !p.compareValues(actualValue, expectedValue) {
			return false
		}
	}
	return true
}

// compareValues compares two values
func (p *JSONParser) compareValues(a, b interface{}) bool {
	// Handle type conversions
	switch aVal := a.(type) {
	case int:
		switch bVal := b.(type) {
		case int:
			return aVal == bVal
		case float64:
			return float64(aVal) == bVal
		case int64:
			return int64(aVal) == bVal
		}
	case int64:
		switch bVal := b.(type) {
		case int:
			return aVal == int64(bVal)
		case float64:
			return float64(aVal) == bVal
		case int64:
			return aVal == bVal
		}
	case float64:
		switch bVal := b.(type) {
		case int:
			return aVal == float64(bVal)
		case float64:
			return aVal == bVal
		case int64:
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

// mapFieldSlice converts map to slice
func (p *JSONParser) mapFieldSlice(fields map[string]JSONField) []JSONField {
	slice := make([]JSONField, 0, len(fields))
	for _, field := range fields {
		slice = append(slice, field)
	}
	return slice
}

// ExtractPaths extracts all paths from a JSON document
func (p *JSONParser) ExtractPaths(data []byte) ([]string, error) {
	var document interface{}
	err := json.Unmarshal(data, &document)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0)
	p.extractPathsRecursive(document, "", &paths)

	return paths, nil
}

// extractPathsRecursive recursively extracts paths
func (p *JSONParser) extractPathsRecursive(value interface{}, currentPath string, paths *[]string) {
	switch val := value.(type) {
	case map[string]interface{}:
		for key, child := range val {
			newPath := key
			if currentPath != "" {
				newPath = currentPath + "." + key
			}
			*paths = append(*paths, newPath)
			p.extractPathsRecursive(child, newPath, paths)
		}
	case []interface{}:
		for i, child := range val {
			newPath := fmt.Sprintf("%s[%d]", currentPath, i)
			*paths = append(*paths, newPath)
			p.extractPathsRecursive(child, newPath, paths)
		}
	}
}

// ValidateJSON validates JSON data
func (p *JSONParser) ValidateJSON(data []byte) error {
	var js interface{}
	return json.Unmarshal(data, &js)
}

// MinifyJSON minifies JSON by removing whitespace
func (p *JSONParser) MinifyJSON(data []byte) ([]byte, error) {
	var js interface{}
	err := json.Unmarshal(data, &js)
	if err != nil {
		return nil, err
	}

	return json.Marshal(js)
}

// PrettyPrintJSON pretty-prints JSON with indentation
func (p *JSONParser) PrettyPrintJSON(data []byte, indent string) ([]byte, error) {
	var js interface{}
	err := json.Unmarshal(data, &js)
	if err != nil {
		return nil, err
	}

	return json.MarshalIndent(js, "", indent)
}

// GetJSONType returns whether the object contains an array, JSON Lines
// sequence, or one object. Pretty-printed objects are not mistaken for JSONL.
func (p *JSONParser) GetJSONType(data []byte) (string, error) {
	return p.GetJSONTypeContext(context.Background(), data)
}

// GetJSONTypeContext detects a JSON document format with cancellation checks.
func (p *JSONParser) GetJSONTypeContext(ctx context.Context, data []byte) (string, error) {
	_, format, err := p.ParseJSONDataContext(ctx, data)
	return format, err
}

// MergeFields merges fields from multiple records in stable lexical order.
func (p *JSONParser) MergeFields(records []map[string]interface{}) []string {
	fieldSet := make(map[string]struct{})
	for _, record := range records {
		for key := range record {
			fieldSet[key] = struct{}{}
		}
	}
	fields := make([]string, 0, len(fieldSet))
	for field := range fieldSet {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

// InferSchemaFromRecords infers a stable schema across every record. Fields
// absent from any record and fields containing explicit nulls are nullable.
func (p *JSONParser) InferSchemaFromRecords(records []map[string]interface{}) *JSONSchema {
	if len(records) == 0 {
		return &JSONSchema{IsArray: true}
	}
	fieldTypes := make(map[string]string)
	seen := make(map[string]int)
	nullable := make(map[string]bool)
	for _, record := range records {
		for key, value := range record {
			seen[key]++
			valueType := p.detectValueType(value)
			if valueType == "null" {
				nullable[key] = true
				continue
			}
			fieldTypes[key] = mergeJSONTypes(fieldTypes[key], valueType)
		}
	}
	fields := make([]JSONField, 0, len(seen))
	for _, name := range p.MergeFields(records) {
		fieldType := fieldTypes[name]
		if fieldType == "" || fieldType == "null" {
			fieldType = "string"
		}
		fields = append(fields, JSONField{
			Name: name, Type: fieldType,
			Nullable: nullable[name] || seen[name] < len(records),
		})
	}
	return &JSONSchema{Fields: fields, IsArray: true}
}

func mergeJSONTypes(current, next string) string {
	if current == "" || current == next {
		return next
	}
	if current == "null" {
		return next
	}
	if next == "null" {
		return current
	}
	if (current == "integer" && (next == "float" || next == "double")) ||
		(next == "integer" && (current == "float" || current == "double")) ||
		(current == "float" && next == "double") || (current == "double" && next == "float") {
		return "double"
	}
	if current == "object" || current == "array" || strings.HasPrefix(current, "array<") ||
		next == "object" || next == "array" || strings.HasPrefix(next, "array<") {
		return "variant"
	}
	return "variant"
}
