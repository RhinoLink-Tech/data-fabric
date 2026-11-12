package object_storage

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// CSVSchemadDetector detects schema from CSV files
type CSVSchemadDetector struct {
	config *CSVDetectionConfig
}

// CSVDetectionConfig holds CSV detection configuration
type CSVDetectionConfig struct {
	SampleSize       int      // Number of rows to sample for type detection
	Delimiter        rune     // Field delimiter
	HasHeader        bool     // Whether file has header row
	QuoteChar        rune     // Quote character
	HeaderRowsToSkip int      // Number of header rows to skip
	NullValues       []string // Values to treat as null
}

// NewCSVSchemadDetector creates a new CSV schema detector
func NewCSVSchemadDetector(config *CSVDetectionConfig) *CSVSchemadDetector {
	if config == nil {
		config = &CSVDetectionConfig{
			SampleSize:       1000,
			Delimiter:        ',',
			HasHeader:        true,
			QuoteChar:        '"',
			HeaderRowsToSkip: 0,
			NullValues:       []string{"", "NULL", "null", "NA", "N/A", "nil"},
		}
	}

	return &CSVSchemadDetector{
		config: config,
	}
}

// DetectedSchema represents detected CSV schema
type DetectedSchema struct {
	Columns    []DetectedColumn
	Delimiter  rune
	HasHeader  bool
	HeaderRow  []string
	RowCount   int
	SampleSize int
	Confidence float64
}

// DetectedColumn represents a detected column
type DetectedColumn struct {
	Name          string
	Type          string
	Nullable      bool
	UniqueCount   int
	NullCount     int
	MinValue      interface{}
	MaxValue      interface{}
	ExampleValues []interface{}
	Confidence    float64
}

// stripUTF8BOM removes a leading UTF-8 byte order mark so it does not
// become part of the first header field.
func stripUTF8BOM(data []byte) []byte {
	return bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
}

// DetectSchema detects schema from CSV data
func (d *CSVSchemadDetector) DetectSchema(data []byte) (*DetectedSchema, error) {
	reader := csv.NewReader(bytes.NewReader(stripUTF8BOM(data)))
	reader.Comma = d.config.Delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	// Skip header rows if needed
	for i := 0; i < d.config.HeaderRowsToSkip; i++ {
		_, err := reader.Read()
		if err != nil {
			return nil, fmt.Errorf("failed to skip header row: %w", err)
		}
	}

	// Read header row if present
	var headerRow []string
	var err error

	if d.config.HasHeader {
		headerRow, err = reader.Read()
		if err != nil {
			return nil, fmt.Errorf("failed to read header row: %w", err)
		}
	}

	// Sample rows for type detection
	sampleRows := make([][]string, 0, d.config.SampleSize)
	rowCount := 0

	for rowCount < d.config.SampleSize {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read row: %w", err)
		}

		sampleRows = append(sampleRows, row)
		rowCount++
	}

	if len(sampleRows) == 0 {
		return nil, fmt.Errorf("no data rows found")
	}

	// Detect number of columns
	numCols := len(sampleRows[0])
	if numCols == 0 {
		return nil, fmt.Errorf("no columns found")
	}

	// Generate column names if no header
	if len(headerRow) == 0 {
		headerRow = d.generateColumnNames(numCols)
	} else if len(headerRow) != numCols {
		// Header and data column count mismatch
		// Truncate or extend header
		if len(headerRow) > numCols {
			headerRow = headerRow[:numCols]
		} else {
			for i := len(headerRow); i < numCols; i++ {
				headerRow = append(headerRow, fmt.Sprintf("column_%d", i+1))
			}
		}
	}

	// Detect types for each column
	columns := make([]DetectedColumn, numCols)

	for colIdx := 0; colIdx < numCols; colIdx++ {
		col := d.detectColumn(headerRow[colIdx], colIdx, sampleRows)
		columns[colIdx] = col
	}

	schema := &DetectedSchema{
		Columns:    columns,
		Delimiter:  d.config.Delimiter,
		HasHeader:  d.config.HasHeader,
		HeaderRow:  headerRow,
		RowCount:   rowCount,
		SampleSize: len(sampleRows),
		Confidence: d.calculateConfidence(columns),
	}

	return schema, nil
}

