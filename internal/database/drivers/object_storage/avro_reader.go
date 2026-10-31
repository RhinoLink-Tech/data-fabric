package object_storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/linkedin/goavro/v2"
)

// AvroReader reads Avro Object Container Files from S3-compatible storage or a supplied reader.
type AvroObjectReadFunc func(context.Context, string) ([]byte, error)

type AvroReader struct {
	s3Client   *S3Client
	config     *AvroReaderConfig
	readObject AvroObjectReadFunc
}

// AvroReaderConfig holds Avro reader configuration.
type AvroReaderConfig struct {
	BatchSize int
}

// NewAvroReader creates a new Avro reader.
func NewAvroReader(s3Client *S3Client, config *AvroReaderConfig) *AvroReader {
	if config == nil {
		config = &AvroReaderConfig{}
	}
	if config.BatchSize <= 0 {
		config.BatchSize = 1000
	}
	return &AvroReader{s3Client: s3Client, config: config}
}

// NewAvroReaderWithReadFunc creates an OCF reader over a filesystem or other
// bounded source without requiring an S3 client.
func NewAvroReaderWithReadFunc(config *AvroReaderConfig, read AvroObjectReadFunc) *AvroReader {
	reader := NewAvroReader(nil, config)
	reader.readObject = read
	return reader
}

// AvroFileReader holds the downloaded OCF bytes so schema discovery and row
// decoding use the same object version and do not issue duplicate downloads.
type AvroFileReader struct {
	data   []byte
	ctx    context.Context
	key    string
	schema *AvroSchema
}

// Read downloads one OCF object and reads its embedded schema from the OCF
// header. Raw Avro datum streams and standalone schema JSON are not OCF files.
func (r *AvroReader) Read(ctx context.Context, key string) (*AvroFileReader, error) {
	if r == nil || (r.s3Client == nil && r.readObject == nil) {
		return nil, fmt.Errorf("S3 client or Avro object reader is required")
	}
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("Avro object key is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var data []byte
	var err error
	if r.readObject != nil {
		data, err = r.readObject(ctx, key)
	} else {
		data, err = r.s3Client.GetObject(ctx, key)
	}
	if err != nil {
		return nil, fmt.Errorf("read Avro OCF object: %w", err)
	}
	ocf, err := goavro.NewOCFReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open Avro OCF object: %w", err)
	}
	schema, err := avroSchemaFromCodec(ocf.Codec())
	if err != nil {
		return nil, err
	}
	return &AvroFileReader{data: data, ctx: ctx, key: key, schema: schema}, nil
}

func avroSchemaFromCodec(codec *goavro.Codec) (*AvroSchema, error) {
	if codec == nil {
		return nil, fmt.Errorf("Avro OCF has no writer schema")
	}
	schemaJSON := codec.Schema()
	var document struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		Fields []struct {
			Name string          `json:"name"`
			Type json.RawMessage `json:"type"`
		} `json:"fields"`
	}
	if err := json.Unmarshal([]byte(schemaJSON), &document); err != nil {
		return nil, fmt.Errorf("parse Avro writer schema: %w", err)
	}
	if document.Type != "record" || len(document.Fields) == 0 {
		return nil, fmt.Errorf("Avro OCF writer schema must be a non-empty record")
	}
	schema := &AvroSchema{JSON: schemaJSON, Name: document.Name, Type: document.Type, Fields: make([]AvroField, 0, len(document.Fields))}
	for _, field := range document.Fields {
		if strings.TrimSpace(field.Name) == "" {
			return nil, fmt.Errorf("Avro writer schema contains a field with no name")
		}
		standardType, logicalType, nullable, precision, scale, err := avroFieldType(field.Type)
		if err != nil {
			return nil, fmt.Errorf("Avro field %q: %w", field.Name, err)
		}
		schema.Fields = append(schema.Fields, AvroField{
			Name: field.Name, Type: standardType, Nullable: nullable,
			LogicalType: logicalType, Precision: precision, Scale: scale,
		})
	}
	return schema, nil
}

