package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/utils"
	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"

	"github.com/google/uuid"
)

type QueryService interface {
	ValidateQuery(ctx context.Context, caller *security.Caller, req *model.QueryRequest) error
	GetQueryStats(ctx context.Context) (*model.QueryStats, error)
	FetchQuery(ctx context.Context, caller *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error)
	FetchNextBatch(ctx context.Context, caller *security.Caller, queryID, slug, token string, batchSize int) (*model.FetchQueryResponse, error)
}

// QuerySession represents a query session for batch fetching
type QuerySession struct {
	QueryID string
	Slug    string
	Token   string
	// CreatorID binds a session to the identity that created it (#9): a
	// continuation Bearer token from a different user is rejected the same
	// way as an unknown session.
	CreatorID        string
	DataSourceID     string
	SQL              string
	Type             int
	BatchSize        int
	Cursor           interface{} // Cursor for pagination
	TotalRows        int64
	FetchedRows      int64
	Columns          []model.ColumnInfo
	Entries          []*[]interface{}
	CreatedAt        time.Time
	ExpiresAt        time.Time
	continuationLock sessionContinuationLock
}

// SessionManager manages query sessions
type SessionManager struct {
	sessions map[string]*QuerySession
	mutex    sync.RWMutex
	ttl      time.Duration
}

type queryService struct {
	datasourceRepo  repository.DataSourceRepository
	connPool        *database.ConnectionPool
	sqlValidator    *security.SQLValidator
	typeMapper      *utils.DataTypeMapper
	stats           *queryStats
	sessionManager  *SessionManager
	preferStreaming bool
	sqlTranslator   *sql_translator.SQLTranslationManager // Added SQL translator
}

type queryStats struct {
	totalQueries       int64
	successfulQueries  int64
	failedQueries      int64
	totalExecutionTime int64 // in nanoseconds
	lastQueryTime      time.Time
	queriesByType      map[string]int64
	mutex              sync.RWMutex
}

// NewQueryService creates a new instance of QueryService
func NewQueryService(datasourceRepo repository.DataSourceRepository, connPool *database.ConnectionPool, preferStreaming bool, translators ...*sql_translator.SQLTranslationManager) QueryService {
	translator := sql_translator.NewSQLTranslationManager(true)
	if len(translators) > 0 && translators[0] != nil {
		translator = translators[0]
	}
	validator := security.NewSQLValidator(10000)
	if translator.SupportsASTAnalysis() {
		validator.SetASTAnalyzer(translator)
	}
	return &queryService{
		datasourceRepo:  datasourceRepo,
		connPool:        connPool,
		sqlValidator:    validator,
		typeMapper:      utils.NewDataTypeMapper(),
		stats:           newQueryStats(),
		sessionManager:  NewSessionManager(),
		preferStreaming: preferStreaming,
		sqlTranslator:   translator,
	}
}

// NewSessionManager creates a new session manager
func NewSessionManager() *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*QuerySession),
		ttl:      30 * time.Minute, // Session TTL: 30 minutes
	}

	// Start cleanup goroutine
	go sm.cleanupSessions()

	return sm
}

func newQueryStats() *queryStats {
	return &queryStats{
		queriesByType: make(map[string]int64),
	}
}

func (qs *queryService) ValidateQuery(ctx context.Context, caller *security.Caller, req *model.QueryRequest) error {
	// Basic validation
	if !req.IsValid() {
		return fmt.Errorf("invalid query request")
	}

	// Apply defaults
	req.ApplyDefaults()

	// Get data source to check database type
	dataSource, err := qs.datasourceRepo.GetByID(ctx, req.DataSourceID)
	if err != nil {
		return err
	}
	if !caller.CanRead(dataSource) {
		return security.ErrDataSourceAccessDenied
	}

	// Translate SQL if needed
	// If the user is sending SQL in a different dialect, translate it to the target database dialect
	translatedSQL, err := qs.translateSQLIfNeeded(ctx, req.SQL, req.SourceDialect, string(dataSource.Type))
	if err != nil {
		return fmt.Errorf("failed to translate SQL: %w", err)
	}

	// Validate the translated form without mutating the request. Execution paths
	// translate the original request exactly once when they run the query.
	sqlToValidate := req.SQL
	if translatedSQL != "" {
		sqlToValidate = translatedSQL
	}

	// Validate SQL with data source-specific syntax checking
	if err := qs.sqlValidator.ValidateDataSourceSyntaxContext(ctx, sqlToValidate, dataSource.Type); err != nil {
		return err
	}

	return nil
}