// detectColumn detects type for a single column
func (d *CSVSchemadDetector) detectColumn(name string, colIdx int, rows [][]string) DetectedColumn {
	col := DetectedColumn{
		Name:          name,
		Type:          "string",
		Nullable:      false,
		UniqueCount:   0,
		NullCount:     0,
		ExampleValues: make([]interface{}, 0, 5),
	}

	valueSet := make(map[string]bool)
	typeCounts := make(map[string]int)

	for _, row := range rows {
		if colIdx >= len(row) {
			continue
		}

		value := strings.TrimSpace(row[colIdx])

		// Check for null
		if d.isNull(value) {
			col.NullCount++
			col.Nullable = true
			continue
		}

		// Add to unique set
		if !valueSet[value] {
			valueSet[value] = true
			col.UniqueCount++
		}

		// Add to example values (limit to 5)
		if len(col.ExampleValues) < 5 {
			col.ExampleValues = append(col.ExampleValues, value)
		}

		// Detect type
		typeName := d.detectValueType(value)
		typeCounts[typeName]++
	}

	// Determine most common type
	if len(typeCounts) > 0 {
		maxCount := 0
		for typeName, count := range typeCounts {
			if count > maxCount {
				maxCount = count
				col.Type = typeName
			}
		}
	}

	// Set min/max for numeric types
	if col.Type == "integer" || col.Type == "float" {
		d.calculateNumericBounds(rows, colIdx, &col)
	}

	col.Confidence = d.calculateColumnConfidence(col, len(rows))

	return col
}

// detectValueType detects the type of a value
func (d *CSVSchemadDetector) detectValueType(value string) string {
	// Try integer
	if _, err := strconv.ParseInt(value, 10, 64); err == nil {
		return "integer"
	}

	// Try float
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return "float"
	}

	// Try boolean
	if strings.ToLower(value) == "true" || strings.ToLower(value) == "false" {
		return "boolean"
	}

	// Try date (ISO format)
	if matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}$`, value); matched {
		if _, err := parseDate(value); err == nil {
			return "date"
		}
	}

	// Try datetime (ISO format)
	if matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}`, value); matched {
		if _, err := parseDateTime(value); err == nil {
			return "datetime"
		}
	}

	// Default to string
	return "string"
}

// isNull checks if a value should be treated as null
func (d *CSVSchemadDetector) isNull(value string) bool {
	for _, nullVal := range d.config.NullValues {
		if value == nullVal {
			return true
		}
	}
	return false
}

// calculateNumericBounds calculates min/max values for numeric columns
func (d *CSVSchemadDetector) calculateNumericBounds(rows [][]string, colIdx int, col *DetectedColumn) {
	var minVal, maxVal float64
	firstVal := true

	for _, row := range rows {
		if colIdx >= len(row) {
			continue
		}

		value := strings.TrimSpace(row[colIdx])
		if d.isNull(value) {
			continue
		}

		var floatVal float64
		var err error

		if col.Type == "integer" {
			intVal, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr != nil {
				continue
			}
			floatVal = float64(intVal)
		} else {
			floatVal, err = strconv.ParseFloat(value, 64)
			if err != nil {
				continue
			}
		}

		if firstVal {
			minVal = floatVal
			maxVal = floatVal
			firstVal = false
		} else {
			if floatVal < minVal {
				minVal = floatVal
			}
			if floatVal > maxVal {
				maxVal = floatVal
			}
		}
	}

	if !firstVal {
		if col.Type == "integer" {
			col.MinValue = int64(minVal)
			col.MaxValue = int64(maxVal)
		} else {
			col.MinValue = minVal
			col.MaxValue = maxVal
		}
	}
}