func avroFieldType(raw json.RawMessage) (standardType, logicalType string, nullable bool, precision, scale int, err error) {
	var union []json.RawMessage
	if err = json.Unmarshal(raw, &union); err == nil && union != nil {
		if len(union) != 2 {
			return "", "", false, 0, 0, fmt.Errorf("only nullable unions with one primitive type are supported")
		}
		var first, second string
		_ = json.Unmarshal(union[0], &first)
		_ = json.Unmarshal(union[1], &second)
		if first == "null" {
			standardType, logicalType, _, precision, scale, err = avroFieldType(union[1])
		} else if second == "null" {
			standardType, logicalType, _, precision, scale, err = avroFieldType(union[0])
		} else {
			return "", "", false, 0, 0, fmt.Errorf("only nullable unions containing null and one primitive type are supported")
		}
		return standardType, logicalType, true, precision, scale, err
	}

	var primitive string
	if json.Unmarshal(raw, &primitive) == nil {
		standardType, err = avroPrimitiveType(primitive, "")
		return standardType, "", false, 0, 0, err
	}
	var object map[string]json.RawMessage
	if err = json.Unmarshal(raw, &object); err != nil || object == nil {
		return "", "", false, 0, 0, fmt.Errorf("invalid Avro field schema")
	}
	var baseType string
	if err = json.Unmarshal(object["type"], &baseType); err != nil {
		return "", "", false, 0, 0, fmt.Errorf("nested, named, or non-primitive Avro fields are not supported")
	}
	_ = json.Unmarshal(object["logicalType"], &logicalType)
	_ = json.Unmarshal(object["precision"], &precision)
	_ = json.Unmarshal(object["scale"], &scale)
	standardType, err = avroPrimitiveType(baseType, logicalType)
	return standardType, logicalType, false, precision, scale, err
}

func avroPrimitiveType(baseType, logicalType string) (string, error) {
	switch logicalType {
	case "date":
		return "date", nil
	case "time-millis", "time-micros":
		return "time", nil
	case "timestamp-millis", "timestamp-micros":
		return "timestamp", nil
	case "decimal":
		if baseType != "bytes" && baseType != "fixed" {
			return "", fmt.Errorf("Avro decimal logical type must use bytes or fixed")
		}
		return "decimal", nil
	case "uuid":
		return "uuid", nil
	}
	switch baseType {
	case "boolean":
		return "boolean", nil
	case "int":
		return "integer", nil
	case "long":
		return "bigint", nil
	case "float":
		return "float", nil
	case "double":
		return "double", nil
	case "string", "enum":
		return "string", nil
	case "bytes":
		return "binary", nil
	default:
		return "", fmt.Errorf("nested or unsupported Avro type %q", baseType)
	}
}

// ReadNumRows decodes at most num records from the configured OCF object.
func (r *AvroFileReader) ReadNumRows(num int) ([]map[string]interface{}, error) {
	if num <= 0 {
		return []map[string]interface{}{}, nil
	}
	return r.readRows(num, nil)
}

// ReadAll decodes all records from the configured OCF object.
func (r *AvroFileReader) ReadAll() ([]map[string]interface{}, error) {
	return r.readRows(0, nil)
}

func (r *AvroFileReader) readRows(maxRows int, callback func(map[string]interface{}) error) ([]map[string]interface{}, error) {
	if r == nil || len(r.data) == 0 || r.schema == nil {
		return nil, fmt.Errorf("Avro reader is not initialized")
	}
	if r.ctx == nil {
		r.ctx = context.Background()
	}
	ocf, err := goavro.NewOCFReader(bytes.NewReader(r.data))
	if err != nil {
		return nil, fmt.Errorf("open Avro OCF records: %w", err)
	}
	rows := make([]map[string]interface{}, 0, 128)
	fields := make(map[string]AvroField, len(r.schema.Fields))
	for _, field := range r.schema.Fields {
		fields[field.Name] = field
	}
	for ocf.Scan() {
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		datum, err := ocf.Read()
		if err != nil {
			return nil, fmt.Errorf("decode Avro OCF record: %w", err)
		}
		record, ok := datum.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("Avro OCF datum is %T, want a record", datum)
		}
		row := make(map[string]interface{}, len(fields))
		for name, field := range fields {
			row[name] = normalizeAvroValue(record[name], field)
		}
		if callback != nil {
			if err := callback(row); err != nil {
				return nil, err
			}
		} else {
			rows = append(rows, row)
			if maxRows > 0 && len(rows) >= maxRows {
				return rows, nil
			}
		}
	}
	if err := ocf.Err(); err != nil {
		return nil, fmt.Errorf("read Avro OCF records: %w", err)
	}
	return rows, nil
}