// translateSQLIfNeeded translates SQL if a source dialect is specified and differs from target
func (qs *queryService) translateSQLIfNeeded(ctx context.Context, sql, sourceDialect, targetDialect string) (string, error) {
	// If no source dialect is specified, no translation is needed
	if sourceDialect == "" {
		return "", nil
	}

	// If source and target are the same, no translation is needed
	if strings.EqualFold(sourceDialect, targetDialect) {
		return "", nil
	}

	// Translate the SQL
	translatedSQL, err := qs.sqlTranslator.TranslateQueryContext(ctx, sql, sourceDialect, targetDialect)
	if err != nil {
		return "", fmt.Errorf("failed to translate SQL from %s to %s: %w", sourceDialect, targetDialect, err)
	}

	return translatedSQL, nil
}

func (qs *queryService) GetQueryStats(ctx context.Context) (*model.QueryStats, error) {
	qs.stats.mutex.RLock()
	defer qs.stats.mutex.RUnlock()

	avgExecutionTime := 0.0
	if qs.stats.totalQueries > 0 {
		avgExecutionTime = float64(qs.stats.totalExecutionTime) / float64(qs.stats.totalQueries) / 1e9 // Convert to seconds
	}

	return &model.QueryStats{
		TotalQueries:      qs.stats.totalQueries,
		SuccessfulQueries: qs.stats.successfulQueries,
		FailedQueries:     qs.stats.failedQueries,
		AvgExecutionTime:  avgExecutionTime,
		LastQueryTime:     qs.stats.lastQueryTime,
		QueriesByType:     qs.stats.queriesByType,
	}, nil
}

// executeQueryWithConnection executes a query using the provided database connection
func (qs *queryService) executeQueryWithConnection(ctx context.Context, db *sql.DB, req *model.QueryRequest, dbType model.DatabaseType) (*model.QueryResponse, error) {
	// Translate SQL if needed
	sqlToExecute := req.SQL
	if req.SourceDialect != "" {
		translatedSQL, err := qs.translateSQLIfNeeded(ctx, req.SQL, req.SourceDialect, string(dbType))
		if err != nil {
			return nil, fmt.Errorf("failed to translate SQL: %w", err)
		}
		if translatedSQL != "" {
			sqlToExecute = translatedSQL
		}
	}

	// Prepare the query with LIMIT and OFFSET if not already present
	finalSQL := qs.applyPagination(sqlToExecute, req.Limit, req.Offset, dbType)

	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Execute query
	rows, err := db.QueryContext(queryCtx, finalSQL, req.Parameters...)
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %w", err)
	}
	defer rows.Close()

	// Get column information
	columns, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("failed to get column types: %w", err)
	}

	// Build column information
	columnInfo := qs.buildColumnInfo(columns, dbType)

	// Process rows
	var results [][]interface{}
	rowCount := 0
	for rows.Next() {
		rowCount++
		row, err := qs.scanRow(rows, len(columns))
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Standardize row values
		standardizedRow, err := qs.standardizeRow(row, columnInfo)
		if err != nil {
			return nil, fmt.Errorf("failed to standardize row: %w", err)
		}

		results = append(results, standardizedRow)
	}

	// Check for errors from rows
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	// Check if there might be more results
	hasMore := rowCount == req.Limit

	// Build response
	return &model.QueryResponse{
		Success: true,
		Columns: columnInfo,
		Rows:    results,
		Metadata: model.QueryMetadata{
			RowCount:        rowCount,
			ExecutionTimeMs: time.Since(time.Now()).Milliseconds(),
			DataSourceID:    req.DataSourceID,
			DatabaseType:    string(dbType),
			HasMore:         hasMore,
			Limit:           req.Limit,
			Offset:          req.Offset,
			ExecutedAt:      time.Now(),
		},
	}, nil
}

