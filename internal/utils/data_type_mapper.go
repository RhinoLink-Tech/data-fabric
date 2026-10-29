package utils

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// DataTypeMapper maps database-specific types to standardized types
type DataTypeMapper struct{}

func (dtm *DataTypeMapper) mapPrestoCompatibleType(columnType string) model.StandardizedType {
	switch {
	case strings.HasPrefix(columnType, "TINYINT"), strings.HasPrefix(columnType, "SMALLINT"), strings.HasPrefix(columnType, "INTEGER"):
		return model.TypeInteger
	case strings.HasPrefix(columnType, "BIGINT"):
		return model.TypeBigInt
	case strings.HasPrefix(columnType, "REAL"):
		return model.TypeFloat
	case strings.HasPrefix(columnType, "DOUBLE"):
		return model.TypeDouble
	case strings.HasPrefix(columnType, "DECIMAL"), strings.HasPrefix(columnType, "NUMERIC"):
		return model.TypeDecimal
	case strings.HasPrefix(columnType, "VARCHAR"), strings.HasPrefix(columnType, "CHAR"),
		strings.HasPrefix(columnType, "IPADDRESS"), strings.HasPrefix(columnType, "GEOMETRY"),
		strings.HasPrefix(columnType, "SPHERICALGEOGRAPHY"), strings.HasPrefix(columnType, "INTERVAL"):
		return model.TypeString
	case strings.HasPrefix(columnType, "VARBINARY"):
		return model.TypeBinary
	case strings.HasPrefix(columnType, "BOOLEAN"):
		return model.TypeBoolean
	case strings.HasPrefix(columnType, "DATE"):
		return model.TypeDate
	case strings.HasPrefix(columnType, "TIMESTAMP"):
		return model.TypeTimestamp
	case strings.HasPrefix(columnType, "TIME"):
		return model.TypeTime
	case strings.HasPrefix(columnType, "JSON"), strings.HasPrefix(columnType, "MAP"),
		strings.HasPrefix(columnType, "BINGTILE"), strings.HasPrefix(columnType, "KDBTREE"):
		return model.TypeJSON
	case strings.HasPrefix(columnType, "ARRAY"):
		return model.TypeArray
	case strings.HasPrefix(columnType, "ROW"):
		return model.TypeStruct
	case strings.HasPrefix(columnType, "UUID"):
		return model.TypeUUID
	default:
		return model.TypeUnknown
	}
}

// NewDataTypeMapper creates a new DataTypeMapper instance
func NewDataTypeMapper() *DataTypeMapper {
	return &DataTypeMapper{}
}

// CoversStandardTypeMapping reports whether MapToStandardType has
// database-specific mapping rules for dbType. Execution paths that need a
// generic fallback for the remaining families route on this instead of
// duplicating the supported list (#260).
func (dtm *DataTypeMapper) CoversStandardTypeMapping(dbType model.DatabaseType) bool {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB, model.DatabaseTypePostgreSQL,
		model.DatabaseTypeOracle, model.DatabaseTypeTrino, model.DatabaseTypePresto,
		model.DatabaseTypeOpenLooKeng, model.DatabaseTypeMongoDB, model.DatabaseTypeApacheDruid,
		model.DatabaseTypeS3CSV:
		return true
	default:
		return false
	}
}

// MapToStandardType maps a database-specific type to a standard type
func (dtm *DataTypeMapper) MapToStandardType(dbType model.DatabaseType, dbColumnType string, nullable bool) model.StandardizedType {
	if !dtm.CoversStandardTypeMapping(dbType) {
		return model.TypeUnknown
	}

	// Normalize the column type (convert to uppercase, remove size constraints)
	normalizedType := dtm.normalizeColumnType(dbColumnType)

	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB:
		return dtm.mapMySQLType(normalizedType, nullable)
	case model.DatabaseTypePostgreSQL:
		return dtm.mapPostgreSQLType(normalizedType, nullable)
	case model.DatabaseTypeOracle:
		// Oracle NUMBER needs its scale before size constraints are removed.
		return dtm.mapOracleType(dbColumnType, nullable)
	case model.DatabaseTypeTrino, model.DatabaseTypePresto, model.DatabaseTypeOpenLooKeng,
		model.DatabaseTypeMongoDB, model.DatabaseTypeApacheDruid:
		return dtm.mapPrestoCompatibleType(normalizedType)
	case model.DatabaseTypeS3CSV, model.DatabaseTypeMinIOCSV:
		return dtm.mapPrestoCompatibleType(normalizedType)
	default:
		// Trino/Presto/OpenLooKeng, MongoDB, Apache Druid, and S3 CSV all
		// share the Presto-compatible mapping.
		return dtm.mapPrestoCompatibleType(normalizedType)
	}
}

