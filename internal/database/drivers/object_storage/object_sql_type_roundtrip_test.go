package object_storage

import (
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/utils"
)

// objectSQLTypeCases lists every standard type name objectSQLType recognises,
// aliases included, with the SQL name it must emit. Keep it in step with the
// switch in objectSQLType.
var objectSQLTypeCases = []struct{ standardType, sqlType string }{
	{"boolean", "BOOLEAN"},
	{"integer", "INTEGER"}, {"int", "INTEGER"}, {"smallint", "INTEGER"}, {"tinyint", "INTEGER"},
	{"bigint", "BIGINT"},
	{"float", "REAL"}, {"real", "REAL"},
	{"double", "DOUBLE"},
	{"decimal", "DECIMAL"},
	{"date", "DATE"},
	{"time", "TIME"},
	{"timestamp", "TIMESTAMP"},
	{"json", "JSON"},
	{"uuid", "UUID"},
	{"binary", "VARBINARY"}, {"varbinary", "VARBINARY"},
	{"varchar", "VARCHAR"},
}

// TestObjectSQLTypeRoundTrip checks that every SQL type name the object SQL
// adapter emits survives objectSQLSchemaFromResult -> objectSQLType. Derived
// SELECTs feed the emitted SQL names back through that path, so a name that
// falls through to the VARCHAR default silently changes the result type.
func TestObjectSQLTypeRoundTrip(t *testing.T) {
	for _, c := range objectSQLTypeCases {
		if got := objectSQLType(c.standardType); got != c.sqlType {
			t.Errorf("objectSQLType(%q) = %s, want %s", c.standardType, got, c.sqlType)
		}
	}

	// Feed each emitted SQL name through a derived schema, as a nested SELECT does.
	for _, c := range objectSQLTypeCases {
		result := &objectSQLResult{columns: []string{"c"}, types: []string{c.sqlType}, nullable: []bool{true}}
		schema := objectSQLSchemaFromResult(result)
		standardType := schema.Columns[0].StandardType
		if got := objectSQLType(standardType); got != c.sqlType {
			t.Errorf("round trip of %s: objectSQLType(%q) = %s, want %s", c.sqlType, standardType, got, c.sqlType)
		}
	}
}

// TestObjectSQLBinaryAndFloatReachTypedEncoding checks the step that decides
// how the gateway encodes values: after a derived round trip, VARBINARY must
// still map to the binary type (base64 on the wire) and REAL to the float type.
func TestObjectSQLBinaryAndFloatReachTypedEncoding(t *testing.T) {
	mapper := utils.NewDataTypeMapper()
	want := map[string]model.StandardizedType{
		"VARBINARY": model.TypeBinary,
		"REAL":      model.TypeFloat,
	}
	for sqlType, wantType := range want {
		result := &objectSQLResult{columns: []string{"c"}, types: []string{sqlType}, nullable: []bool{true}}
		schema := objectSQLSchemaFromResult(result)
		emittedType := objectSQLType(schema.Columns[0].StandardType)
		if got := mapper.MapToStandardType(model.DatabaseTypeS3Avro, emittedType, true); got != wantType {
			t.Errorf("encoding type of %s: MapToStandardType(%q) = %s, want %s", sqlType, emittedType, got, wantType)
		}
	}
}
