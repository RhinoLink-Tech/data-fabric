package table_formats

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// HudiIncrementalQuery provides incremental query functionality for Hudi
type HudiIncrementalQuery struct {
	driver *HudiDriver
}

// HudiIncrementalReadOptions is the format-neutral boundary to a Hudi-capable
// query engine. The instant range follows Hudi's standard semantics:
// beginInstantTime is exclusive and endInstantTime is inclusive.
type HudiIncrementalReadOptions struct {
	BasePath          string
	BeginInstantTime  string
	EndInstantTime    string
	IncrementalFormat string
	SelectedFields    []string
	OperationFilter   string
	PageSize          int
	PageToken         string
}

// HudiIncrementalExecutor adapts the Hudi read contract to an actual query
// engine. Implementations must keep the end instant fixed across pages and
// return an opaque continuation token bound to the table and request.
type HudiIncrementalExecutor interface {
	ReadIncremental(context.Context, HudiIncrementalReadOptions) (*HudiQueryResult, error)
}

var hudiFieldIdentifier = regexp.MustCompile("^[A-Za-z_][A-Za-z0-9_.$]*$")

// NewHudiIncrementalQuery creates a new incremental query handler
func NewHudiIncrementalQuery(driver *HudiDriver) *HudiIncrementalQuery {
	return &HudiIncrementalQuery{driver: driver}
}

// IncrementalQueryRequest represents an incremental query request
type IncrementalQueryRequest struct {
	BasePath          string
	BeginInstantTime  string // Start instant (exclusive)
	EndInstantTime    string // End instant (inclusive); required to pin a stable source snapshot
	QueryType         string // "incremental" or legacy "query_between_instants"
	IncrementalFormat string // "latest_state" or "cdc"
	SelectedFields    []string
	TableType         string // "COPY_ON_WRITE" or "MERGE_ON_READ"
	PageSize          int
	PageToken         string // Opaque engine continuation token
}

// IncrementalQueryResult represents incremental query results
type IncrementalQueryResult struct {
	Data          [][]interface{} `json:"data"`
	Schema        []HudiField     `json:"schema"`
	BeginTime     string          `json:"beginTime"`
	EndTime       string          `json:"endTime"`
	NextPageToken string          `json:"nextPageToken,omitempty"`
}

// QueryIncremental executes an incremental query
func (iq *HudiIncrementalQuery) QueryIncremental(ctx context.Context, req *IncrementalQueryRequest) (*IncrementalQueryResult, error) {
	options, err := BuildHudiIncrementalReadOptions(iq.driver.parser, req)
	if err != nil {
		return nil, err
	}
	if iq.driver.incrementalExecutor == nil {
		return nil, fmt.Errorf("Hudi record-level incremental reads require a configured Hudi-capable query engine")
	}
	if err := iq.driver.validateIncrementalFormat(ctx, options); err != nil {
		return nil, err
	}

	result, err := iq.driver.incrementalExecutor.ReadIncremental(ctx, options)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("Hudi query engine returned an empty result")
	}
	if err := validateHudiIncrementalPage(options, req.PageToken, result); err != nil {
		return nil, err
	}
	return &IncrementalQueryResult{
		Data:          result.Rows,
		Schema:        convertToHudiFields(result.Schema.Fields),
		BeginTime:     options.BeginInstantTime,
		EndTime:       options.EndInstantTime,
		NextPageToken: result.NextPageToken,
	}, nil
}

func validateHudiIncrementalPage(options HudiIncrementalReadOptions, requestToken string, result *HudiQueryResult) error {
	if len(result.Rows) > options.PageSize {
		return fmt.Errorf("Hudi query engine returned %d rows for page size %d", len(result.Rows), options.PageSize)
	}
	if requestToken != "" && result.NextPageToken == requestToken {
		return fmt.Errorf("Hudi query engine repeated the current page token")
	}
	return nil
}