// mapMySQLType maps MySQL/MariaDB types to standard types
func (dtm *DataTypeMapper) mapMySQLType(columnType string, nullable bool) model.StandardizedType {
	switch {
	case strings.HasPrefix(columnType, "TINYINT"):
		return model.TypeInteger
	case strings.HasPrefix(columnType, "SMALLINT"):
		return model.TypeInteger
	case strings.HasPrefix(columnType, "MEDIUMINT"):
		return model.TypeInteger
	case strings.HasPrefix(columnType, "INT"):
		return model.TypeInteger
	case strings.HasPrefix(columnType, "INTEGER"):
		return model.TypeInteger
	case strings.HasPrefix(columnType, "BIGINT"):
		return model.TypeBigInt
	case strings.HasPrefix(columnType, "FLOAT"):
		return model.TypeFloat
	case strings.HasPrefix(columnType, "DOUBLE"):
		return model.TypeDouble
	case strings.HasPrefix(columnType, "DECIMAL"):
		return model.TypeDecimal
	case strings.HasPrefix(columnType, "NUMERIC"):
		return model.TypeDecimal
	case columnType == "DATE":
		return model.TypeDate
	case columnType == "TIME":
		return model.TypeTime
	case columnType == "DATETIME":
		return model.TypeDateTime
	case columnType == "TIMESTAMP":
		return model.TypeTimestamp
	case columnType == "YEAR":
		return model.TypeInteger
	case strings.HasPrefix(columnType, "CHAR"):
		return model.TypeString
	case strings.HasPrefix(columnType, "VARCHAR"):
		return model.TypeString
	case strings.HasPrefix(columnType, "TINYTEXT"):
		return model.TypeText
	case strings.HasPrefix(columnType, "TEXT"):
		return model.TypeText
	case strings.HasPrefix(columnType, "MEDIUMTEXT"):
		return model.TypeText
	case strings.HasPrefix(columnType, "LONGTEXT"):
		return model.TypeText
	case strings.HasPrefix(columnType, "BINARY"):
		return model.TypeBinary
	case strings.HasPrefix(columnType, "VARBINARY"):
		return model.TypeBinary
	case strings.HasPrefix(columnType, "TINYBLOB"):
		return model.TypeBinary
	case strings.HasPrefix(columnType, "BLOB"):
		return model.TypeBinary
	case strings.HasPrefix(columnType, "MEDIUMBLOB"):
		return model.TypeBinary
	case strings.HasPrefix(columnType, "LONGBLOB"):
		return model.TypeBinary
	case columnType == "BOOLEAN":
		return model.TypeBoolean
	case columnType == "BOOL":
		return model.TypeBoolean
	case columnType == "BIT":
		return model.TypeBoolean
	case strings.HasPrefix(columnType, "ENUM"):
		return model.TypeString
	case strings.HasPrefix(columnType, "SET"):
		return model.TypeString
	case columnType == "JSON":
		return model.TypeJSON
	default:
		return model.TypeUnknown
	}
}