// applyPagination adds LIMIT and OFFSET to SQL if not already present
func (qs *queryService) applyPagination(sql string, limit, offset int, dbType model.DatabaseType) string {
	sqlUpper := strings.ToUpper(strings.TrimSpace(sql))

	// Check if LIMIT is already present
	if strings.Contains(sqlUpper, " LIMIT ") {
		return sql
	}

	// Handle different database types
	switch dbType {
	case model.DatabaseTypePostgreSQL:
		if offset > 0 {
			return fmt.Sprintf("%s LIMIT %d OFFSET %d", sql, limit, offset)
		}
		return fmt.Sprintf("%s LIMIT %d", sql, limit)

	case model.DatabaseTypeOracle:
		// Oracle uses different pagination syntax
		if offset > 0 {
			return fmt.Sprintf("SELECT * FROM (SELECT a.*, ROWNUM rnum FROM (%s) a WHERE ROWNUM <= %d) WHERE rnum > %d", sql, limit+offset, offset)
		}
		return fmt.Sprintf("SELECT * FROM (%s) WHERE ROWNUM <= %d", sql, limit)

	default: // MySQL, MariaDB
		if offset > 0 {
			return fmt.Sprintf("%s LIMIT %d OFFSET %d", sql, limit, offset)
		}
		return fmt.Sprintf("%s LIMIT %d", sql, limit)
	}
}

// buildColumnInfo builds column information from database column types
func (qs *queryService) buildColumnInfo(columns []*sql.ColumnType, dbType model.DatabaseType) []model.ColumnInfo {
	columnInfo := make([]model.ColumnInfo, len(columns))

	for i, col := range columns {
		nullable, _ := col.Nullable()

		columnInfo[i] = model.ColumnInfo{
			Name:     col.Name(),
			Type:     qs.typeMapper.MapToStandardType(dbType, col.DatabaseTypeName(), nullable),
			Nullable: nullable,
		}
	}

	return columnInfo
}

// scanRow scans a single row from the database
func (qs *queryService) scanRow(rows *sql.Rows, columnCount int) ([]interface{}, error) {
	row := make([]interface{}, columnCount)
	rowPointers := make([]interface{}, columnCount)

	for i := range row {
		rowPointers[i] = &row[i]
	}

	if err := rows.Scan(rowPointers...); err != nil {
		return nil, err
	}

	return row, nil
}

// standardizeRow standardizes row values based on column types
func (qs *queryService) standardizeRow(row []interface{}, columns []model.ColumnInfo) ([]interface{}, error) {
	standardizedRow := make([]interface{}, len(row))

	for i, value := range row {
		if i >= len(columns) {
			standardizedRow[i] = value
			continue
		}

		targetType := model.StandardizedType(columns[i].Type)
		standardized, err := qs.typeMapper.StandardizeValue(value, targetType)
		if err != nil {
			return nil, fmt.Errorf("failed to standardize value for column %s: %w", columns[i].Name, err)
		}

		standardizedRow[i] = standardized
	}

	return standardizedRow, nil
}

// createErrorResponse maps legacy query errors to safe public messages. The
// submitted SQL and raw driver error are intentionally excluded from responses.
func (qs *queryService) createErrorResponse(err error, _ string) *model.QueryResponse {
	code := "QUERY_EXECUTION_ERROR"
	switch {
	case errors.Is(err, security.ErrNotSelectQuery), errors.Is(err, security.ErrSQLInjection),
		errors.Is(err, security.ErrDangerousKeyword), errors.Is(err, security.ErrSQLSyntaxError):
		code = "SECURITY_VIOLATION"
	case errors.Is(err, repository.ErrDataSourceNotFound):
		code = "DATASOURCE_NOT_FOUND"
	}
	return &model.QueryResponse{
		Success: false,
		Error: &model.QueryError{
			Code:    code,
			Message: SafeQueryErrorMessage(err),
		},
	}
}

// recordQueryStats records query execution statistics
func (qs *queryService) recordQueryStats(dataSourceID string, success bool, duration time.Duration) {
	qs.stats.mutex.Lock()
	defer qs.stats.mutex.Unlock()

	qs.stats.totalQueries++
	qs.stats.lastQueryTime = time.Now()
	qs.stats.totalExecutionTime += duration.Nanoseconds()

	if success {
		qs.stats.successfulQueries++
	} else {
		qs.stats.failedQueries++
	}

	// Count queries by data source type (would need to look up the data source)
	qs.stats.queriesByType[dataSourceID]++
}