// BuildHudiIncrementalReadOptions validates and normalizes the Hudi reader
// contract before it is handed to Spark, Flink, or another Hudi-capable engine.
func BuildHudiIncrementalReadOptions(parser *HudiMetadataParser, req *IncrementalQueryRequest) (HudiIncrementalReadOptions, error) {
	if parser == nil {
		parser = NewHudiMetadataParser()
	}
	if req == nil {
		return HudiIncrementalReadOptions{}, fmt.Errorf("incremental request is required")
	}
	if err := validateHudiIncrementalRequest(parser, req); err != nil {
		return HudiIncrementalReadOptions{}, err
	}

	format := req.IncrementalFormat
	if format == "" {
		format = "latest_state"
	}
	if format != "latest_state" && format != "cdc" {
		return HudiIncrementalReadOptions{}, fmt.Errorf("unsupported Hudi incremental format %q; expected latest_state or cdc", format)
	}
	if format == "cdc" && req.TableType != "" && ParseHudiTableType(strings.ToUpper(req.TableType)) != string(CopyOnWriteTableType) {
		return HudiIncrementalReadOptions{}, fmt.Errorf("Hudi CDC reads require a Copy-on-Write table")
	}
	pageSize := req.PageSize
	if pageSize == 0 {
		pageSize = 1000
	}
	if pageSize < 1 || pageSize > 10000 {
		return HudiIncrementalReadOptions{}, fmt.Errorf("page size must be between 1 and 10000")
	}
	fields := append([]string(nil), req.SelectedFields...)
	for _, field := range fields {
		if !hudiFieldIdentifier.MatchString(field) {
			return HudiIncrementalReadOptions{}, fmt.Errorf("invalid selected field %q", field)
		}
	}

	return HudiIncrementalReadOptions{
		BasePath:          req.BasePath,
		BeginInstantTime:  req.BeginInstantTime,
		EndInstantTime:    req.EndInstantTime,
		IncrementalFormat: format,
		SelectedFields:    fields,
		PageSize:          pageSize,
		PageToken:         req.PageToken,
	}, nil
}

func validateHudiIncrementalRequest(parser *HudiMetadataParser, req *IncrementalQueryRequest) error {
	if req.BasePath == "" {
		return fmt.Errorf("basePath is required")
	}
	if req.BeginInstantTime == "" {
		return fmt.Errorf("beginInstantTime is required")
	}
	if req.EndInstantTime == "" {
		return fmt.Errorf("endInstantTime is required to keep incremental pages on one source snapshot")
	}
	if req.QueryType != "" && req.QueryType != "incremental" && req.QueryType != "query_between_instants" {
		return fmt.Errorf("unsupported Hudi query type %q; expected incremental", req.QueryType)
	}
	begin, err := parseHudiReadBoundary(parser, req.BeginInstantTime, true)
	if err != nil {
		return fmt.Errorf("invalid beginInstantTime format: %w", err)
	}
	end, err := parseHudiReadBoundary(parser, req.EndInstantTime, false)
	if err != nil {
		return fmt.Errorf("invalid endInstantTime format: %w", err)
	}
	if !begin.Before(end) {
		return fmt.Errorf("beginInstantTime must be earlier than endInstantTime")
	}
	return nil
}

func parseHudiReadBoundary(parser *HudiMetadataParser, value string, allowEarliest bool) (time.Time, error) {
	if value == "earliest" && allowEarliest {
		return time.Time{}, nil
	}
	if parsed, err := parser.ParseInstantTime(value); err == nil {
		return parsed, nil
	}
	if (len(value) == 10 || len(value) == 13) && allASCIIDigits(value) {
		numeric, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return time.Time{}, err
		}
		if len(value) == 10 {
			return time.Unix(numeric, 0).UTC(), nil
		}
		return time.UnixMilli(numeric).UTC(), nil
	}
	for _, layout := range []string{
		"2006-01-02",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.999",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05.999",
		time.RFC3339Nano,
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported instant time %q", value)
}

func allASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// GetChangesSince retrieves changes since a specific instant
func (iq *HudiIncrementalQuery) GetChangesSince(ctx context.Context, basePath, beginInstantTime string) (*IncrementalQueryResult, error) {
	// Get latest commit to use as end time
	latestCommit, err := iq.driver.GetLatestCommit(ctx, basePath)
	if err != nil {
		return nil, err
	}

	req := &IncrementalQueryRequest{
		BasePath:         basePath,
		BeginInstantTime: beginInstantTime,
		EndInstantTime:   latestCommit.CommitTime,
		QueryType:        "query_between_instants",
	}

	return iq.QueryIncremental(ctx, req)
}