// calculateConfidence calculates overall schema confidence
func (d *CSVSchemadDetector) calculateConfidence(columns []DetectedColumn) float64 {
	if len(columns) == 0 {
		return 0.0
	}

	totalConfidence := 0.0
	for _, col := range columns {
		totalConfidence += col.Confidence
	}

	return totalConfidence / float64(len(columns))
}

// calculateColumnConfidence calculates confidence for a column
func (d *CSVSchemadDetector) calculateColumnConfidence(col DetectedColumn, totalRows int) float64 {
	if totalRows == 0 {
		return 0.0
	}

	// Confidence based on null ratio and type consistency
	nullRatio := float64(col.NullCount) / float64(totalRows)
	confidence := 1.0 - nullRatio

	// Reduce confidence if many unique values (might be string)
	if col.Type != "string" && col.UniqueCount > 100 {
		confidence *= 0.8
	}

	// Ensure confidence is between 0 and 1
	if confidence < 0.0 {
		confidence = 0.0
	}
	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence
}

// generateColumnNames generates column names if no header
func (d *CSVSchemadDetector) generateColumnNames(numCols int) []string {
	names := make([]string, numCols)
	for i := 0; i < numCols; i++ {
		names[i] = fmt.Sprintf("column_%d", i+1)
	}
	return names
}

// DetectDelimiter automatically detects the CSV delimiter
func (d *CSVSchemadDetector) DetectDelimiter(data []byte) rune {
	delimiters := []rune{',', '\t', ';', '|', '|'}
	delimiterCounts := make(map[rune]int)

	// Count delimiter occurrences in first few lines
	reader := csv.NewReader(bytes.NewReader(stripUTF8BOM(data)))
	linesRead := 0

	for linesRead < 10 {
		line, err := reader.Read()
		if err != nil {
			break
		}
		linesRead++

		// Count each delimiter
		for _, delim := range delimiters {
			count := 0
			for _, field := range line {
				count += strings.Count(field, string(delim))
			}
			// Account for number of fields (fields = delimiters + 1)
			fieldCount := len(line)
			if fieldCount > 1 {
				delimiterCounts[delim] += fieldCount - 1
			}
		}
	}

	// Find most common delimiter
	maxCount := 0
	bestDelim := ','
	for delim, count := range delimiterCounts {
		if count > maxCount {
			maxCount = count
			bestDelim = delim
		}
	}

	return bestDelim
}

// DetectHasHeader tries to detect if CSV has a header row
func (d *CSVSchemadDetector) DetectHasHeader(data []byte) bool {
	reader := csv.NewReader(bytes.NewReader(stripUTF8BOM(data)))
	reader.Comma = d.config.Delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	// Read first few rows
	rows := make([][]string, 0, 10)
	for i := 0; i < 10; i++ {
		row, err := reader.Read()
		if err != nil {
			break
		}
		rows = append(rows, row)
	}

	if len(rows) < 2 {
		return false
	}

	// Check if first row looks like a header
	// Heuristics:
	// 1. First row contains strings, subsequent rows contain numbers
	// 2. First row values are unique
	// 3. First row values don't match common data patterns

	firstRow := rows[0]
	if len(firstRow) == 0 {
		return false
	}

	// Check uniqueness
	firstRowSet := make(map[string]bool)
	for _, val := range firstRow {
		firstRowSet[val] = true
	}

	uniqueRatio := float64(len(firstRowSet)) / float64(len(firstRow))

	// Check if first row contains strings while second row contains numbers
	hasHeaderScore := 0.0

	if uniqueRatio > 0.8 {
		hasHeaderScore += 0.3
	}

	if len(rows) > 1 {
		secondRow := rows[1]
		stringCountFirstRow := 0
		numberCountSecondRow := 0

		for _, val := range firstRow {
			if d.detectValueType(val) == "string" {
				stringCountFirstRow++
			}
		}

		for _, val := range secondRow {
			if d.detectValueType(val) == "integer" || d.detectValueType(val) == "float" {
				numberCountSecondRow++
			}
		}

		stringRatioFirstRow := float64(stringCountFirstRow) / float64(len(firstRow))
		numberRatioSecondRow := float64(numberCountSecondRow) / float64(len(secondRow))

		if stringRatioFirstRow > 0.5 && numberRatioSecondRow > 0.5 {
			hasHeaderScore += 0.4
		}

		// Check if first row values don't match common patterns
		for _, val := range firstRow {
			if d.looksLikeColumnName(val) {
				hasHeaderScore += 0.1
			}
		}
	}

	return hasHeaderScore > 0.5
}