// mapPostgreSQLType maps PostgreSQL types to standard types
func (dtm *DataTypeMapper) mapPostgreSQLType(columnType string, nullable bool) model.StandardizedType {
	// normalizeColumnType uppercases its input, so the literals below have to be
	// matched case-insensitively.
	columnType = strings.ToLower(columnType)

	switch {
	case columnType == "smallint":
		return model.TypeInteger
	case columnType == "integer":
		return model.TypeInteger
	case columnType == "bigint":
		return model.TypeBigInt
	case columnType == "smallserial":
		return model.TypeInteger
	case columnType == "serial":
		return model.TypeInteger
	case columnType == "bigserial":
		return model.TypeBigInt
	case columnType == "decimal":
		return model.TypeDecimal
	case columnType == "numeric":
		return model.TypeDecimal
	case columnType == "real":
		return model.TypeFloat
	case columnType == "double precision":
		return model.TypeDouble
	case columnType == "smallmoney":
		return model.TypeDecimal
	case columnType == "money":
		return model.TypeDecimal
	case columnType == "character varying" || strings.HasPrefix(columnType, "varchar"):
		return model.TypeString
	case columnType == "character" || strings.HasPrefix(columnType, "char"):
		return model.TypeString
	case columnType == "text":
		return model.TypeText
	case columnType == "bytea":
		return model.TypeBinary
	case columnType == "timestamp":
		return model.TypeTimestamp
	case columnType == "timestamptz":
		return model.TypeTimestamp
	case columnType == "date":
		return model.TypeDate
	case columnType == "time":
		return model.TypeTime
	case columnType == "timetz":
		return model.TypeTime
	case columnType == "boolean":
		return model.TypeBoolean
	case columnType == "bool":
		return model.TypeBoolean
	case columnType == "uuid":
		return model.TypeUUID
	case columnType == "xml":
		return model.TypeText
	case columnType == "json":
		return model.TypeJSON
	case columnType == "jsonb":
		return model.TypeJSON
	case strings.HasPrefix(columnType, "int"):
		return model.TypeInteger
	case strings.HasPrefix(columnType, "float"):
		return model.TypeFloat
	default:
		return model.TypeUnknown
	}
}

// mapOracleType maps Oracle types to standard types
func (dtm *DataTypeMapper) mapOracleType(columnType string, nullable bool) model.StandardizedType {
	hasScale := strings.Contains(columnType, ",")
	columnType = dtm.normalizeColumnType(columnType)

	switch {
	case strings.HasPrefix(columnType, "NUMBER"):
		// Check if it has precision and scale
		if hasScale {
			return model.TypeDecimal
		}
		return model.TypeInteger
	case columnType == "BINARY_FLOAT":
		return model.TypeFloat
	case columnType == "BINARY_DOUBLE":
		return model.TypeDouble
	case columnType == "FLOAT":
		return model.TypeFloat
	case strings.HasPrefix(columnType, "CHAR"):
		return model.TypeString
	case strings.HasPrefix(columnType, "VARCHAR2"):
		return model.TypeString
	case strings.HasPrefix(columnType, "NCHAR"):
		return model.TypeString
	case strings.HasPrefix(columnType, "NVARCHAR2"):
		return model.TypeString
	case columnType == "CLOB":
		return model.TypeText
	case columnType == "NCLOB":
		return model.TypeText
	case columnType == "BLOB":
		return model.TypeBinary
	case columnType == "BFILE":
		return model.TypeBinary
	case columnType == "RAW":
		return model.TypeBinary
	case columnType == "LONG RAW":
		return model.TypeBinary
	case columnType == "DATE":
		return model.TypeDate
	case columnType == "TIMESTAMP":
		return model.TypeTimestamp
	case strings.HasPrefix(columnType, "TIMESTAMP WITH TIME ZONE"):
		return model.TypeTimestamp
	case strings.HasPrefix(columnType, "TIMESTAMP WITH LOCAL TIME ZONE"):
		return model.TypeTimestamp
	case columnType == "INTERVAL YEAR TO MONTH":
		return model.TypeString
	case columnType == "INTERVAL DAY TO SECOND":
		return model.TypeString
	case columnType == "ROWID":
		return model.TypeString
	case columnType == "UROWID":
		return model.TypeString
	default:
		return model.TypeUnknown
	}
}

// normalizeColumnType normalizes the column type by removing size constraints
func (dtm *DataTypeMapper) normalizeColumnType(columnType string) string {
	// Convert to uppercase
	normalized := strings.ToUpper(strings.TrimSpace(columnType))

	// Remove parentheses and contents (size constraints)
	if start := strings.Index(normalized, "("); start != -1 {
		if end := strings.Index(normalized[start:], ")"); end != -1 {
			normalized = normalized[:start] + normalized[start+end+1:]
		}
	}

	// Remove extra spaces
	normalized = strings.Join(strings.Fields(normalized), " ")

	return normalized
}