// FetchQuery executes a fetch query and returns the first batch of data
func (qs *queryService) FetchQuery(ctx context.Context, caller *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	ctx, cancel := withQueryTimeout(ctx, req.Timeout)
	defer cancel()

	// Get data source
	datasource, err := qs.datasourceRepo.GetByID(ctx, req.DataSourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get data source: %w", err)
	}
	if !caller.CanRead(datasource) {
		return nil, security.ErrDataSourceAccessDenied
	}

	// Inactive or errored sources must not execute queries (#118)
	if err := requireActiveDataSource(datasource); err != nil {
		return nil, err
	}

	// Translate SQL if needed
	sqlToExecute := req.SQL
	if req.SourceDialect != "" {
		translatedSQL, err := qs.translateSQLIfNeeded(ctx, req.SQL, req.SourceDialect, string(datasource.Type))
		if err != nil {
			return nil, fmt.Errorf("failed to translate SQL: %w", err)
		}
		if translatedSQL != "" {
			sqlToExecute = translatedSQL
		}
	}

	// Validate after dialect conversion so valid source-dialect SQL is not rejected
	// by the validator before it is converted to the target dialect.
	if err := qs.sqlValidator.ValidateStatementForDataSourceContext(ctx, sqlToExecute, datasource.Type); err != nil {
		return nil, fmt.Errorf("SQL validation failed: %w", err)
	}

	// Get connection from pool
	conn, err := qs.connPool.GetConnection(ctx, datasource)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}

	// Create query session
	session := &QuerySession{
		QueryID:      qs.generateSessionID(),
		Slug:         qs.generateSlug(),
		Token:        qs.generateToken(),
		CreatorID:    caller.Key(),
		DataSourceID: req.DataSourceID,
		SQL:          sqlToExecute,
		Type:         req.Type,
		BatchSize:    req.BatchSize,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(30 * time.Minute),
	}

	// Execute query and get first batch
	columns, entries, totalCount, err := qs.executeBatchQuery(ctx, datasource.Type, conn, sqlToExecute, int64(req.BatchSize), 0)
	if err != nil {
		return nil, fmt.Errorf("failed to execute batch query: %w", err)
	}

	// Prepare response
	response := &model.FetchQueryResponse{
		QueryID:    session.QueryID,
		Slug:       session.Slug,
		Token:      session.Token,
		Columns:    columns,
		Entries:    entries,
		TotalCount: int(totalCount),
	}

	if req.Type == 2 && len(entries) < int(totalCount) {
		response.NextURI = qs.buildNextURI(session.QueryID, session.Slug, session.Token, req.BatchSize)

		// Update session data
		session.Columns = columns
		session.Entries = entries
		session.TotalRows = totalCount
		session.FetchedRows = int64(len(entries))

		// Store session
		qs.sessionManager.Store(session)
	}

	return response, nil
}

