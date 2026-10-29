package object_storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/scritchley/orc"
	"vitess.io/vitess/go/vt/sqlparser"
)

// ORCReader reads flat-schema ORC files from S3-compatible object storage.
type ORCReader struct {
	s3Client *S3Client
	config   *ORCReaderConfig
}

// ORCReaderConfig holds ORC reader configuration.
type ORCReaderConfig struct {
	BatchSize       int
	EnablePredicate bool
	Columns         []string
}

func NewORCReader(s3Client *S3Client, config *ORCReaderConfig) *ORCReader {
	if config == nil {
		config = &ORCReaderConfig{BatchSize: 1000}
	}
	if config.BatchSize <= 0 {
		config.BatchSize = 1000
	}
	return &ORCReader{s3Client: s3Client, config: config}
}

// ORCFileReader wraps the ORC decoder and its ranged S3 object reader.
type ORCFileReader struct {
	reader *orc.Reader
	file   *S3File
	ctx    context.Context
	key    string
	schema *ORCSchema
}

func (r *ORCReader) Read(ctx context.Context, key string) (*ORCFileReader, error) {
	if r == nil || r.s3Client == nil {
		return nil, fmt.Errorf("S3 client is required")
	}
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("ORC object key is required")
	}
	file, err := NewS3FileWithContext(ctx, r.s3Client, key)
	if err != nil {
		return nil, fmt.Errorf("failed to create ranged S3 file: %w", err)
	}
	decoder, err := orc.NewReader(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("failed to open ORC object: %w", err)
	}
	schema, err := orcSchemaFromReader(decoder)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return &ORCFileReader{reader: decoder, file: file, ctx: ctx, key: key, schema: schema}, nil
}

func orcSchemaFromReader(reader *orc.Reader) (*ORCSchema, error) {
	if reader == nil || reader.Schema() == nil {
		return nil, fmt.Errorf("ORC file has no schema")
	}
	root := reader.Schema()
	if root.Type().GetKind().String() != "STRUCT" {
		return nil, fmt.Errorf("ORC root type %s is not a struct", root.Type().GetKind())
	}
	fields := root.Columns()
	if len(fields) == 0 {
		return nil, fmt.Errorf("ORC file has no top-level columns")
	}
	schema := &ORCSchema{Columns: make([]ORCColumn, 0, len(fields))}
	for _, name := range fields {
		field, err := root.GetField(name)
		if err != nil {
			return nil, fmt.Errorf("read ORC field %q: %w", name, err)
		}
		typ := field.Type()
		kind := typ.GetKind().String()
		standardType, err := orcStandardType(kind)
		if err != nil {
			return nil, fmt.Errorf("ORC field %q: %w", name, err)
		}
		schema.Columns = append(schema.Columns, ORCColumn{
			Name: name, Type: standardType, Precision: int(typ.GetPrecision()), Scale: int(typ.GetScale()), Nullable: true,
		})
	}
	return schema, nil
}

func orcStandardType(kind string) (string, error) {
	switch kind {
	case "BOOLEAN":
		return "boolean", nil
	case "BYTE", "SHORT", "INT":
		return "integer", nil
	case "LONG":
		return "bigint", nil
	case "FLOAT":
		return "float", nil
	case "DOUBLE":
		return "double", nil
	case "STRING", "CHAR", "VARCHAR":
		return "string", nil
	case "DECIMAL":
		return "decimal", nil
	case "DATE":
		return "date", nil
	case "TIMESTAMP":
		return "timestamp", nil
	case "BINARY":
		return "binary", nil
	default:
		return "", fmt.Errorf("nested or unsupported ORC type %q is outside the flat primitive query subset", strings.ToLower(kind))
	}
}

func (r *ORCFileReader) ReadNumRows(num int) ([]map[string]interface{}, error) {
	if num <= 0 {
		return []map[string]interface{}{}, nil
	}
	return r.readRows(num)
}

func (r *ORCFileReader) ReadAll() ([]map[string]interface{}, error) {
	return r.readRows(0)
}

func (r *ORCFileReader) readRows(maxRows int) ([]map[string]interface{}, error) {
	if r == nil || r.reader == nil || r.schema == nil {
		return nil, fmt.Errorf("ORC reader is not initialized")
	}
	columns := make([]string, len(r.schema.Columns))
	for i, column := range r.schema.Columns {
		columns[i] = column.Name
	}
	cursor := r.reader.Select(columns...)
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("select ORC columns: %w", err)
	}
	capacity := r.reader.NumRows()
	if capacity > 4096 {
		capacity = 4096
	}
	rows := make([]map[string]interface{}, 0, capacity)
	for cursor.Stripes() {
		for cursor.Next() {
			if err := r.ctx.Err(); err != nil {
				return nil, err
			}
			values := cursor.Row()
			if len(values) != len(columns) {
				return nil, fmt.Errorf("ORC row has %d values for %d columns", len(values), len(columns))
			}
			row := make(map[string]interface{}, len(columns))
			for i, name := range columns {
				row[name] = normalizeORCValue(values[i])
			}
			rows = append(rows, row)
			if maxRows > 0 && len(rows) >= maxRows {
				return rows, nil
			}
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("read ORC rows: %w", err)
	}
	return rows, nil
}

