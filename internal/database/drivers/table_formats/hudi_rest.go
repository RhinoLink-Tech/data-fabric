package table_formats

import (
	"time"
)

type HudiAuth struct {
	Username string
	Password string
	Type     string
}

// HudiConfig keeps the deprecated REST fields for source compatibility.
// New integrations must supply a storage metadata reader and/or a query-engine
// adapter because Apache Hudi does not define a generic REST query service.
type HudiConfig struct {
	BaseURL             string        `json:"-"`
	Timeout             time.Duration `json:"-"`
	Auth                *HudiAuth     `json:"-"`
	MetadataReader      HudiMetadataReader
	IncrementalExecutor HudiIncrementalExecutor
}

type HudiPartition struct {
	Path      string                 `json:"path"`
	Values    map[string]interface{} `json:"values"`
	FileCount int                    `json:"fileCount"`
	SizeBytes int64                  `json:"sizeBytes"`
}

type HudiTableMetadata struct {
	TableName       string            `json:"tableName,omitempty"`
	TableType       string            `json:"tableType"`
	RecordFields    []HudiField       `json:"fields"`
	PartitionFields []string          `json:"partitionFields"`
	KeyField        string            `json:"keyField"`
	Properties      map[string]string `json:"properties"`
	HudiVersion     string            `json:"hudiVersion"`
	CDCEnabled      bool              `json:"cdcEnabled"`
}

type HudiField struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

type HudiCommit struct {
	CommitTime   string                 `json:"commitTime"`
	CommitSeqNo  string                 `json:"commitSeqNo"`
	ActionType   string                 `json:"actionType"`
	State        string                 `json:"state"`
	TimestampMs  int64                  `json:"timestampMs"`
	Operation    string                 `json:"operation"`
	RecordsStats HudiRecordsStats       `json:"recordsStats,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

type HudiRecordsStats struct {
	NumInserts int `json:"numInserts"`
	NumUpdates int `json:"numUpdates"`
	NumDeletes int `json:"numDeletes"`
}

type HudiSavepoint struct {
	SavepointTime string `json:"savepointTime"`
	CommitTime    string `json:"commitTime"`
	Path          string `json:"path"`
}

type HudiQueryResult struct {
	Schema        HudiResultSchema `json:"schema"`
	Rows          [][]interface{}  `json:"rows"`
	Timestamp     int64            `json:"timestamp"`
	NextPageToken string           `json:"nextPageToken,omitempty"`
}

type HudiResultSchema struct {
	Fields []HudiResultField `json:"fields"`
}

type HudiResultField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func ConvertHudiTypeToStandardType(hudiType string) string {
	switch hudiType {
	case "boolean":
		return "BOOLEAN"
	case "int", "integer":
		return "INTEGER"
	case "long":
		return "BIGINT"
	case "float":
		return "FLOAT"
	case "double":
		return "DOUBLE"
	case "decimal":
		return "DECIMAL"
	case "date":
		return "DATE"
	case "timestamp":
		return "TIMESTAMP"
	case "string":
		return "VARCHAR"
	case "binary":
		return "VARBINARY"
	case "array", "ARRAY":
		return "ARRAY"
	case "map", "MAP":
		return "MAP"
	case "struct", "STRUCT", "record", "RECORD":
		return "STRUCT"
	default:
		return hudiType
	}
}

func ParseHudiTableType(tableType string) string {
	switch tableType {
	case "COPY_ON_WRITE", "COW":
		return "COPY_ON_WRITE"
	case "MERGE_ON_READ", "MOR":
		return "MERGE_ON_READ"
	default:
		return tableType
	}
}
