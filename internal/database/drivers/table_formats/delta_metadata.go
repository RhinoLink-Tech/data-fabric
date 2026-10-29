package table_formats

import (
	"encoding/json"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/common"
	"time"
)

// DeltaLogParser handles Delta Lake log (JSON) parsing
type DeltaLogParser struct{}

// NewDeltaLogParser creates a new Delta log parser
func NewDeltaLogParser() *DeltaLogParser {
	return &DeltaLogParser{}
}

// ParseDeltaLog parses a Delta log JSON file
func (p *DeltaLogParser) ParseDeltaLog(logData []byte) (*DeltaLogEntry, error) {
	var entry DeltaLogEntry
	if err := json.Unmarshal(logData, &entry); err != nil {
		return nil, fmt.Errorf("failed to parse Delta log: %w", err)
	}
	return &entry, nil
}

// ParseTableMetadata parses Delta table metadata into standard schema
func (p *DeltaLogParser) ParseTableMetadata(deltaMeta *DeltaTableDetails, tableName string) (*common.DataSourceSchema, error) {
	schema := &common.DataSourceSchema{
		Tables: make(map[string]*common.TableSchema),
	}

	tableSchema := &common.TableSchema{
		Name:       tableName,
		Type:       "TABLE",
		Schema:     deltaMeta.SchemaName,
		Columns:    make([]common.ColumnSchema, 0),
		Properties: make(map[string]interface{}),
	}

	// Parse columns
	for _, col := range deltaMeta.Columns {
		column := common.ColumnSchema{
			Name:     col.Name,
			Type:     ConvertDeltaTypeToStandardType(col.TypeText),
			Nullable: col.Nullable,
			Comment:  col.Comment,
		}
		tableSchema.Columns = append(tableSchema.Columns, column)
	}

	// Add properties
	tableSchema.Properties["storage-location"] = deltaMeta.StorageLocation
	tableSchema.Properties["format"] = deltaMeta.Format.Name
	tableSchema.Properties["created-at"] = time.UnixMilli(deltaMeta.CreatedAt).Format(time.RFC3339)
	tableSchema.Properties["created-by"] = deltaMeta.CreatedBy

	for k, v := range deltaMeta.Properties {
		tableSchema.Properties[k] = v
	}

	schema.Tables[tableName] = tableSchema

	return schema, nil
}

// GetCommitAtTime retrieves the commit version for a specific timestamp
func (p *DeltaLogParser) GetCommitAtTime(commits []DeltaCommitInfo, timestamp time.Time) (int64, error) {
	targetMs := timestamp.UnixMilli()

	// Find the commit at or before the target timestamp
	var bestCommit *DeltaCommitInfo
	for i := range commits {
		commit := &commits[i]
		if commit.Timestamp.UnixMilli() <= targetMs {
			if bestCommit == nil || commit.Timestamp.After(bestCommit.Timestamp) {
				bestCommit = commit
			}
		}
	}

	if bestCommit == nil {
		return 0, fmt.Errorf("no commit found for timestamp: %v", timestamp)
	}

	return bestCommit.Version, nil
}

// GetLatestVersion retrieves the latest version from commits
func (p *DeltaLogParser) GetLatestVersion(commits []DeltaCommitInfo) (int64, error) {
	if len(commits) == 0 {
		return 0, fmt.Errorf("no commits available")
	}

	// Commits come from DESCRIBE HISTORY, which is newest-first.
	latest := commits[0]
	return latest.Version, nil
}

// DeltaLogEntry represents a Delta log entry
type DeltaLogEntry struct {
	CommitInfo DeltaCommitInfo   `json:"commitInfo"`
	Protocol   DeltaProtocol     `json:"protocol"`
	MetaData   *DeltaMetaData    `json:"metaData,omitempty"`
	Add        []DeltaFileAction `json:"add,omitempty"`
	Remove     []DeltaFileAction `json:"remove,omitempty"`
}

// DeltaProtocol represents the Delta protocol version
type DeltaProtocol struct {
	MinReaderVersion int `json:"minReaderVersion"`
	MinWriterVersion int `json:"minWriterVersion"`
}

// DeltaMetaData represents table metadata in Delta log
type DeltaMetaData struct {
	ID               string            `json:"id"`
	Format           DeltaFormat       `json:"format"`
	SchemaString     string            `json:"schemaString"`
	PartitionColumns []string          `json:"partitionColumns"`
	Configuration    map[string]string `json:"configuration"`
	CreatedTime      int64             `json:"createdTime"`
}

// DeltaFileAction represents a file add/remove action
type DeltaFileAction struct {
	Path             string            `json:"path"`
	Size             int64             `json:"size"`
	ModificationTime int64             `json:"modificationTime"`
	DataChange       bool              `json:"dataChange"`
	Stats            string            `json:"stats,omitempty"`
	PartitionValues  map[string]string `json:"partitionValues,omitempty"`
	Tags             map[string]string `json:"tags,omitempty"`
}

// ParseStats parses the stats field of a file action
func (p *DeltaLogParser) ParseStats(statsJSON string) (*DeltaFileStats, error) {
	if statsJSON == "" {
		return nil, fmt.Errorf("stats are empty")
	}

	var stats DeltaFileStats
	if err := json.Unmarshal([]byte(statsJSON), &stats); err != nil {
		return nil, fmt.Errorf("failed to parse stats: %w", err)
	}

	return &stats, nil
}

