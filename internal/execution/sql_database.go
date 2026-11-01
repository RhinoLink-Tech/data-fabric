package execution

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// SQLDatabaseExecutor adapts an existing database/sql pool to the SQLExecutor
// contract and exposes rows as typed result batches.
type SQLDatabaseExecutor struct{ db *sql.DB }

// NewSQLDatabaseExecutor wraps a database/sql pool without taking ownership of
// the pool itself.
func NewSQLDatabaseExecutor(db *sql.DB) *SQLDatabaseExecutor { return &SQLDatabaseExecutor{db: db} }
func (e *SQLDatabaseExecutor) Capabilities() Capabilities {
	return Capabilities{Mode: ModeSQL, SupportsParameters: true, SupportsSchemaDiscovery: true, SupportsBatching: true, SupportsCancellation: true}
}
func (e *SQLDatabaseExecutor) ExecuteSQL(ctx context.Context, request SQLRequest) (ResultStream, error) {
	if ctx == nil {
		return nil, newError(ErrorInvalidRequest, "execute SQL", fmt.Errorf("context is required"))
	}
	if e == nil || e.db == nil {
		return nil, newError(ErrorInvalidRequest, "execute SQL", fmt.Errorf("database/sql pool is required"))
	}
	if request.Query == "" {
		return nil, newError(ErrorInvalidRequest, "execute SQL", fmt.Errorf("query is required"))
	}
	started := time.Now()
	queryCtx, cancel := context.WithCancel(ctx)
	rows, err := e.db.QueryContext(queryCtx, request.Query, request.Parameters...)
	if err != nil {
		cancel()
		return nil, WrapError("execute SQL", err)
	}
	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		_ = rows.Close()
		cancel()
		return nil, WrapError("read SQL column types", err)
	}
	columns := make([]Column, len(columnTypes))
	values := make([]any, len(columnTypes))
	scanArgs := make([]any, len(columnTypes))
	for i, columnType := range columnTypes {
		nullable, known := columnType.Nullable()
		columns[i] = Column{Name: columnType.Name(), Type: columnType.DatabaseTypeName(), Nullable: nullable, NullableKnown: known}
		scanArgs[i] = &values[i]
	}
	return &sqlRowsStream{
		rows: rows, columns: columns, metadata: ResultMetadata{TotalRows: -1},
		started: started, queryCancel: cancel, scanValues: values, scanArgs: scanArgs,
	}, nil
}

type sqlRowsStream struct {
	mu           sync.Mutex
	rows         *sql.Rows
	columns      []Column
	metadata     ResultMetadata
	started      time.Time
	queryCancel  context.CancelFunc
	scanValues   []any
	scanArgs     []any
	pending      []any
	rowsRead     int64
	done, closed bool
}

func (s *sqlRowsStream) Columns() []Column {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Column(nil), s.columns...)
}
func (s *sqlRowsStream) Metadata() ResultMetadata {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.metadata
	if !s.started.IsZero() {
		m.Duration = time.Since(s.started)
	}
	return m
}
func (s *sqlRowsStream) NextBatch(ctx context.Context, maxRows int) (Batch, error) {
	if ctx == nil {
		return Batch{}, newError(ErrorInvalidRequest, "read SQL batch", fmt.Errorf("context is required"))
	}
	if maxRows < 1 {
		return Batch{}, newError(ErrorInvalidRequest, "read SQL batch", fmt.Errorf("maxRows must be positive"))
	}
	if err := ctx.Err(); err != nil {
		return Batch{}, WrapError("read SQL batch", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Batch{}, newError(ErrorClosed, "read SQL batch", fmt.Errorf("result stream is closed"))
	}
	stopCancellation := context.AfterFunc(ctx, s.queryCancel)
	defer stopCancellation()
	resultRows := make([][]any, 0, maxRows)
	for len(resultRows) < maxRows {
		row, ok, err := s.nextRow(ctx)
		if err != nil {
			return Batch{}, err
		}
		if !ok {
			break
		}
		resultRows = append(resultRows, row)
	}
	s.rowsRead += int64(len(resultRows))
	hasMore := false
	if len(resultRows) == maxRows && !s.done {
		row, ok, err := s.nextRow(ctx)
		if err != nil {
			return Batch{}, err
		}
		if ok {
			s.pending = row
			hasMore = true
		}
	}
	if !hasMore && s.done {
		s.metadata.TotalRows = s.rowsRead
	}
	return Batch{Rows: resultRows, HasMore: hasMore}, nil
}
func (s *sqlRowsStream) nextRow(ctx context.Context) ([]any, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, WrapError("read SQL batch", err)
	}
	if s.pending != nil {
		row := s.pending
		s.pending = nil
		return row, true, nil
	}
	if s.done {
		return nil, false, nil
	}
	if !s.rows.Next() {
		s.done = true
		if err := s.rows.Err(); err != nil {
			return nil, false, WrapError("iterate SQL rows", err)
		}
		return nil, false, nil
	}
	if err := s.rows.Scan(s.scanArgs...); err != nil {
		return nil, false, WrapError("scan SQL row", err)
	}
	return cloneRow(s.scanValues), true, nil
}
func (s *sqlRowsStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.queryCancel != nil {
		s.queryCancel()
	}
	if s.rows == nil {
		return nil
	}
	return s.rows.Close()
}