// looksLikeColumnName checks if a value looks like a column name
func (d *CSVSchemadDetector) looksLikeColumnName(value string) bool {
	value = strings.TrimSpace(value)

	// Empty string
	if value == "" {
		return false
	}

	// Contains only alphabets, numbers, underscore, hyphen, space
	matched, _ := regexp.MatchString(`^[a-zA-Z][a-zA-Z0-9_\- ]*$`, value)
	if !matched {
		return false
	}

	// Not a number
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return false
	}

	// Not a date
	if matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}$`, value); matched {
		return false
	}

	return true
}

// AnalyzeStatistics analyzes column statistics
func (d *CSVSchemadDetector) AnalyzeStatistics(data []byte, colIdx int) (*ColumnStatistics, error) {
	reader := csv.NewReader(bytes.NewReader(stripUTF8BOM(data)))
	reader.Comma = d.config.Delimiter

	stats := &ColumnStatistics{
		Count:       0,
		NullCount:   0,
		UniqueCount: 0,
		Values:      make(map[string]int),
	}

	skipHeader := d.config.HasHeader
	if skipHeader {
		_, err := reader.Read()
		if err != nil {
			return nil, err
		}
	}

	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if colIdx >= len(row) {
			continue
		}

		stats.Count++
		value := strings.TrimSpace(row[colIdx])

		if d.isNull(value) {
			stats.NullCount++
			continue
		}

		stats.Values[value]++

		if len(stats.Values) > 1000 {
			// Stop tracking unique values beyond 1000
			stats.UniqueCount = 1000
		}
	}

	if stats.UniqueCount == 0 {
		stats.UniqueCount = len(stats.Values)
	}

	return stats, nil
}

// ColumnStatistics represents column statistics
type ColumnStatistics struct {
	Count       int
	NullCount   int
	UniqueCount int
	Values      map[string]int
	MinValue    interface{}
	MaxValue    interface{}
	Mean        float64
	Median      interface{}
	Mode        string
}

// parseDate parses a date string
func parseDate(s string) (interface{}, error) {
	// Simple date parsing - would use actual date parsing library
	return s, nil
}

// parseDateTime parses a datetime string
func parseDateTime(s string) (interface{}, error) {
	// Simple datetime parsing - would use actual datetime parsing library
	return s, nil
}

// GetMostFrequentValues returns the most frequent values
func (s *ColumnStatistics) GetMostFrequentValues(n int) []string {
	type valueCount struct {
		value string
		count int
	}

	vals := make([]valueCount, 0, len(s.Values))
	for val, count := range s.Values {
		vals = append(vals, valueCount{val, count})
	}

	// Sort by count (descending)
	for i := 0; i < len(vals); i++ {
		for j := i + 1; j < len(vals); j++ {
			if vals[j].count > vals[i].count {
				vals[i], vals[j] = vals[j], vals[i]
			}
		}
	}

	result := make([]string, 0, n)
	for i := 0; i < n && i < len(vals); i++ {
		result = append(result, vals[i].value)
	}

	return result
}

// CalculateMean calculates mean for numeric values
func (s *ColumnStatistics) CalculateMean() float64 {
	if len(s.Values) == 0 {
		return math.NaN()
	}

	sum := 0.0
	count := 0

	for val, freq := range s.Values {
		if floatVal, err := strconv.ParseFloat(val, 64); err == nil {
			sum += floatVal * float64(freq)
			count += freq
		}
	}

	if count == 0 {
		return math.NaN()
	}

	return sum / float64(count)
}
