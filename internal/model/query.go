package model

import (
	"time"
)

// QueryRequest represents a query execution request (for backward compatibility)
type QueryRequest struct {
	DataSourceID  string        `json:"dataSourceId" validate:"required,uuid4"`
	SQL           string        `json:"sql" validate:"required"`
	SourceDialect string        `json:"sourceDialect,omitempty"` // Source SQL dialect for translation (e.g., mysql, trino, presto, openlookeng, postgres)
	Parameters    []interface{} `json:"parameters"`
	Limit         int           `json:"limit" validate:"omitempty,min=1,max=10000"`
	Offset        int           `json:"offset" validate:"omitempty,min=0"`
	Timeout       int           `json:"timeout" validate:"omitempty,min=1,max=300"` // timeout in seconds
}

// FetchQueryRequest represents a fetch query request for batch data retrieval
type FetchQueryRequest struct {
	DataSourceID  string        `json:"dataSourceId" validate:"required,uuid4"`
	SQL           string        `json:"sql" validate:"required"`
	SourceDialect string        `json:"sourceDialect,omitempty"` // Source SQL dialect for translation (e.g., mysql, trino, presto, openlookeng, postgres)
	Parameters    []interface{} `json:"parameters,omitempty"`    // Ordered values bound by the driver, never interpolated into the SQL text (#21)
	Type          int           `json:"type"`                    // 1-同步查询 2-流式查询
	BatchSize     int           `json:"batch_size,omitempty"`    // 当前批次数据的预期条数，范围：1-10000，默认10000
	Timeout       int           `json:"timeout,omitempty"`       // 获取第一批数据的预期时间，单位：秒，范围：1-1800，流式查询生效
}

// FetchQueryResponse represents the response for a fetch query
// When a streaming cursor is used, `NextURI` will be populated and can be
// called to retrieve the next batch of rows. If `NextURI` is empty, the
// result set is exhausted.
type FetchQueryResponse struct {
	QueryID       string           `json:"queryId"`
	Slug          string           `json:"slug"`
	Token         string           `json:"token"`
	NextURI       string           `json:"nextUri,omitempty"`
	ExecutionMode string           `json:"executionMode,omitempty"`
	Columns       []ColumnInfo     `json:"columns"`
	Entries       []*[]interface{} `json:"entries"`
	TotalCount    int              `json:"totalCount"`
}

// QueryResponse represents the response for a query execution
type QueryResponse struct {
	Columns  []ColumnInfo    `json:"columns"`
	Rows     [][]interface{} `json:"rows"`
	NextURI  string          `json:"nextUri,omitempty"`
	Metadata QueryMetadata   `json:"metadata"`
	Success  bool            `json:"success"`
	Error    *QueryError     `json:"error,omitempty"`
}

// ColumnInfo represents column information in the result set
type ColumnInfo struct {
	Name         string           `json:"name"`
	Type         StandardizedType `json:"type"`
	Nullable     bool             `json:"nullable"`
	NestedFields []ColumnInfo     `json:"nestedFields,omitempty"` // For nested structures like structs/records
}

// QueryMetadata contains metadata about the query execution
type QueryMetadata struct {
	RowCount        int       `json:"rowCount"`
	ExecutionTimeMs int64     `json:"executionTimeMs"`
	DataSourceID    string    `json:"dataSourceId"`
	DatabaseType    string    `json:"databaseType"`
	HasMore         bool      `json:"hasMore"`
	Limit           int       `json:"limit"`
	Offset          int       `json:"offset"`
	ExecutedAt      time.Time `json:"executedAt"`
}

// QueryError represents query-specific error information
type QueryError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	SQL     string `json:"sql,omitempty"`
	Details string `json:"details,omitempty"`
}

// StandardizedResult represents a standardized query result across different database types
type StandardizedResult struct {
	Columns []StandardizedColumn `json:"columns"`
	Rows    []StandardizedRow    `json:"rows"`
	Count   int64                `json:"count"`
	HasMore bool                 `json:"hasMore"`
}

// StandardizedColumn represents a standardized column definition
type StandardizedColumn struct {
	Name     string           `json:"name"`
	Type     StandardizedType `json:"type"`
	Nullable bool             `json:"nullable"`
	Original string           `json:"original"` // Original database type
}

// StandardizedRow represents a row of data with standardized types
type StandardizedRow []interface{}

// StandardizedType represents standardized data types across databases
type StandardizedType string