// GetChangesInRange retrieves changes within a time range
func (iq *HudiIncrementalQuery) GetChangesInRange(ctx context.Context, basePath, beginInstantTime, endInstantTime string) (*IncrementalQueryResult, error) {
	req := &IncrementalQueryRequest{
		BasePath:         basePath,
		BeginInstantTime: beginInstantTime,
		EndInstantTime:   endInstantTime,
		QueryType:        "query_between_instants",
	}

	return iq.QueryIncremental(ctx, req)
}

// GetChangesSinceTimestamp retrieves changes since a timestamp
func (iq *HudiIncrementalQuery) GetChangesSinceTimestamp(ctx context.Context, basePath string, timestamp time.Time) (*IncrementalQueryResult, error) {
	instantTime := iq.driver.parser.FormatInstantTime(timestamp)
	return iq.GetChangesSince(ctx, basePath, instantTime)
}

// GetLatestChanges retrieves changes since the last N commits
func (iq *HudiIncrementalQuery) GetLatestChanges(ctx context.Context, basePath string, numCommits int) (*IncrementalQueryResult, error) {
	commits, err := iq.driver.GetCommits(ctx, basePath)
	if err != nil {
		return nil, err
	}

	if len(commits) < numCommits+1 {
		return nil, fmt.Errorf("not enough commits (need at least %d, have %d)", numCommits+1, len(commits))
	}

	beginInstantTime := commits[len(commits)-numCommits-1].CommitTime
	endInstantTime := commits[len(commits)-1].CommitTime

	return iq.GetChangesInRange(ctx, basePath, beginInstantTime, endInstantTime)
}

// GetPendingCompaction retrieves pending compaction instants
func (iq *HudiIncrementalQuery) GetPendingCompaction(ctx context.Context, basePath string) ([]CompactionInstant, error) {
	// Compaction plan would be stored in Hudi metadata
	// This is a placeholder implementation
	return []CompactionInstant{}, nil
}

// CompactionInstant represents a compaction instant
type CompactionInstant struct {
	InstantTime string
	State       string
}

// GetInserts retrieves only inserts through a Hudi-capable query engine.
func (iq *HudiIncrementalQuery) GetInserts(ctx context.Context, basePath, beginInstantTime, endInstantTime string) (*IncrementalQueryResult, error) {
	return iq.queryOperation(ctx, basePath, beginInstantTime, endInstantTime, "INSERT")
}

// GetUpdates retrieves only updates through a Hudi-capable query engine.
func (iq *HudiIncrementalQuery) GetUpdates(ctx context.Context, basePath, beginInstantTime, endInstantTime string) (*IncrementalQueryResult, error) {
	return iq.queryOperation(ctx, basePath, beginInstantTime, endInstantTime, "UPDATE")
}

// GetDeletes retrieves only deletes through a Hudi-capable query engine.
func (iq *HudiIncrementalQuery) GetDeletes(ctx context.Context, basePath, beginInstantTime, endInstantTime string) (*IncrementalQueryResult, error) {
	return iq.queryOperation(ctx, basePath, beginInstantTime, endInstantTime, "DELETE")
}

func (iq *HudiIncrementalQuery) queryOperation(ctx context.Context, basePath, beginInstantTime, endInstantTime, operation string) (*IncrementalQueryResult, error) {
	req := &IncrementalQueryRequest{
		BasePath:          basePath,
		BeginInstantTime:  beginInstantTime,
		EndInstantTime:    endInstantTime,
		QueryType:         "incremental",
		IncrementalFormat: "cdc",
	}
	options, err := BuildHudiIncrementalReadOptions(iq.driver.parser, req)
	if err != nil {
		return nil, err
	}
	options.OperationFilter = operation
	if iq.driver.incrementalExecutor == nil {
		return nil, fmt.Errorf("Hudi CDC reads require a configured query engine")
	}
	if err := iq.driver.validateIncrementalFormat(ctx, options); err != nil {
		return nil, err
	}
	result, err := iq.driver.incrementalExecutor.ReadIncremental(ctx, options)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("Hudi query engine returned an empty result")
	}
	if err := validateHudiIncrementalPage(options, req.PageToken, result); err != nil {
		return nil, err
	}
	return &IncrementalQueryResult{
		Data:          result.Rows,
		Schema:        convertToHudiFields(result.Schema.Fields),
		BeginTime:     options.BeginInstantTime,
		EndTime:       options.EndInstantTime,
		NextPageToken: result.NextPageToken,
	}, nil
}

