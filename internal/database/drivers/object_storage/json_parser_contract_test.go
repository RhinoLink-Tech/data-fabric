package object_storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
)

func TestJSONParserInfersStableSchemaAcrossDocumentForms(t *testing.T) {
	parser := object_storage.NewJSONParser(nil)
	tests := []struct {
		name string
		data string
		kind string
		rows int
	}{
		{name: "object", data: "{\n  \"id\": 1,\n  \"name\": \"solo\"\n}", kind: "object", rows: 1},
		{name: "array", data: "[{\"id\":1,\"name\":\"Ada\"},{\"id\":2,\"active\":true},{\"id\":3,\"name\":null,\"active\":false}]", kind: "array", rows: 3},
		{name: "json-lines", data: "{\"id\":1,\"name\":\"Ada\"}\n{\"id\":2,\"active\":true}\n{\"id\":3,\"name\":null,\"active\":false}\n", kind: "jsonlines", rows: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind, err := parser.GetJSONType([]byte(test.data))
			if err != nil || kind != test.kind {
				t.Fatalf("GetJSONType() = %q, %v; want %q", kind, err, test.kind)
			}
			schema, err := parser.DetectSchema([]byte(test.data))
			if err != nil {
				t.Fatalf("DetectSchema() error = %v", err)
			}
			parsed, err := parseJSONByKind(parser, test.kind, []byte(test.data))
			if err != nil {
				t.Fatalf("parse document: %v", err)
			}
			if len(parsed.Data) != test.rows {
				t.Fatalf("parsed rows = %d, want %d", len(parsed.Data), test.rows)
			}
			if test.kind != "object" && len(schema.Fields) != 3 {
				t.Fatalf("inferred fields = %+v, want id/name/active from all rows", schema.Fields)
			}
			if test.kind != "object" {
				nullableByName := make(map[string]bool, len(schema.Fields))
				for _, field := range schema.Fields {
					nullableByName[field.Name] = field.Nullable
				}
				if nullableByName["id"] || !nullableByName["name"] || !nullableByName["active"] {
					t.Fatalf("field nullability should account for missing and null values: %+v", schema.Fields)
				}
			}
		})
	}
}

func TestJSONParserKeepsJSONStringsAndRejectsHeterogeneousSQLTypes(t *testing.T) {
	parser := object_storage.NewJSONParser(nil)
	parsed, err := parser.ParseJSONArray([]byte("[{\"code\":\"00123\"},{\"code\":\"456\"}]"))
	if err != nil {
		t.Fatalf("ParseJSONArray() error = %v", err)
	}
	if parsed.Schema.Fields[0].Type != "string" || parsed.Data[0]["code"] != "00123" {
		t.Fatalf("numeric-looking JSON strings were coerced: schema=%+v row=%+v", parsed.Schema.Fields, parsed.Data[0])
	}

	mixed, err := parser.ParseJSONArray([]byte("[{\"value\":1},{\"value\":\"one\"}]"))
	if err != nil {
		t.Fatalf("ParseJSONArray(mixed) error = %v", err)
	}
	if mixed.Schema.Fields[0].Type != "variant" {
		t.Fatalf("heterogeneous values inferred as %q, want variant", mixed.Schema.Fields[0].Type)
	}
}

func parseJSONByKind(parser *object_storage.JSONParser, kind string, data []byte) (*object_storage.ParsedJSON, error) {
	switch kind {
	case "object":
		return parser.ParseJSONDocument(data)
	case "array":
		return parser.ParseJSONArray(data)
	case "jsonlines":
		return parser.ParseJSONLines(data)
	default:
		panic("unexpected fixture kind")
	}
}

func TestJSONParserRejectsOutOfRangeIntegersAndHonorsCancellation(t *testing.T) {
	parser := object_storage.NewJSONParser(nil)
	if _, err := parser.ParseJSONDocument([]byte("{\"id\":9223372036854775808}")); err == nil || !strings.Contains(err.Error(), "int64 range") {
		t.Fatalf("out-of-range integer error = %v, want int64 range rejection", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := parser.ParseJSONArrayContext(ctx, []byte("[{\"id\":1}]")); err != context.Canceled {
		t.Fatalf("cancelled array parse error = %v, want context.Canceled", err)
	}
}