// DeltaFileStats contains file-level statistics
type DeltaFileStats struct {
	NumRecords int64                  `json:"numRecords"`
	MinValues  map[string]interface{} `json:"minValues"`
	MaxValues  map[string]interface{} `json:"maxValues"`
	NullCount  map[string]int64       `json:"nullCount"`
}

// GetPartitionColumns returns partition columns from metadata
func (p *DeltaLogParser) GetPartitionColumns(metaData *DeltaMetaData) []string {
	if metaData == nil {
		return []string{}
	}
	return metaData.PartitionColumns
}

// CalculateTableSize calculates total table size from file actions
func (p *DeltaLogParser) CalculateTableSize(add []DeltaFileAction) int64 {
	var totalSize int64
	for _, file := range add {
		// Only count active files (not removed)
		if file.DataChange {
			totalSize += file.Size
		}
	}
	return totalSize
}

// EstimateRowCount estimates row count from file stats
func (p *DeltaLogParser) EstimateRowCount(add []DeltaFileAction) int64 {
	var totalCount int64
	for _, file := range add {
		stats, err := p.ParseStats(file.Stats)
		if err == nil && stats.NumRecords > 0 {
			totalCount += stats.NumRecords
		}
	}
	return totalCount
}

// BuildTimeTravelQuery builds a query for time travel
func (p *DeltaLogParser) BuildTimeTravelQuery(tableName string, version int64) string {
	// Delta Lake time travel syntax
	// SELECT * FROM table VERSION AS OF version
	// SELECT * FROM table TIMESTAMP AS OF timestamp
	return fmt.Sprintf("SELECT * FROM %s VERSION AS OF %d", tableName, version)
}

// GetSchemaEvolutionHistory extracts schema evolution history from commits
func (p *DeltaLogParser) GetSchemaEvolutionHistory(commits []DeltaCommitInfo) []DeltaSchemaChange {
	history := make([]DeltaSchemaChange, 0)

	for _, commit := range commits {
		// Check if this commit involved schema changes
		if commit.Operation == "WRITE" || commit.Operation == "CREATE TABLE" {
			change := DeltaSchemaChange{
				Version:   commit.Version,
				Timestamp: commit.Timestamp,
				Operation: commit.Operation,
				UserName:  commit.UserName,
			}
			history = append(history, change)
		}
	}

	return history
}

// DeltaSchemaChange represents a schema evolution event
type DeltaSchemaChange struct {
	Version   int64
	Timestamp time.Time
	Operation string
	UserName  string
}

// ParsePartitionSpec parses partition columns into a spec
func (p *DeltaLogParser) ParsePartitionSpec(partitionCols []string) []DeltaPartitionField {
	fields := make([]DeltaPartitionField, len(partitionCols))
	for i, col := range partitionCols {
		fields[i] = DeltaPartitionField{
			SourceID:  i,
			Name:      col,
			Transform: "value", // Delta uses partition by value
		}
	}
	return fields
}

// Delta-specific partition field definition
type DeltaPartitionField struct {
	SourceID  int
	Name      string
	Transform string
}

// GetActiveVersions returns active versions within retention period
func (p *DeltaLogParser) GetActiveVersions(commits []DeltaCommitInfo, retention time.Duration) []int64 {
	cutoffTime := time.Now().Add(-retention)
	active := make([]int64, 0)

	for _, commit := range commits {
		if commit.Timestamp.After(cutoffTime) {
			active = append(active, commit.Version)
		}
	}

	return active
}

// ValidateDeltaTableConfig validates Delta table configuration
func (p *DeltaLogParser) ValidateDeltaTableConfig(config map[string]string) error {
	// Validate Delta Lake specific properties
	// TODO: Implement validation logic
	return nil
}

// GetOperationStatistics calculates statistics by operation type
func (p *DeltaLogParser) GetOperationStatistics(commits []DeltaCommitInfo) map[string]int64 {
	stats := make(map[string]int64)

	for _, commit := range commits {
		stats[commit.Operation]++
	}

	return stats
}

// GetCommitTimeline returns a timeline of commits
func (p *DeltaLogParser) GetCommitTimeline(commits []DeltaCommitInfo) []CommitTimelineEntry {
	timeline := make([]CommitTimelineEntry, len(commits))
	for i, commit := range commits {
		timeline[i] = CommitTimelineEntry{
			Version:   commit.Version,
			Timestamp: commit.Timestamp,
			Operation: commit.Operation,
		}
	}
	return timeline
}

// CommitTimelineEntry represents a commit on a timeline
type CommitTimelineEntry struct {
	Version   int64
	Timestamp time.Time
	Operation string
}

// CalculateVacuumCandidateFiles identifies files that can be vacuumed
func (p *DeltaLogParser) CalculateVacuumCandidateFiles(add, remove []DeltaFileAction, retentionHours float64) []string {
	cutoffTime := time.Now().Add(-time.Duration(retentionHours) * time.Hour)
	candidates := make([]string, 0)

	// Build a set of active files
	activeFiles := make(map[string]bool)
	for _, file := range add {
		activeFiles[file.Path] = true
	}
	for _, file := range remove {
		activeFiles[file.Path] = false
	}

	// Check for old files
	for _, file := range add {
		if !activeFiles[file.Path] {
			modTime := time.UnixMilli(file.ModificationTime)
			if modTime.Before(cutoffTime) {
				candidates = append(candidates, file.Path)
			}
		}
	}

	return candidates
}