// FetchNextBatch fetches the next batch of data for a query session
func (qs *queryService) FetchNextBatch(ctx context.Context, caller *security.Caller, queryID, slug, token string, batchSize int) (*model.FetchQueryResponse, error) {
	ctx, cancel := withQueryTimeout(ctx, defaultQueryTimeoutSeconds)
	defer cancel()

	// Serialize continuation requests for this session. The first request
	// using a token advances the offset and rotates it; concurrent replays
	// wait, then fail validation with the stale token.
	session, err := qs.sessionManager.getAndLock(ctx, queryID)
	if err != nil {
		return nil, err
	}
	defer session.continuationLock.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateContinuationToken(session.Slug, session.Token, slug, token); err != nil {
		return nil, err
	}

	// Sessions are bound to their creator (#9): another user's valid Bearer
	// token cannot drain a stream it did not open. The response matches an
	// unknown session so session existence is not disclosed.
	if session.CreatorID != caller.Key() {
		return nil, ErrQuerySessionUnavailable
	}

	// Check if all data has been fetched
	if session.FetchedRows >= session.TotalRows {
		return &model.FetchQueryResponse{
			QueryID:    session.QueryID,
			Slug:       session.Slug,
			Token:      session.Token,
			Columns:    session.Columns,
			Entries:    []*[]interface{}{},
			TotalCount: int(session.TotalRows),
		}, nil
	}

	// Re-check the data source on continuation: the source may have been
	// disabled or deleted since the session was created (#118)
	datasource, err := qs.datasourceRepo.GetByID(ctx, session.DataSourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get data source: %w", err)
	}

	// Inactive or errored sources must not continue fetch sessions (#118)
	if !caller.CanRead(datasource) {
		return nil, security.ErrDataSourceAccessDenied
	}
	if err := requireActiveDataSource(datasource); err != nil {
		return nil, err
	}

	conn, err := qs.connPool.GetConnection(ctx, datasource)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}

	// Calculate offset for next batch
	offset := session.FetchedRows

	// Execute query for next batch
	columns, entries, _, err := qs.executeBatchQuery(ctx, datasource.Type, conn, session.SQL, int64(batchSize), offset)
	if err != nil {
		return nil, fmt.Errorf("failed to execute batch query: %w", err)
	}
	// Update session
	session.Entries = entries
	session.FetchedRows += int64(len(entries))
	session.Token = qs.generateToken()

	// Prepare response
	response := &model.FetchQueryResponse{
		QueryID:    session.QueryID,
		Slug:       session.Slug,
		Token:      session.Token,
		NextURI:    "",
		Columns:    columns,
		Entries:    entries,
		TotalCount: int(session.TotalRows),
	}

	// Check if this is the last batch
	if batchHasMoreRows(session.FetchedRows, session.TotalRows, len(entries)) {
		response.NextURI = qs.buildNextURI(session.QueryID, session.Slug, session.Token, batchSize)
	} else {
		qs.sessionManager.Delete(session.QueryID)
	}

	return response, nil
}

// SessionManager methods

func (sm *SessionManager) Store(session *QuerySession) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	sm.sessions[session.QueryID] = session
}

func (sm *SessionManager) getAndLock(ctx context.Context, queryID string) (*QuerySession, error) {
	session, err := sm.Get(queryID)
	if err != nil {
		return nil, ErrQuerySessionUnavailable
	}
	if err := session.continuationLock.Lock(ctx); err != nil {
		return nil, err
	}

	current, err := sm.Get(queryID)
	if err != nil || current != session {
		session.continuationLock.Unlock()
		return nil, ErrQuerySessionUnavailable
	}
	return session, nil
}

func (sm *SessionManager) Get(queryID string) (*QuerySession, error) {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	session, exists := sm.sessions[queryID]
	if !exists {
		return nil, fmt.Errorf("session not found")
	}

	// Check if session is expired
	if time.Now().After(session.ExpiresAt) {
		return nil, fmt.Errorf("session expired")
	}

	return session, nil
}

func (sm *SessionManager) Delete(queryID string) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	delete(sm.sessions, queryID)
}

func (sm *SessionManager) cleanupSessions() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		sm.mutex.Lock()
		now := time.Now()
		for id, session := range sm.sessions {
			if now.After(session.ExpiresAt) {
				delete(sm.sessions, id)
			}
		}
		sm.mutex.Unlock()
	}
}

func (qs *queryService) buildNextURI(queryID, slug, token string, batchSize int) string {
	return fmt.Sprintf("/api/v1/fetch/%s/%s/%s?batch_size=%d", queryID, slug, token, batchSize)
}

