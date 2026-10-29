package utils

import (
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

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

func TestMapObjectStorageQueryColumnTypes(t *testing.T) {
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
	for _, dbType := range []model.DatabaseType{model.DatabaseTypeS3CSV, model.DatabaseTypeMinIOCSV, model.DatabaseTypeMinIOParquet} {
		for _, tt := range tests {
			t.Run(string(dbType)+"/"+tt.columnType, func(t *testing.T) {
				if got := mapper.MapToStandardType(dbType, tt.columnType, true); got != tt.want {
					t.Errorf("MapToStandardType(%s, %q) = %q, want %q", dbType, tt.columnType, got, tt.want)
				}
			})
		}
	}
}

func TestMapMySQLTypes(t *testing.T) {
	mapper := NewDataTypeMapper()
	tests := []struct {
		columnType string
		want       model.StandardizedType
	}{
		{columnType: "tinyint(1)", want: model.TypeInteger},
		{columnType: "smallint", want: model.TypeInteger},
		{columnType: "mediumint", want: model.TypeInteger},
		{columnType: "int(11) unsigned", want: model.TypeInteger},
		{columnType: "integer", want: model.TypeInteger},
		{columnType: "bigint unsigned", want: model.TypeBigInt},
		{columnType: "float(24)", want: model.TypeFloat},
		{columnType: "double", want: model.TypeDouble},
		{columnType: "decimal(12, 2)", want: model.TypeDecimal},
		{columnType: "numeric(10,3)", want: model.TypeDecimal},
		{columnType: "date", want: model.TypeDate},
		{columnType: "time(6)", want: model.TypeTime},
		{columnType: "datetime(6)", want: model.TypeDateTime},
		{columnType: "timestamp(6)", want: model.TypeTimestamp},
		{columnType: "year(4)", want: model.TypeInteger},
		{columnType: "char(8)", want: model.TypeString},
		{columnType: "varchar(255)", want: model.TypeString},
		{columnType: "tinytext", want: model.TypeText},
		{columnType: "text", want: model.TypeText},
		{columnType: "mediumtext", want: model.TypeText},
		{columnType: "longtext", want: model.TypeText},
		{columnType: "binary(16)", want: model.TypeBinary},
		{columnType: "varbinary(128)", want: model.TypeBinary},
		{columnType: "tinyblob", want: model.TypeBinary},
		{columnType: "blob", want: model.TypeBinary},
		{columnType: "mediumblob", want: model.TypeBinary},
		{columnType: "longblob", want: model.TypeBinary},
		{columnType: "boolean", want: model.TypeBoolean},
		{columnType: "bool", want: model.TypeBoolean},
		{columnType: "bit(1)", want: model.TypeBoolean},
		{columnType: "enum('small','large')", want: model.TypeString},
		{columnType: "set('a','b')", want: model.TypeString},
		{columnType: "json", want: model.TypeJSON},
		{columnType: "  VaRcHaR(32)  ", want: model.TypeString},
		{columnType: "INT", want: model.TypeInteger},
		{columnType: "unknown_type", want: model.TypeUnknown},
		{columnType: "", want: model.TypeUnknown},
	}

	for _, dbType := range []model.DatabaseType{model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB} {
		for _, tt := range tests {
			t.Run(string(dbType)+"/"+tt.columnType, func(t *testing.T) {
				for _, nullable := range []bool{false, true} {
					if got := mapper.MapToStandardType(dbType, tt.columnType, nullable); got != tt.want {
						t.Errorf("MapToStandardType(%s, %q, nullable=%t) = %q, want %q", dbType, tt.columnType, nullable, got, tt.want)
					}
				}
			})
		}
	}
}

func TestMapOracleTypes(t *testing.T) {
	mapper := NewDataTypeMapper()
	tests := []struct {
		columnType string
		want       model.StandardizedType
	}{
		{columnType: "NUMBER", want: model.TypeInteger},
		{columnType: "number(10)", want: model.TypeInteger},
		{columnType: "number(12, 2)", want: model.TypeDecimal},
		{columnType: "NUMBER(10,0)", want: model.TypeDecimal},
		{columnType: "  NuMbEr (12, -2)  ", want: model.TypeDecimal},
		{columnType: "binary_float", want: model.TypeFloat},
		{columnType: "binary_double", want: model.TypeDouble},
		{columnType: "float(126)", want: model.TypeFloat},
		{columnType: "char(8)", want: model.TypeString},
		{columnType: "varchar2(255 CHAR)", want: model.TypeString},
		{columnType: "nchar(8)", want: model.TypeString},
		{columnType: "nvarchar2(255)", want: model.TypeString},
		{columnType: "clob", want: model.TypeText},
		{columnType: "nclob", want: model.TypeText},
		{columnType: "blob", want: model.TypeBinary},
		{columnType: "bfile", want: model.TypeBinary},
		{columnType: "raw(16)", want: model.TypeBinary},
		{columnType: "long raw", want: model.TypeBinary},
		{columnType: "date", want: model.TypeDate},
		{columnType: "timestamp(6)", want: model.TypeTimestamp},
		{columnType: "timestamp(6) with time zone", want: model.TypeTimestamp},
		{columnType: "timestamp(6) with local time zone", want: model.TypeTimestamp},
		{columnType: "interval year to month", want: model.TypeString},
		{columnType: "interval day to second", want: model.TypeString},
		{columnType: "rowid", want: model.TypeString},
		{columnType: "urowid", want: model.TypeString},
		{columnType: "  TiMeStAmP(6)   WITH TIME ZONE  ", want: model.TypeTimestamp},
		{columnType: "unknown_type", want: model.TypeUnknown},
		{columnType: "", want: model.TypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.columnType, func(t *testing.T) {
			for _, nullable := range []bool{false, true} {
				if got := mapper.MapToStandardType(model.DatabaseTypeOracle, tt.columnType, nullable); got != tt.want {
					t.Errorf("MapToStandardType(oracle, %q, nullable=%t) = %q, want %q", tt.columnType, nullable, got, tt.want)
				}
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
		// normalizeColumnType uppercases before mapPostgreSQLType lowers
		// again, so uppercase spellings must map the same as lowercase
		// ones (#137).
		{columnType: "INTEGER", want: model.TypeInteger},
		{columnType: "NUMERIC(10,2)", want: model.TypeDecimal},
		{columnType: "unknown_type", want: model.TypeUnknown},
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

func TestStandardizeValueConvertsIntegers(t *testing.T) {
	mapper := NewDataTypeMapper()
	tests := []struct {
		name    string
		value   interface{}
		want    interface{}
		wantErr error
		invalid bool
	}{
		{name: "nil", value: nil, want: nil},
		{name: "int", value: int(42), want: int(42)},
		{name: "int8", value: int8(-128), want: int8(-128)},
		{name: "int16", value: int16(-32768), want: int16(-32768)},
		{name: "int32", value: int32(-2147483648), want: int32(-2147483648)},
		{name: "int64", value: int64(-9223372036854775808), want: int64(-9223372036854775808)},
		{name: "uint", value: uint(42), want: uint(42)},
		{name: "uint8", value: uint8(255), want: uint8(255)},
		{name: "uint16", value: uint16(65535), want: uint16(65535)},
		{name: "uint32", value: uint32(4294967295), want: uint32(4294967295)},
		{name: "uint64", value: uint64(18446744073709551615), want: uint64(18446744073709551615)},
		{name: "positive float32 truncates", value: float32(42.75), want: int64(42)},
		{name: "negative float32 truncates", value: float32(-42.75), want: int64(-42)},
		{name: "positive float64 truncates", value: float64(42.75), want: int64(42)},
		{name: "negative float64 truncates", value: float64(-42.75), want: int64(-42)},
		{name: "zero string", value: "0", want: int64(0)},
		{name: "signed string", value: "+42", want: int64(42)},
		{name: "max int64 string", value: "9223372036854775807", want: int64(9223372036854775807)},
		{name: "min int64 bytes", value: []byte("-9223372036854775808"), want: int64(-9223372036854775808)},
		{name: "invalid string", value: "42.5", wantErr: strconv.ErrSyntax},
		{name: "invalid bytes", value: []byte("abc"), wantErr: strconv.ErrSyntax},
		{name: "empty string", value: "", wantErr: strconv.ErrSyntax},
		{name: "overflow string", value: "9223372036854775808", wantErr: strconv.ErrRange},
		{name: "underflow bytes", value: []byte("-9223372036854775809"), wantErr: strconv.ErrRange},
		{name: "unsupported bool", value: true, invalid: true},
		{name: "unsupported struct", value: struct{}{}, invalid: true},
	}

	for _, targetType := range []model.StandardizedType{model.TypeInteger, model.TypeBigInt} {
		for _, tt := range tests {
			t.Run(string(targetType)+"/"+tt.name, func(t *testing.T) {
				got, err := mapper.StandardizeValue(tt.value, targetType)
				if tt.wantErr != nil || tt.invalid {
					if err == nil {
						t.Fatalf("StandardizeValue(%#v, %s) returned no error", tt.value, targetType)
					}
					if tt.invalid && got != nil {
						t.Errorf("StandardizeValue(%#v, %s) = %#v, want nil for unsupported input", tt.value, targetType, got)
					}
					if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
						t.Errorf("StandardizeValue(%#v, %s) error = %v, want %v", tt.value, targetType, err, tt.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("StandardizeValue(%#v, %s) returned error: %v", tt.value, targetType, err)
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("StandardizeValue(%#v, %s) = %#v (%T), want %#v (%T)", tt.value, targetType, got, got, tt.want, tt.want)
				}
			})
		}
	}
}

func TestStandardizeValueConvertsFloats(t *testing.T) {
	mapper := NewDataTypeMapper()
	tests := []struct {
		name    string
		value   interface{}
		want    interface{}
		wantErr error
		invalid bool
	}{
		{name: "nil", value: nil, want: nil},
		{name: "int", value: int(42), want: float64(42)},
		{name: "int8", value: int8(-128), want: float64(-128)},
		{name: "int16", value: int16(-32768), want: float64(-32768)},
		{name: "int32", value: int32(-2147483648), want: float64(-2147483648)},
		{name: "int64", value: int64(-9223372036854775808), want: float64(-9223372036854775808)},
		{name: "uint", value: uint(42), want: float64(42)},
		{name: "uint8", value: uint8(255), want: float64(255)},
		{name: "uint16", value: uint16(65535), want: float64(65535)},
		{name: "uint32", value: uint32(4294967295), want: float64(4294967295)},
		{name: "uint64 exact boundary", value: uint64(9007199254740992), want: float64(9007199254740992)},
		{name: "float32", value: float32(-1.25), want: float64(-1.25)},
		{name: "float64", value: float64(123.456), want: float64(123.456)},
		{name: "zero string", value: "0", want: float64(0)},
		{name: "decimal string", value: "-123.45", want: float64(-123.45)},
		{name: "scientific string", value: "1.25e3", want: float64(1250)},
		{name: "decimal bytes", value: []byte("123.45"), want: float64(123.45)},
		{name: "max float64 string", value: "1.7976931348623157e308", want: float64(1.7976931348623157e308)},
		{name: "invalid string", value: "not-a-number", wantErr: strconv.ErrSyntax},
		{name: "invalid bytes", value: []byte("1.2.3"), wantErr: strconv.ErrSyntax},
		{name: "empty bytes", value: []byte{}, wantErr: strconv.ErrSyntax},
		{name: "overflow string", value: "1e309", wantErr: strconv.ErrRange},
		{name: "negative overflow bytes", value: []byte("-1e309"), wantErr: strconv.ErrRange},
		{name: "unsupported bool", value: true, invalid: true},
		{name: "unsupported struct", value: struct{}{}, invalid: true},
	}

	for _, targetType := range []model.StandardizedType{model.TypeFloat, model.TypeDouble, model.TypeDecimal} {
		for _, tt := range tests {
			t.Run(string(targetType)+"/"+tt.name, func(t *testing.T) {
				got, err := mapper.StandardizeValue(tt.value, targetType)
				if tt.wantErr != nil || tt.invalid {
					if err == nil {
						t.Fatalf("StandardizeValue(%#v, %s) returned no error", tt.value, targetType)
					}
					if tt.invalid && got != nil {
						t.Errorf("StandardizeValue(%#v, %s) = %#v, want nil for unsupported input", tt.value, targetType, got)
					}
					if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
						t.Errorf("StandardizeValue(%#v, %s) error = %v, want %v", tt.value, targetType, err, tt.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("StandardizeValue(%#v, %s) returned error: %v", tt.value, targetType, err)
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("StandardizeValue(%#v, %s) = %#v (%T), want %#v (%T)", tt.value, targetType, got, got, tt.want, tt.want)
				}
			})
		}
	}
}

func TestStandardizeValueDates(t *testing.T) {
	mapper := NewDataTypeMapper()
	date := time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC)
	dateTime := time.Date(2024, time.February, 29, 13, 14, 15, 123456789, time.FixedZone("UTC+8", 8*60*60))
	tests := []struct {
		name    string
		value   interface{}
		want    interface{}
		wantErr bool
	}{
		{name: "time.Time preserves value", value: dateTime, want: dateTime},
		{name: "date string", value: "2024-02-29", want: date},
		{name: "date bytes", value: []byte("2024-02-29"), want: date},
		{name: "nil", value: nil, want: nil},
		{name: "invalid calendar date", value: "2023-02-29", wantErr: true},
		{name: "malformed string", value: "not-a-date", wantErr: true},
		{name: "malformed bytes", value: []byte("not-a-date"), wantErr: true},
		{name: "unsupported type", value: int64(1709164800), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mapper.StandardizeValue(tt.value, model.TypeDate)
			if (err != nil) != tt.wantErr {
				t.Fatalf("StandardizeValue(%#v, date) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("StandardizeValue(%#v, date) = %#v (%T), want %#v (%T)", tt.value, got, got, tt.want, tt.want)
			}
		})
	}
}

func TestStandardizeValueTimes(t *testing.T) {
	mapper := NewDataTypeMapper()
	dateTime := time.Date(2024, time.February, 29, 13, 14, 15, 0, time.UTC)
	nanoTime := dateTime.Add(123456789 * time.Nanosecond)
	milliTime := dateTime.Add(123 * time.Millisecond)
	zonedTime := time.Date(2024, time.February, 29, 13, 14, 15, 123456789, time.FixedZone("UTC+8", 8*60*60))
	tests := []struct {
		name    string
		value   interface{}
		want    interface{}
		wantErr bool
	}{
		{name: "time.Time preserves value", value: zonedTime, want: zonedTime},
		{name: "RFC3339", value: "2024-02-29T13:14:15Z", want: dateTime},
		{name: "RFC3339Nano", value: "2024-02-29T13:14:15.123456789Z", want: nanoTime},
		{name: "space separated datetime", value: "2024-02-29 13:14:15", want: dateTime},
		{name: "T separated datetime", value: "2024-02-29T13:14:15", want: dateTime},
		{name: "millisecond datetime", value: "2024-02-29 13:14:15.123", want: milliTime},
		{name: "RFC3339 bytes", value: []byte("2024-02-29T13:14:15Z"), want: dateTime},
		{name: "RFC3339Nano bytes", value: []byte("2024-02-29T13:14:15.123456789Z"), want: nanoTime},
		{name: "space separated datetime bytes", value: []byte("2024-02-29 13:14:15"), want: dateTime},
		{name: "T separated datetime bytes", value: []byte("2024-02-29T13:14:15"), want: dateTime},
		{name: "millisecond datetime bytes", value: []byte("2024-02-29 13:14:15.123"), want: milliTime},
		{name: "nil", value: nil, want: nil},
		{name: "invalid calendar date", value: "2023-02-29T13:14:15Z", wantErr: true},
		{name: "invalid clock time", value: "2024-02-29 25:14:15", wantErr: true},
		{name: "malformed string", value: "not-a-time", wantErr: true},
		{name: "malformed bytes", value: []byte("not-a-time"), wantErr: true},
		{name: "unsupported type", value: int64(1709212455), wantErr: true},
	}

	for _, targetType := range []model.StandardizedType{model.TypeTime, model.TypeDateTime, model.TypeTimestamp} {
		for _, tt := range tests {
			t.Run(string(targetType)+"/"+tt.name, func(t *testing.T) {
				got, err := mapper.StandardizeValue(tt.value, targetType)
				if (err != nil) != tt.wantErr {
					t.Fatalf("StandardizeValue(%#v, %s) error = %v, wantErr %v", tt.value, targetType, err, tt.wantErr)
				}
				if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
					t.Errorf("StandardizeValue(%#v, %s) = %#v (%T), want %#v (%T)", tt.value, targetType, got, got, tt.want, tt.want)
				}
			})
		}
	}
}
