package utils

import (
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestMapQueryEngineTypes(t *testing.T) {
	mapper := NewDataTypeMapper()
	tests := []struct {
		columnType string
		want       model.StandardizedType
	}{
		{columnType: "bigint", want: model.TypeBigInt},
		{columnType: "integer", want: model.TypeInteger},
		{columnType: "decimal(12, 2)", want: model.TypeDecimal},
		{columnType: "double", want: model.TypeDouble},
		{columnType: "timestamp with time zone", want: model.TypeTimestamp},
		{columnType: "time with time zone", want: model.TypeTime},
		{columnType: "array(bigint)", want: model.TypeArray},
		{columnType: "row(id bigint)", want: model.TypeStruct},
		{columnType: "map(varchar, bigint)", want: model.TypeJSON},
		{columnType: "uuid", want: model.TypeUUID},
	}

	for _, dbType := range []model.DatabaseType{
		model.DatabaseTypeTrino,
		model.DatabaseTypePresto,
		model.DatabaseTypeOpenLooKeng,
		model.DatabaseTypeApacheDruid,
	} {
		for _, tt := range tests {
			t.Run(string(dbType)+"/"+tt.columnType, func(t *testing.T) {
				if got := mapper.MapToStandardType(dbType, tt.columnType, true); got != tt.want {
					t.Errorf("MapToStandardType(%s, %q) = %q, want %q", dbType, tt.columnType, got, tt.want)
				}
			})
		}
	}
}

func TestMapMongoDBSQLAdapterTypes(t *testing.T) {
	mapper := NewDataTypeMapper()
	tests := []struct {
		columnType string
		want       model.StandardizedType
	}{
		{columnType: "INTEGER", want: model.TypeInteger},
		{columnType: "BIGINT", want: model.TypeBigInt},
		{columnType: "DOUBLE", want: model.TypeDouble},
		{columnType: "BOOLEAN", want: model.TypeBoolean},
		{columnType: "TIMESTAMP", want: model.TypeTimestamp},
		{columnType: "JSON", want: model.TypeJSON},
	}
	for _, tt := range tests {
		t.Run(tt.columnType, func(t *testing.T) {
			if got := mapper.MapToStandardType(model.DatabaseTypeMongoDB, tt.columnType, true); got != tt.want {
				t.Errorf("MapToStandardType(mongodb, %q) = %q, want %q", tt.columnType, got, tt.want)
			}
		})
	}
}

func TestMapS3CSVInferredTypes(t *testing.T) {
	mapper := NewDataTypeMapper()
	tests := []struct {
		columnType string
		want       model.StandardizedType
	}{
		{columnType: "BIGINT", want: model.TypeBigInt},
		{columnType: "DOUBLE", want: model.TypeDouble},
		{columnType: "BOOLEAN", want: model.TypeBoolean},
		{columnType: "VARCHAR", want: model.TypeString},
	}
	for _, tt := range tests {
		t.Run(tt.columnType, func(t *testing.T) {
			if got := mapper.MapToStandardType(model.DatabaseTypeS3CSV, tt.columnType, true); got != tt.want {
				t.Errorf("MapToStandardType(s3_csv, %q) = %q, want %q", tt.columnType, got, tt.want)
			}
		})
	}
}

func TestMapPostgreSQLTypes(t *testing.T) {
	mapper := NewDataTypeMapper()
	tests := []struct {
		columnType string
		want       model.StandardizedType
	}{
		{columnType: "integer", want: model.TypeInteger},
		{columnType: "smallint", want: model.TypeInteger},
		{columnType: "bigint", want: model.TypeBigInt},
		{columnType: "serial", want: model.TypeInteger},
		{columnType: "bigserial", want: model.TypeBigInt},
		{columnType: "numeric(12, 2)", want: model.TypeDecimal},
		{columnType: "money", want: model.TypeDecimal},
		{columnType: "real", want: model.TypeFloat},
		{columnType: "double precision", want: model.TypeDouble},
		{columnType: "character varying(255)", want: model.TypeString},
		{columnType: "varchar(64)", want: model.TypeString},
		{columnType: "text", want: model.TypeText},
		{columnType: "xml", want: model.TypeText},
		{columnType: "bytea", want: model.TypeBinary},
		{columnType: "timestamp", want: model.TypeTimestamp},
		{columnType: "timestamptz", want: model.TypeTimestamp},
		{columnType: "date", want: model.TypeDate},
		{columnType: "time", want: model.TypeTime},
		{columnType: "boolean", want: model.TypeBoolean},
		{columnType: "uuid", want: model.TypeUUID},
		{columnType: "json", want: model.TypeJSON},
		{columnType: "jsonb", want: model.TypeJSON},
	}

	for _, tt := range tests {
		t.Run(tt.columnType, func(t *testing.T) {
			if got := mapper.MapToStandardType(model.DatabaseTypePostgreSQL, tt.columnType, true); got != tt.want {
				t.Errorf("MapToStandardType(PostgreSQL, %q) = %q, want %q", tt.columnType, got, tt.want)
			}
		})
	}
}

// TestStandardizeValueValidatesUUID verifies that values standardized as UUID
// are validated: valid strings and byte slices are accepted (and returned as a
// string), while malformed values and unsupported types return an error rather
// than being passed through unchanged.
func TestStandardizeValueValidatesUUID(t *testing.T) {
	mapper := NewDataTypeMapper()
	const validUUID = "123e4567-e89b-12d3-a456-426614174000"

	t.Run("valid string", func(t *testing.T) {
		got, err := mapper.StandardizeValue(validUUID, model.TypeUUID)
		if err != nil {
			t.Fatalf("StandardizeValue(%q, UUID) returned error: %v", validUUID, err)
		}
		if s, ok := got.(string); !ok || s != validUUID {
			t.Errorf("StandardizeValue(%q, UUID) = %#v, want %q", validUUID, got, validUUID)
		}
	})

	t.Run("valid byte slice", func(t *testing.T) {
		got, err := mapper.StandardizeValue([]byte(validUUID), model.TypeUUID)
		if err != nil {
			t.Fatalf("StandardizeValue([]byte(%q), UUID) returned error: %v", validUUID, err)
		}
		if s, ok := got.(string); !ok || s != validUUID {
			t.Errorf("StandardizeValue([]byte(%q), UUID) = %#v, want %q", validUUID, got, validUUID)
		}
	})

	t.Run("malformed string is rejected", func(t *testing.T) {
		if _, err := mapper.StandardizeValue("not-a-uuid", model.TypeUUID); err == nil {
			t.Errorf("StandardizeValue(%q, UUID) = nil error, want an error for malformed UUID", "not-a-uuid")
		}
	})

	t.Run("malformed byte slice is rejected", func(t *testing.T) {
		if _, err := mapper.StandardizeValue([]byte("nope"), model.TypeUUID); err == nil {
			t.Errorf("StandardizeValue([]byte(%q), UUID) = nil error, want an error for malformed UUID", "nope")
		}
	})

	t.Run("unsupported type is rejected", func(t *testing.T) {
		if _, err := mapper.StandardizeValue(42, model.TypeUUID); err == nil {
			t.Errorf("StandardizeValue(int, UUID) = nil error, want an error for unsupported type")
		}
	})
}

func TestStandardizeValueConvertsNumericBooleans(t *testing.T) {
	mapper := NewDataTypeMapper()
	tests := []struct {
		name  string
		value interface{}
		want  bool
	}{
		{name: "int zero", value: int(0), want: false},
		{name: "int non-zero", value: int(5), want: true},
		{name: "int8 non-zero", value: int8(1), want: true},
		{name: "int16 non-zero", value: int16(-3), want: true},
		{name: "int32 non-zero", value: int32(7), want: true},
		{name: "int64 zero", value: int64(0), want: false},
		{name: "int64 non-zero", value: int64(9), want: true},
		{name: "uint zero", value: uint(0), want: false},
		{name: "uint non-zero", value: uint(3), want: true},
		{name: "uint8 zero", value: uint8(0), want: false},
		{name: "uint32 non-zero", value: uint32(1), want: true},
		{name: "uint64 zero", value: uint64(0), want: false},
		{name: "float32 zero", value: float32(0), want: false},
		{name: "float64 zero", value: float64(0), want: false},
		{name: "float64 non-zero", value: float64(0.5), want: true},
		{name: "bool true", value: true, want: true},
		{name: "bool false", value: false, want: false},
		{name: "string true", value: "true", want: true},
		{name: "string zero", value: "0", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("StandardizeValue(%T, boolean) panicked: %v", tt.value, r)
				}
			}()

			got, err := mapper.StandardizeValue(tt.value, model.TypeBoolean)
			if err != nil {
				t.Fatalf("StandardizeValue(%T, boolean) returned error: %v", tt.value, err)
			}

			gotBool, ok := got.(bool)
			if !ok {
				t.Fatalf("StandardizeValue(%T, boolean) = %#v, want a bool", tt.value, got)
			}
			if gotBool != tt.want {
				t.Errorf("StandardizeValue(%T, boolean) = %v, want %v", tt.value, gotBool, tt.want)
			}
		})
	}
}