// executeBatchQuery executes a query and returns results in batch format
func (qs *queryService) executeBatchQuery(ctx context.Context, dbType model.DatabaseType, conn interface{}, query string, batchSize, offset int64) ([]model.ColumnInfo, []*[]interface{}, int64, error) {
	// Convert conn to *sql.DB
	db, ok := conn.(*sql.DB)
	if !ok {
		return nil, nil, 0, fmt.Errorf("invalid database connection type")
	}

	// Apply pagination to the SQL query
	paginatedSQL, err := qs.connPool.ApplyBatchPagination(dbType, query, batchSize, offset)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to apply query pagination: %w", err)
	}

	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Execute the query
	rows, err := db.QueryContext(queryCtx, paginatedSQL)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to execute query: %w", err)
	}
	defer rows.Close()

	// Get column information
	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to get column types: %w", err)
	}

	// Build column information
	columns := qs.buildBatchColumnInfo(columnTypes, dbType)

	// Process rows. Reuse the scan destination slice across rows; only the
	// returned row needs a fresh allocation because it remains in entries.
	scanArgs := make([]interface{}, len(columnTypes))
	var entries []*[]interface{}
	for rows.Next() {
		rowValues, err := qs.scanBatchRowWithArgs(rows, len(columnTypes), scanArgs)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("failed to scan row: %w", err)
		}
		// Encode into the fetch wire representation with the column types:
		// NULL stays null, binary bytes become base64, text bytes stay text
		// (#123).
		encodeFetchRow(rowValues, columns)
		entries = append(entries, &rowValues)
	}

	// Check for errors from rows iteration
	if err = rows.Err(); err != nil {
		return nil, nil, 0, fmt.Errorf("error iterating rows: %w", err)
	}

	// If the first page is not full, it contains the complete result set, so its
	// length is already the exact total. Avoid a second database round trip for
	// these common small-result queries. A full page is ambiguous and still
	// needs the exact count to preserve the existing response contract.
	var totalCount int64
	if offset == 0 {
		if batchSize > 0 && int64(len(entries)) < batchSize {
			totalCount = int64(len(entries))
		} else {
			totalCount, err = qs.getRowCount(ctx, db, query)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("failed to get row count: %w", err)
			}
		}
	}

	return columns, entries, totalCount, nil
}

// buildBatchColumnInfo builds column information from column types
func (qs *queryService) buildBatchColumnInfo(columnTypes []*sql.ColumnType, dbType model.DatabaseType) []model.ColumnInfo {
	columns := make([]model.ColumnInfo, len(columnTypes))

	for i, col := range columnTypes {
		nullable, _ := col.Nullable()
		columns[i] = model.ColumnInfo{
			Name:     col.Name(),
			Type:     qs.typeMapper.MapToStandardType(dbType, col.DatabaseTypeName(), nullable),
			Nullable: nullable,
		}
	}

	return columns
}

// scanBatchRowWithArgs scans a row using reusable scan arguments when
// provided. Values are returned as the driver delivered them; converting
// them into the fetch wire representation is the caller's job (see
// encodeFetchRow), because it needs the result set's column types.
func (qs *queryService) scanBatchRowWithArgs(rows *sql.Rows, columnCount int, scanArgs []interface{}) ([]interface{}, error) {
	result := make([]interface{}, columnCount)
	if len(scanArgs) != columnCount {
		scanArgs = make([]interface{}, columnCount)
	}
	for i := range result {
		scanArgs[i] = &result[i]
	}

	if err := rows.Scan(scanArgs...); err != nil {
		return nil, err
	}

	return result, nil
}

// getRowCount gets the total row count for the query
func (qs *queryService) getRowCount(ctx context.Context, db *sql.DB, sql string) (int64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	return countRowsInQuery(ctx, db, sql)
}

// buildCountSQL wraps the query in a derived table before counting it.
//
// The count has to be taken over the query's own result set. Rewriting the
// SELECT list in place instead ("SELECT COUNT(*) FROM users LIMIT 5") leaves the
// caller's LIMIT/OFFSET applying to the single aggregate row, so the count comes
// back as the whole table. Batch pagination then believes there are more rows
// than the query can ever return and hands out next-batch links for rows that do
// not exist.
func buildCountSQL(sql string) string {
	return fmt.Sprintf("SELECT COUNT(*) FROM (%s) AS t", countSubquery(sql))
}

// buildCountSQLNoAlias is buildCountSQL without the derived-table alias, which
// Oracle rejects.
func buildCountSQLNoAlias(sql string) string {
	return fmt.Sprintf("SELECT COUNT(*) FROM (%s)", countSubquery(sql))
}

// countSubquery prepares the caller's statement for use as a derived table.
//
// The statement is embedded as written, so it has to be legal inside parentheses.
// A trailing statement terminator is not, and the validator only strips it from
// the copy it parses rather than from the statement that gets executed, so it is
// dropped here.
func countSubquery(sql string) string {
	return strings.TrimRight(removeOrderByClause(sql), "; \t\r\n")
}