const (
	TypeInteger   StandardizedType = "integer"
	TypeBigInt    StandardizedType = "bigint"
	TypeInt64     StandardizedType = "int64" // Added for compatibility
	TypeFloat     StandardizedType = "float"
	TypeFloat64   StandardizedType = "float64" // Added for compatibility
	TypeDouble    StandardizedType = "double"
	TypeDecimal   StandardizedType = "decimal"
	TypeString    StandardizedType = "string"
	TypeText      StandardizedType = "text"
	TypeBoolean   StandardizedType = "boolean"
	TypeDate      StandardizedType = "date"
	TypeTime      StandardizedType = "time"
	TypeDateTime  StandardizedType = "datetime"
	TypeTimestamp StandardizedType = "timestamp"
	TypeBinary    StandardizedType = "binary"
	TypeJSON      StandardizedType = "json"
	TypeUUID      StandardizedType = "uuid"
	TypeArray     StandardizedType = "array"
	TypeStruct    StandardizedType = "struct"
	TypeGeography StandardizedType = "geography"
	TypeUnknown   StandardizedType = "unknown"
)

// Alias constants for backward compatibility
const (
	StandardizedTypeString    StandardizedType = TypeString
	StandardizedTypeInteger   StandardizedType = TypeInteger
	StandardizedTypeBigInt    StandardizedType = TypeBigInt
	StandardizedTypeInt64     StandardizedType = TypeInt64 // Added alias
	StandardizedTypeFloat     StandardizedType = TypeFloat
	StandardizedTypeFloat64   StandardizedType = TypeFloat64 // Added alias
	StandardizedTypeDouble    StandardizedType = TypeDouble
	StandardizedTypeDecimal   StandardizedType = TypeDecimal
	StandardizedTypeText      StandardizedType = TypeText
	StandardizedTypeBoolean   StandardizedType = TypeBoolean
	StandardizedTypeDate      StandardizedType = TypeDate
	StandardizedTypeTime      StandardizedType = TypeTime
	StandardizedTypeDateTime  StandardizedType = TypeDateTime
	StandardizedTypeTimestamp StandardizedType = TypeTimestamp
	StandardizedTypeBinary    StandardizedType = TypeBinary
	StandardizedTypeJSON      StandardizedType = TypeJSON
	StandardizedTypeUUID      StandardizedType = TypeUUID
	StandardizedTypeArray     StandardizedType = TypeArray
	StandardizedTypeStruct    StandardizedType = TypeStruct
	StandardizedTypeGeography StandardizedType = TypeGeography
	StandardizedTypeUnknown   StandardizedType = TypeUnknown
)

// TableSchema represents the schema of a single table
type TableSchema struct {
	Name        string                 `json:"name"`
	Schema      string                 `json:"schema,omitempty"` // Database/schema name
	Type        string                 `json:"type,omitempty"`   // TABLE, VIEW, etc.
	Columns     []ColumnInfo           `json:"columns"`
	PrimaryKey  []string               `json:"primaryKey,omitempty"`
	Indexes     []IndexSchema          `json:"indexes,omitempty"`
	ForeignKeys []ForeignKeySchema     `json:"foreignKeys,omitempty"`
	Properties  map[string]interface{} `json:"properties,omitempty"` // Table-specific properties
}

// IndexSchema represents an index definition
type IndexSchema struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

// ForeignKeySchema represents a foreign key definition
type ForeignKeySchema struct {
	Name         string   `json:"name"`
	Columns      []string `json:"columns"`
	ReferTable   string   `json:"referTable"`
	ReferColumns []string `json:"referColumns"`
}

// QueryStats represents query execution statistics
type QueryStats struct {
	TotalQueries      int64            `json:"totalQueries"`
	SuccessfulQueries int64            `json:"successfulQueries"`
	FailedQueries     int64            `json:"failedQueries"`
	AvgExecutionTime  float64          `json:"avgExecutionTime"`
	LastQueryTime     time.Time        `json:"lastQueryTime"`
	QueriesByType     map[string]int64 `json:"queriesByType"`
}

// IsValid checks if the QueryRequest is valid
func (qr *QueryRequest) IsValid() bool {
	if qr.DataSourceID == "" || qr.SQL == "" {
		return false
	}
	return true
}

// ApplyDefaults applies default values to the QueryRequest
func (qr *QueryRequest) ApplyDefaults() {
	if qr.Limit <= 0 {
		qr.Limit = 1000 // Default limit
	}
	if qr.Limit > 10000 {
		qr.Limit = 10000 // Max limit
	}
	if qr.Timeout <= 0 {
		qr.Timeout = 30 // Default timeout 30 seconds
	}
}
