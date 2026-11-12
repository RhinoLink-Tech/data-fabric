package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/execution"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/replicarouting"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"
)

// fetchNativeAPIQuery executes through the source protocol while preserving
// the gateway's existing paginated fetch response.
func (qs *queryService) fetchNativeAPIQuery(ctx context.Context, caller *security.Caller, datasource *model.DataSource, req *model.FetchQueryRequest, query string, tables []sqlanalysis.TableReference, parameters []interface{}, executor execution.NativeAPIExecutor, budget queryBudgetState) (*model.FetchQueryResponse, error) {
	batchSize := req.BatchSize
	if batchSize <= 0 {
		batchSize = 10000
	}
	if batchSize > 10000 {
		batchSize = 10000
	}

	capabilities := executor.Capabilities()
	if !capabilities.SupportsBatching {
		return nil, fmt.Errorf("native API executor does not support bounded batches")
	}
	if len(parameters) > 0 && !capabilities.SupportsParameters {
		return nil, fmt.Errorf("native API executor does not support query parameters")
	}

	cursor := &nativeAPIQueryCursor{
		executor:   executor,
		query:      query,
		parameters: nativeParameters(parameters),
		totalRows:  -1,
	}
	batch, sourceColumns, totalRows, err := cursor.NextBatch(ctx, batchSize)
	if err != nil {
		_ = cursor.Close()
		if budget.expired(time.Now()) {
			qs.stats.RecordBudgetExceeded()
			return nil, ErrQueryDurationLimitExceeded
		}
		return nil, fmt.Errorf("failed to execute native API query: %w", err)
	}
	if budget.expired(time.Now()) {
		_ = cursor.Close()
		qs.stats.RecordBudgetExceeded()
		return nil, ErrQueryDurationLimitExceeded
	}
	columns := qs.buildExecutionColumnInfo(sourceColumns, datasource.Type)
	entries := encodeExecutionRows(batch.Rows, columns)
	if err := budget.accountPage(columns, entries); err != nil {
		_ = cursor.Close()
		if err == ErrQueryResultLimitExceeded {
			qs.stats.RecordBudgetExceeded()
		}
		return nil, err
	}
	if budget.expired(time.Now()) {
		_ = cursor.Close()
		qs.stats.RecordBudgetExceeded()
		return nil, ErrQueryDurationLimitExceeded
	}

	createdAt := time.Now()
	queryID := qs.generateSessionID()
	expiresAt := replicarouting.QuerySessionExpiry(ctx, createdAt.Add(qs.sessionManager.ttl))
	if budget.deadline.Before(expiresAt) {
		expiresAt = budget.deadline
	}
	slug, err := querySessionSlug(ctx, queryID, expiresAt, qs.generateSlug)
	if err != nil {
		_ = cursor.Close()
		return nil, err
	}
	session := &QuerySession{
		QueryID:      queryID,
		Slug:         slug,
		Token:        qs.generateToken(),
		creator:      querySessionPrincipalFromContext(ctx, caller),
		DataSourceID: req.DataSourceID,
		SQL:          query,
		Tables:       tables,
		Parameters:   parameters,
		Type:         req.Type,
		BatchSize:    batchSize,
		TotalRows:    totalRows,
		FetchedRows:  int64(len(entries)),
		Columns:      columns,
		Entries:      entries,
		Cursor:       cursor,
		CreatedAt:    createdAt,
		ExpiresAt:    expiresAt,
		budget:       budget,
	}
	response := &model.FetchQueryResponse{
		QueryID:    session.QueryID,
		Slug:       session.Slug,
		Token:      session.Token,
		Columns:    columns,
		Entries:    entries,
		TotalCount: int(totalRows),
	}
	if batch.HasMore {
		if !qs.sessionManager.Store(session) {
			return nil, ErrQuerySessionUnavailable
		}
		response.NextURI = qs.buildNextURI(session.QueryID, session.Slug, session.Token, batchSize)
	} else {
		_ = cursor.Close()
	}
	return response, nil
}

func (qs *queryService) buildExecutionColumnInfo(sourceColumns []execution.Column, dbType model.DatabaseType) []model.ColumnInfo {
	columns := make([]model.ColumnInfo, len(sourceColumns))
	for i, column := range sourceColumns {
		columns[i] = model.ColumnInfo{
			Name:     column.Name,
			Type:     qs.typeMapper.MapToStandardType(dbType, column.Type, column.Nullable),
			Nullable: column.Nullable,
		}
	}
	return columns
}