// buildCountSQLInPlace counts a query by replacing its SELECT list with COUNT(*)
// and keeping the rest of the statement.
//
// This form cannot count a query that limits its own rows, because the limit then
// applies to the single aggregate row instead of to the result set, so it is only
// reached for queries the derived-table forms cannot express at all.
func buildCountSQLInPlace(sql string) string {
	sql = countSubquery(sql)

	var parenLevel int
	for i := 0; i < len(sql); i++ {
		switch sql[i] {
		case '(':
			parenLevel++
		case ')':
			parenLevel--
		default:
			// 检查是否在最外层遇到FROM
			if parenLevel == 0 && i+4 <= len(sql) {
				if upper := sql[i : i+4]; strings.ToUpper(upper) == "FROM" {
					return "SELECT COUNT(*) " + sql[i:]
				}
			}
		}
	}

	return sql
}

// countRowsInQuery counts the rows the query returns.
//
// The derived table is tried first because it is the only form that counts a
// query carrying its own LIMIT/OFFSET. Not every query can be wrapped, though: a
// result set with two columns of the same name, which is what selecting every
// column of a join produces, is rejected by MySQL and PostgreSQL. Counting the
// FROM clause directly works for those, so it is the last resort. The middle
// attempt drops the derived-table alias, which Oracle rejects.
//
// All three failing means the statement cannot be counted at all, and the first
// error is reported: it is the one taken against the query as the caller wrote
// it, and it is the one that names the problem.
func countRowsInQuery(ctx context.Context, db *sql.DB, sql string) (int64, error) {
	var count int64
	firstErr := db.QueryRowContext(ctx, buildCountSQL(sql)).Scan(&count)
	if firstErr == nil {
		return count, nil
	}

	if err := db.QueryRowContext(ctx, buildCountSQLNoAlias(sql)).Scan(&count); err == nil {
		return count, nil
	}

	if err := db.QueryRowContext(ctx, buildCountSQLInPlace(sql)).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count rows: %w", firstErr)
	}

	return count, nil
}

// batchHasMoreRows reports whether a batch fetch should hand out another
// next-batch link.
//
// An empty batch means the query is exhausted, even when the row count taken
// earlier said otherwise: rows can disappear between batches. Continuing in that
// case would repeat the same offset forever.
func batchHasMoreRows(fetchedRows, totalRows int64, batchLen int) bool {
	if batchLen == 0 {
		return false
	}

	return fetchedRows < totalRows
}

// removeOrderByClause removes an ORDER BY clause that limits nothing from SQL
//
// 使用括号层级判断，只在最外层匹配ORDER BY。
//
// An ORDER BY that carries no row limit is dropped: it cannot change a count, and
// some databases reject one inside a subquery. An ORDER BY that does carry a
// limit is left alone, because the clause is followed by the rows it applies to
// and removing it would remove them as well -- the count would then report every
// row the table holds instead of the rows the query returns.
func removeOrderByClause(sql string) string {
	var parenLevel int
	for i := 0; i < len(sql); i++ {
		switch sql[i] {
		case '(':
			parenLevel++
		case ')':
			parenLevel--
		default:
			// 检查是否在最外层遇到ORDER BY
			if parenLevel == 0 && i+8 <= len(sql) {
				if upper := sql[i : i+8]; strings.ToUpper(upper) == "ORDER BY" {
					if hasRowLimit(sql[i:]) {
						return sql
					}
					return strings.TrimSpace(sql[:i])
				}
			}
		}
	}
	return sql
}

// hasRowLimit reports whether a statement tail limits how many rows it returns.
// The keywords are matched as whole words so that a column named "limit_count"
// or "fetched_at" does not read as one.
func hasRowLimit(sql string) bool {
	words := strings.FieldsFunc(strings.ToUpper(sql), func(r rune) bool {
		return !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_'
	})

	for _, word := range words {
		switch word {
		case "LIMIT", "OFFSET", "FETCH":
			return true
		}
	}

	return false
}

func (qs *queryService) generateSessionID() string {
	return uuid.New().String()
}

func (qs *queryService) generateToken() string {
	return uuid.New().String()
}

func (qs *queryService) generateSlug() string {
	return uuid.New().String()[:8]
}