func avroDateValue(daysSinceEpoch int64) time.Time {
	return time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(daysSinceEpoch))
}

func normalizeAvroValue(value interface{}, field AvroField) interface{} {
	switch v := value.(type) {
	case nil, bool, float64, string, []byte, time.Time:
		return v
	case time.Duration:
		if field.LogicalType == "time-millis" || field.LogicalType == "time-micros" {
			return time.Unix(0, int64(v)).UTC()
		}
		return int64(v)
	case int64:
		if field.LogicalType == "date" {
			return avroDateValue(v)
		}
		return v
	case int32:
		if field.LogicalType == "date" {
			return avroDateValue(int64(v))
		}
		return int64(v)
	case int:
		if field.LogicalType == "date" {
			return avroDateValue(int64(v))
		}
		return int64(v)
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case uint8:
		return int64(v)
	case uint16:
		return int64(v)
	case uint32:
		return int64(v)
	case float32:
		return float64(v)
	case *big.Rat:
		if v == nil {
			return nil
		}
		if field.Scale > 0 {
			return v.FloatString(field.Scale)
		}
		return v.RatString()
	case big.Rat:
		if field.Scale > 0 {
			return v.FloatString(field.Scale)
		}
		return v.RatString()
	case map[string]interface{}:
		if len(v) == 1 {
			for unionType, unionValue := range v {
				if unionType == "null" {
					return nil
				}
				return normalizeAvroValue(unionValue, field)
			}
		}
		return v
	default:
		return v
	}
}

// GetSchema returns the writer schema embedded in the OCF header.
func (r *AvroFileReader) GetSchema() (*AvroSchema, error) {
	if r == nil || r.schema == nil {
		return nil, fmt.Errorf("Avro reader is not initialized")
	}
	return r.schema, nil
}

// Close releases the in-memory OCF bytes.
func (r *AvroFileReader) Close() error {
	if r != nil {
		r.data = nil
	}
	return nil
}

// AvroSchema represents the record schema embedded in an OCF.
type AvroSchema struct {
	JSON   string
	Name   string
	Type   string
	Fields []AvroField
}

// AvroField represents a supported top-level Avro record field.
type AvroField struct {
	Name        string
	Type        string
	Nullable    bool
	LogicalType string
	Precision   int
	Scale       int
}

// GetMetadata retrieves S3 metadata for an Avro OCF object.
func (r *AvroReader) GetMetadata(ctx context.Context, key string) (*AvroFileMetadata, error) {
	if r == nil || r.s3Client == nil {
		return nil, fmt.Errorf("S3 client is required")
	}
	metadata, err := r.s3Client.GetObjectMetadata(ctx, key)
	if err != nil {
		return nil, err
	}
	return &AvroFileMetadata{Key: key, Size: metadata.ContentLength, ModifiedAt: metadata.LastModified}, nil
}

// AvroFileMetadata represents S3 metadata for an Avro OCF object.
type AvroFileMetadata struct {
	Key        string
	Size       int64
	ModifiedAt time.Time
	Codec      string
}

// StreamRecords streams records from the downloaded OCF bytes to callback.
func (r *AvroFileReader) StreamRecords(callback func(map[string]interface{}) error) error {
	if callback == nil {
		return fmt.Errorf("Avro record callback is required")
	}
	_, err := r.readRows(0, callback)
	return err
}