func encodeExecutionRows(rows [][]any, columns []model.ColumnInfo) []*[]interface{} {
	entries := make([]*[]interface{}, len(rows))
	for i, sourceRow := range rows {
		row := sourceRow
		encodeFetchRow(row, columns)
		entries[i] = &row
	}
	return entries
}

type nativeAPIQueryCursor struct {
	mu         sync.Mutex
	executor   execution.NativeAPIExecutor
	query      string
	parameters []execution.Parameter
	offset     int64
	totalRows  int64
	closed     bool
}

func nativeParameters(parameters []interface{}) []execution.Parameter {
	result := make([]execution.Parameter, len(parameters))
	for i, value := range parameters {
		result[i] = execution.Parameter{Value: value}
	}
	return result
}

func (c *nativeAPIQueryCursor) NextBatch(ctx context.Context, batchSize int) (execution.Batch, []execution.Column, int64, error) {
	if ctx == nil {
		return execution.Batch{}, nil, 0, fmt.Errorf("context is required")
	}
	if batchSize < 1 {
		return execution.Batch{}, nil, 0, fmt.Errorf("batch size must be positive")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return execution.Batch{}, nil, 0, &execution.Error{Kind: execution.ErrorClosed, Operation: "fetch native API batch", Err: fmt.Errorf("query cursor is closed")}
	}

	stream, err := c.executor.ExecuteNativeAPI(ctx, execution.NativeAPIRequest{
		Operation:  "sql",
		Query:      c.query,
		Parameters: c.parameters,
		BatchSize:  batchSize,
		Limit:      batchSize,
		Offset:     c.offset,
	})
	if err != nil {
		return execution.Batch{}, nil, 0, err
	}
	if stream == nil {
		return execution.Batch{}, nil, 0, fmt.Errorf("native API executor returned a nil result stream")
	}
	columns := stream.Columns()
	batch, readErr := stream.NextBatch(ctx, batchSize)
	closeErr := stream.Close()
	if readErr != nil {
		return execution.Batch{}, nil, 0, readErr
	}
	if closeErr != nil {
		return execution.Batch{}, nil, 0, closeErr
	}
	if len(batch.Rows) > batchSize {
		return execution.Batch{}, nil, 0, fmt.Errorf("native API executor returned %d rows for batch size %d", len(batch.Rows), batchSize)
	}
	if batch.HasMore {
		return execution.Batch{}, nil, 0, fmt.Errorf("native API executor did not honor the bounded page request")
	}

	if c.totalRows < 0 {
		if len(batch.Rows) < batchSize {
			c.totalRows = c.offset + int64(len(batch.Rows))
		} else {
			count, err := c.countRows(ctx)
			if err != nil {
				return execution.Batch{}, nil, 0, fmt.Errorf("count native API query rows: %w", err)
			}
			c.totalRows = count
		}
	}
	if len(batch.Rows) == 0 && c.offset < c.totalRows {
		return execution.Batch{}, nil, 0, fmt.Errorf("native API returned an empty page at offset %d before total row count %d", c.offset, c.totalRows)
	}
	c.offset += int64(len(batch.Rows))
	batch.HasMore = c.offset < c.totalRows
	return batch, columns, c.totalRows, nil
}

func (c *nativeAPIQueryCursor) countRows(ctx context.Context) (int64, error) {
	stream, err := c.executor.ExecuteNativeAPI(ctx, execution.NativeAPIRequest{
		Operation:  "sql",
		Query:      buildCountSQL(c.query),
		Parameters: c.parameters,
		BatchSize:  1,
		Limit:      1,
	})
	if err != nil {
		return 0, err
	}
	if stream == nil {
		return 0, fmt.Errorf("native API executor returned a nil count stream")
	}
	defer stream.Close()
	batch, err := stream.NextBatch(ctx, 1)
	if err != nil {
		return 0, err
	}
	if len(batch.Rows) != 1 || len(batch.Rows[0]) != 1 {
		return 0, fmt.Errorf("count query returned %d rows with an invalid shape", len(batch.Rows))
	}
	return nativeCountValue(batch.Rows[0][0])
}

func nativeCountValue(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case int64:
		return typed, nil
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return 0, fmt.Errorf("count exceeds int64 range")
		}
		return int64(typed), nil
	case float64:
		if typed < 0 || typed != float64(int64(typed)) {
			return 0, fmt.Errorf("count is not an integer")
		}
		return int64(typed), nil
	case json.Number:
		return typed.Int64()
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unsupported count value type %T", value)
	}
}

func (c *nativeAPIQueryCursor) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}
