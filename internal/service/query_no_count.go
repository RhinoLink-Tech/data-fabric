package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/execution"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// noCountSQLSource identifies the database/sql source paths whose bounded
// pagination semantics have dedicated contract coverage. Other drivers must
// fail explicitly until their own lookahead semantics are established.
func noCountSQLSource(kind model.DatabaseType) bool {
	return kind == model.DatabaseTypeMySQL || kind == model.DatabaseTypeMariaDB || kind == model.DatabaseTypePostgreSQL || kind == model.DatabaseTypeTrino
}

// noCountSQLBatch returns at most size rows and at most one lookahead row.
// It does not perform a helper count; the next offset advances only by the
// visible rows, so the lookahead is fetched again on the next request.
func (qs *queryService) noCountSQLBatch(ctx context.Context, kind model.DatabaseType, connection interface{}, query string, size, offset int64, args []interface{}) ([]model.ColumnInfo, []*[]interface{}, []*[]interface{}, error) {
	if !noCountSQLSource(kind) {
		return nil, nil, nil, ErrNoCountUnsupported
	}
	db, ok := connection.(*sql.DB)
	if !ok || size < 1 || size > 10000 || offset < 0 {
		return nil, nil, nil, fmt.Errorf("invalid bounded no-count request")
	}
	var statement string
	var err error
	if kind == model.DatabaseTypeTrino {
		statement, err = noCountTrinoPagination(query, size+1, offset)
	} else {
		statement, err = qs.connPool.ApplyBatchPagination(kind, query, size+1, offset)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := execution.NewSQLDatabaseExecutor(db).ExecuteSQL(queryCtx, execution.SQLRequest{Query: statement, Parameters: args})
	if err != nil {
		return nil, nil, nil, err
	}
	defer stream.Close()
	columns := qs.buildExecutionColumnInfo(stream.Columns(), kind)
	batch, err := stream.NextBatch(queryCtx, int(size+1))
	if err != nil {
		return nil, nil, nil, err
	}
	if batch.HasMore || int64(len(batch.Rows)) > size+1 {
		return nil, nil, nil, fmt.Errorf("source did not respect the bounded lookahead")
	}
	rows := encodeExecutionRows(batch.Rows, columns)
	if int64(len(rows)) <= size {
		return columns, rows, nil, nil
	}
	return columns, rows[:size], rows[size:], nil
}

// noCountTrinoPagination preserves the original top-level ORDER BY while
// applying the page window within an optional literal LIMIT/OFFSET. Complex
// or parameterized top-level limit forms fail closed rather than being
// appended twice or wrapped in a subquery that can discard output ordering.
var trinoLiteralWindow = regexp.MustCompile(`(?i)^(?:OFFSET\s+([0-9]+)(?:\s+ROWS?)?(?:\s+|$))?(?:LIMIT\s+([0-9]+|ALL))?\s*$`)

func noCountTrinoPagination(query string, size, offset int64) (string, error) {
	if size < 1 || size > 10001 || offset < 0 {
		return "", ErrNoCountTrinoWindowUnsupported
	}
	sql := strings.TrimSpace(query)
	if strings.HasSuffix(sql, ";") {
		sql = strings.TrimSpace(strings.TrimSuffix(sql, ";"))
	}
	if sql == "" || topLevelKeywordIndex(sql, "FETCH") >= 0 {
		return "", ErrNoCountTrinoWindowUnsupported
	}
	limitAt, offsetAt := topLevelKeywordIndex(sql, "LIMIT"), topLevelKeywordIndex(sql, "OFFSET")
	if offsetAt >= 0 && limitAt >= 0 && offsetAt > limitAt {
		return "", ErrNoCountTrinoWindowUnsupported
	}
	windowAt := limitAt
	if offsetAt >= 0 && (windowAt < 0 || offsetAt < windowAt) {
		windowAt = offsetAt
	}
	if windowAt < 0 {
		return fmt.Sprintf("%s\nOFFSET %d LIMIT %d", sql, offset, size), nil
	}
	match := trinoLiteralWindow.FindStringSubmatch(sql[windowAt:])
	if match == nil || (match[1] == "" && match[2] == "") {
		return "", ErrNoCountTrinoWindowUnsupported
	}
	var originalOffset int64
	var err error
	if match[1] != "" {
		originalOffset, err = strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return "", ErrNoCountTrinoWindowUnsupported
		}
	}
	if offset > math.MaxInt64-originalOffset {
		return "", ErrNoCountTrinoWindowUnsupported
	}
	if match[2] != "" && !strings.EqualFold(match[2], "ALL") {
		originalLimit, err := strconv.ParseInt(match[2], 10, 64)
		if err != nil {
			return "", ErrNoCountTrinoWindowUnsupported
		}
		if offset >= originalLimit {
			size = 0
		} else if remaining := originalLimit - offset; remaining < size {
			size = remaining
		}
	}
	prefix := strings.TrimSpace(sql[:windowAt])
	if prefix == "" {
		return "", ErrNoCountTrinoWindowUnsupported
	}
	return fmt.Sprintf("%s\nOFFSET %d LIMIT %d", prefix, originalOffset+offset, size), nil
}