// StandardizeValue standardizes a value based on its type
func (dtm *DataTypeMapper) StandardizeValue(value interface{}, targetType model.StandardizedType) (interface{}, error) {
	if value == nil {
		return nil, nil
	}

	switch targetType {
	case model.TypeInteger, model.TypeBigInt:
		return dtm.convertToInteger(value)
	case model.TypeFloat, model.TypeDouble, model.TypeDecimal:
		return dtm.convertToFloat(value)
	case model.TypeString, model.TypeText:
		return dtm.convertToString(value)
	case model.TypeBoolean:
		return dtm.convertToBoolean(value)
	case model.TypeDate:
		return dtm.convertToDate(value)
	case model.TypeTime, model.TypeDateTime, model.TypeTimestamp:
		return dtm.convertToTime(value)
	case model.TypeBinary:
		return dtm.convertToBinary(value)
	case model.TypeUUID:
		return dtm.convertToUUID(value)
	case model.TypeJSON:
		return dtm.convertToString(value) // JSON as string for now
	default:
		return value, nil
	}
}

// Helper conversion methods
func (dtm *DataTypeMapper) convertToInteger(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case int, int8, int16, int32, int64:
		return v, nil
	case uint, uint8, uint16, uint32, uint64:
		return v, nil
	case float32:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	case []byte:
		return strconv.ParseInt(string(v), 10, 64)
	default:
		return nil, fmt.Errorf("cannot convert %T to integer", value)
	}
}

func (dtm *DataTypeMapper) convertToFloat(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case int:
		return float64(v), nil
	case int8:
		return float64(v), nil
	case int16:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case uint:
		return float64(v), nil
	case uint8:
		return float64(v), nil
	case uint16:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case float32:
		return float64(v), nil
	case float64:
		return v, nil
	case string:
		return strconv.ParseFloat(v, 64)
	case []byte:
		return strconv.ParseFloat(string(v), 64)
	default:
		return nil, fmt.Errorf("cannot convert %T to float", value)
	}
}

func (dtm *DataTypeMapper) convertToString(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	case time.Time:
		return v.Format(time.RFC3339), nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

func (dtm *DataTypeMapper) convertToBoolean(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case int:
		return v != 0, nil
	case int8:
		return v != 0, nil
	case int16:
		return v != 0, nil
	case int32:
		return v != 0, nil
	case int64:
		return v != 0, nil
	case uint:
		return v != 0, nil
	case uint8:
		return v != 0, nil
	case uint16:
		return v != 0, nil
	case uint32:
		return v != 0, nil
	case uint64:
		return v != 0, nil
	case float32:
		return v != 0, nil
	case float64:
		return v != 0, nil
	case string:
		lower := strings.ToLower(v)
		return lower == "true" || lower == "1" || lower == "t" || lower == "yes", nil
	case []byte:
		lower := strings.ToLower(string(v))
		return lower == "true" || lower == "1" || lower == "t" || lower == "yes", nil
	default:
		return nil, fmt.Errorf("cannot convert %T to boolean", value)
	}
}

func (dtm *DataTypeMapper) convertToDate(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case time.Time:
		return v, nil
	case string:
		return time.Parse("2006-01-02", v)
	case []byte:
		return time.Parse("2006-01-02", string(v))
	default:
		return nil, fmt.Errorf("cannot convert %T to date", value)
	}
}

func (dtm *DataTypeMapper) convertToTime(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case time.Time:
		return v, nil
	case string:
		// Try multiple time formats
		formats := []string{
			time.RFC3339,
			time.RFC3339Nano,
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05.999",
		}
		for _, format := range formats {
			if t, err := time.Parse(format, v); err == nil {
				return t, nil
			}
		}
		return nil, fmt.Errorf("cannot parse time string: %s", v)
	case []byte:
		return dtm.convertToTime(string(v))
	default:
		return nil, fmt.Errorf("cannot convert %T to time", value)
	}
}

func (dtm *DataTypeMapper) convertToBinary(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to binary", value)
	}
}

func (dtm *DataTypeMapper) convertToUUID(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case string:
		if _, err := uuid.Parse(v); err != nil {
			return nil, fmt.Errorf("invalid UUID %q: %w", v, err)
		}
		return v, nil
	case []byte:
		s := string(v)
		if _, err := uuid.Parse(s); err != nil {
			return nil, fmt.Errorf("invalid UUID %q: %w", s, err)
		}
		return s, nil
	default:
		return nil, fmt.Errorf("cannot convert %T to UUID", value)
	}
}