// GetRecordCountByOperation gets count of records by operation type
func (iq *HudiIncrementalQuery) GetRecordCountByOperation(ctx context.Context, basePath, beginInstantTime, endInstantTime string) (map[string]int64, error) {
	commits, err := iq.driver.GetCommits(ctx, basePath)
	if err != nil {
		return nil, err
	}

	// Filter commits within range
	filteredCommits := filterCommitsByRange(commits, beginInstantTime, endInstantTime)

	// Aggregate by operation
	stats := make(map[string]int64)
	for _, commit := range filteredCommits {
		if commit.Operation != "" {
			stats[commit.Operation]++
		}
		if commit.RecordsStats.NumInserts > 0 {
			stats["inserts"] += int64(commit.RecordsStats.NumInserts)
		}
		if commit.RecordsStats.NumUpdates > 0 {
			stats["updates"] += int64(commit.RecordsStats.NumUpdates)
		}
		if commit.RecordsStats.NumDeletes > 0 {
			stats["deletes"] += int64(commit.RecordsStats.NumDeletes)
		}
	}

	return stats, nil
}

// filterCommitsByRange filters commits by instant time range
func filterCommitsByRange(commits []HudiCommit, beginTime, endTime string) []HudiCommit {
	result := make([]HudiCommit, 0)
	for _, commit := range commits {
		if commit.CommitTime > beginTime && commit.CommitTime <= endTime {
			result = append(result, commit)
		}
	}
	return result
}

// GetCDCChanges retrieves change data capture (CDC) changes
func (iq *HudiIncrementalQuery) GetCDCChanges(ctx context.Context, basePath, beginInstantTime, endInstantTime string) (*CDCResult, error) {
	return nil, fmt.Errorf("Hudi before/after CDC images are not exposed by the current incremental read contract")
}

// CDCResult represents CDC query results
type CDCResult struct {
	BeforeImages [][]interface{}
	AfterImages  [][]interface{}
}

// StreamChanges streams changes as they happen
func (iq *HudiIncrementalQuery) StreamChanges(ctx context.Context, basePath string, callback func(*IncrementalQueryResult) error) error {
	// Streaming would require polling or event-based mechanism
	// This is a placeholder implementation
	return fmt.Errorf("streaming not yet implemented")
}

// GetHudiMetadataFields returns Hudi's metadata fields
func (iq *HudiIncrementalQuery) GetHudiMetadataFields() []HudiMetadataField {
	return []HudiMetadataField{
		{
			Name:        "_hoodie_commit_time",
			Type:        "string",
			Description: "Commit instant time",
		},
		{
			Name:        "_hoodie_commit_seqno",
			Type:        "string",
			Description: "Commit sequence number",
		},
		{
			Name:        "_hoodie_record_key",
			Type:        "string",
			Description: "Record key",
		},
		{
			Name:        "_hoodie_partition_path",
			Type:        "string",
			Description: "Partition path",
		},
		{
			Name:        "_hoodie_file_name",
			Type:        "string",
			Description: "File name",
		},
		{
			Name:        "_hoodie_operation",
			Type:        "string",
			Description: "Operation type (INSERT/UPDATE/DELETE)",
		},
	}
}

// HudiMetadataField represents a Hudi metadata field
type HudiMetadataField struct {
	Name        string
	Type        string
	Description string
}

// ValidateIncrementalQueryRequest validates an incremental query request
func (iq *HudiIncrementalQuery) ValidateIncrementalQueryRequest(req *IncrementalQueryRequest) error {
	_, err := BuildHudiIncrementalReadOptions(iq.driver.parser, req)
	return err
}

// Helper functions

func convertToHudiFields(fields []HudiResultField) []HudiField {
	result := make([]HudiField, len(fields))
	for i, field := range fields {
		result[i] = HudiField{
			Name: field.Name,
			Type: field.Type,
		}
	}
	return result
}

func joinFields(fields []string) string {
	result := ""
	for i, field := range fields {
		if i > 0 {
			result += ", "
		}
		result += field
	}
	return result
}