// accountNoCountPage enforces limits against the extra row while keeping the
// persisted result budget tied to rows actually returned. A pagination probe
// is deliberately fetched again at the next offset and is not double-counted.
func accountNoCountPage(budget *queryBudgetState, columns []model.ColumnInfo, rows, probe []*[]interface{}) error {
	if err := budget.accountPage(columns, rows); err != nil {
		return err
	}
	if len(probe) > 0 {
		trial := *budget
		if err := trial.accountPage(columns, probe); err != nil {
			return err
		}
	}
	return nil
}

// fetchNoCountSQLInitial is called only after source and SQL authorization.
func (qs *queryService) fetchNoCountSQLInitial(ctx context.Context, kind model.DatabaseType, conn interface{}, session *QuerySession, budget queryBudgetState) (*model.FetchQueryResponse, error) {
	columns, rows, probe, err := qs.noCountSQLBatch(ctx, kind, conn, session.SQL, int64(session.BatchSize), 0, session.Parameters)
	if err != nil {
		if budget.expired(time.Now()) {
			qs.stats.RecordBudgetExceeded()
			return nil, ErrQueryDurationLimitExceeded
		}
		return nil, fmt.Errorf("failed to execute bounded no-count query: %w", err)
	}
	if budget.expired(time.Now()) {
		qs.stats.RecordBudgetExceeded()
		return nil, ErrQueryDurationLimitExceeded
	}
	if err = accountNoCountPage(&budget, columns, rows, probe); err != nil {
		if errors.Is(err, ErrQueryResultLimitExceeded) {
			qs.stats.RecordBudgetExceeded()
		}
		return nil, err
	}
	if budget.expired(time.Now()) {
		qs.stats.RecordBudgetExceeded()
		return nil, ErrQueryDurationLimitExceeded
	}
	session.budget = budget
	more := len(probe) > 0
	response := &model.FetchQueryResponse{QueryID: session.QueryID, Slug: session.Slug, Token: session.Token, Columns: columns, Entries: rows, CountMode: model.QueryCountNone, HasMore: &more}
	if more {
		response.NextURI = qs.buildNextURI(session.QueryID, session.Slug, session.Token, session.BatchSize)
		session.Columns, session.Entries, session.FetchedRows = columns, rows, int64(len(rows))
		if !qs.sessionManager.Store(session) {
			return nil, ErrQuerySessionUnavailable
		}
	}
	return response, nil
}

// fetchNoCountSQLNext runs while the session continuation lock is held, after
// token validation, ownership checks and current source/policy reauthorization.
func (qs *queryService) fetchNoCountSQLNext(ctx context.Context, source *model.DataSource, session *QuerySession, size int) (*model.FetchQueryResponse, error) {
	conn, err := qs.connPool.GetConnection(ctx, source)
	if err != nil {
		if session.budget.expired(time.Now()) {
			qs.discardBudgetSession(session)
			return nil, ErrQueryDurationLimitExceeded
		}
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}
	columns, rows, probe, err := qs.noCountSQLBatch(ctx, source.Type, conn, session.SQL, int64(size), session.FetchedRows, session.Parameters)
	if err != nil {
		if session.budget.expired(time.Now()) {
			qs.discardBudgetSession(session)
			return nil, ErrQueryDurationLimitExceeded
		}
		return nil, fmt.Errorf("failed to execute bounded no-count query: %w", err)
	}
	if session.budget.expired(time.Now()) {
		qs.discardBudgetSession(session)
		return nil, ErrQueryDurationLimitExceeded
	}
	if err = accountNoCountPage(&session.budget, columns, rows, probe); err != nil {
		if errors.Is(err, ErrQueryResultLimitExceeded) {
			qs.discardBudgetSession(session)
		} else {
			qs.sessionManager.Delete(session.QueryID)
		}
		return nil, err
	}
	if session.budget.expired(time.Now()) {
		qs.discardBudgetSession(session)
		return nil, ErrQueryDurationLimitExceeded
	}
	session.BatchSize, session.Entries = size, rows
	session.FetchedRows += int64(len(rows))
	session.Token = qs.generateToken()
	more := len(probe) > 0
	response := &model.FetchQueryResponse{QueryID: session.QueryID, Slug: session.Slug, Token: session.Token, Columns: columns, Entries: rows, CountMode: model.QueryCountNone, HasMore: &more}
	if more {
		response.NextURI = qs.buildNextURI(session.QueryID, session.Slug, session.Token, size)
	} else {
		qs.sessionManager.Delete(session.QueryID)
	}
	return response, nil
}

// Encode a copy: the raw pending row remains available for the next chunk.
func encodedNoCountProbe(probe []interface{}, columns []model.ColumnInfo) []*[]interface{} {
	if probe == nil {
		return nil
	}
	row := append([]interface{}(nil), probe...)
	encodeFetchRow(row, columns)
	return []*[]interface{}{&row}
}