func normalizeORCValue(value interface{}) interface{} {
	switch v := value.(type) {
	case nil, bool, int64, float64, string, []byte, time.Time:
		return v
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int:
		return int64(v)
	case uint8:
		return int64(v)
	case uint16:
		return int64(v)
	case uint32:
		return int64(v)
	case float32:
		return float64(v)
	case orc.Date:
		return v.Time
	case orc.Decimal:
		return v.String()
	default:
		return v
	}
}

func (r *ORCFileReader) GetSchema() (*ORCSchema, error) {
	if r == nil || r.schema == nil {
		return nil, fmt.Errorf("ORC reader is not initialized")
	}
	return r.schema, nil
}

func (r *ORCFileReader) GetNumRows() int {
	if r == nil || r.reader == nil {
		return 0
	}
	return r.reader.NumRows()
}

func (r *ORCFileReader) Close() error {
	if r == nil || r.file == nil {
		return nil
	}
	return r.file.Close()
}

// GetStripeStatistics reports the stripe count only. This reader does not
// expose ORC stripe offsets or column statistics for pruning.
func (r *ORCFileReader) GetStripeStatistics() ([]ORCStripeStatistics, error) {
	if r == nil || r.reader == nil {
		return nil, fmt.Errorf("ORC reader is not initialized")
	}
	stripes, err := r.reader.NumStripes()
	if err != nil {
		return nil, err
	}
	stats := make([]ORCStripeStatistics, stripes)
	for i := range stats {
		stats[i].StripeNumber = i
	}
	return stats, nil
}

// ApplyPredicatePushdown returns every stripe because this reader does not
// expose statistics for safe stripe pruning. Predicates are applied after
// decoding by FilterORCFile or the shared SQL adapter.
func (r *ORCReader) ApplyPredicatePushdown(ctx context.Context, key string, predicates []ORCPredicate) ([]int, error) {
	file, err := r.Read(ctx, key)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stripes, err := file.reader.NumStripes()
	if err != nil {
		return nil, err
	}
	indexes := make([]int, stripes)
	for i := range indexes {
		indexes[i] = i
	}
	return indexes, nil
}

func (r *ORCReader) stripeMatchesPredicates(ORCStripeStatistics, []ORCPredicate) bool {
	return true
}

func (r *ORCReader) columnMatchesPredicate(ORCStatistics, ORCPredicate) bool {
	return true
}

type ORCPredicate struct {
	ColumnName string
	Operator   string
	Value      interface{}
	Values     []interface{}
}

func (r *ORCReader) FilterORCFile(ctx context.Context, key string, predicates []ORCPredicate) ([]map[string]interface{}, error) {
	file, err := r.Read(ctx, key)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rows, err := file.ReadAll()
	if err != nil {
		return nil, err
	}
	conditions := make([]s3CSVCondition, 0, len(predicates))
	for _, predicate := range predicates {
		condition := s3CSVCondition{column: predicate.ColumnName, value: predicate.Value}
		switch strings.ToUpper(predicate.Operator) {
		case "EQ":
			condition.op = sqlparser.EqualOp
		case "NEQ":
			condition.op = sqlparser.NotEqualOp
		case "LT":
			condition.op = sqlparser.LessThanOp
		case "LTE":
			condition.op = sqlparser.LessEqualOp
		case "GT":
			condition.op = sqlparser.GreaterThanOp
		case "GTE":
			condition.op = sqlparser.GreaterEqualOp
		case "IN":
			condition.op = sqlparser.InOp
			condition.values = predicate.Values
		case "IS_NULL":
			condition.isNull = 1
		default:
			return nil, fmt.Errorf("unsupported ORC predicate operator %q", predicate.Operator)
		}
		conditions = append(conditions, condition)
	}
	return applyCSVConditions(ctx, rows, conditions)
}

func (r *ORCReader) GetMetadata(ctx context.Context, key string) (*ORCFileMetadata, error) {
	file, err := r.Read(ctx, key)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	objectMetadata, err := r.s3Client.GetObjectMetadata(ctx, key)
	if err != nil {
		return nil, err
	}
	return &ORCFileMetadata{NumberOfRows: int64(file.GetNumRows()), FileSize: objectMetadata.ContentLength}, nil
}

func (r *ORCReader) GetSchemaFromKey(ctx context.Context, key string) (*ORCSchema, error) {
	file, err := r.Read(ctx, key)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return file.GetSchema()
}

func (r *ORCReader) EstimateCompressionRatio(context.Context, string) (float64, error) {
	return 0, fmt.Errorf("ORC compression ratio metadata is not available")
}

type ORCSchema struct {
	Columns []ORCColumn
}

type ORCColumn struct {
	Name      string
	Type      string
	Precision int
	Scale     int
	Nullable  bool
	Comment   string
}

type ORCStatistics struct {
	HasNull       bool
	MinValue      interface{}
	MaxValue      interface{}
	NumValues     int64
	NumDistinct   int64
	FractionNulls float64
}

type ORCStripeStatistics struct {
	StripeNumber int
	Offset       int64
	IndexLength  int64
	DataLength   int64
	FooterLength int64
	NumberOfRows int64
	ColumnStats  []ORCStatistics
}

type ORCFileMetadata struct {
	NumberOfRows    int64
	FileSize        int64
	Compression     string
	WriterVersion   string
	SoftwareVersion string
}
