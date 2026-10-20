package service

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// countingDB is a database/sql driver that records every query it is asked to
// run and answers each one with a single integer.
type countingDB struct {
	queries []string
	count   int64
	// rejectDerivedTable makes the fake refuse a count taken over a derived
	// table, the way MySQL and PostgreSQL refuse one whose wrapped query has two
	// output columns with the same name.
	rejectDerivedTable bool
}

func (c *countingDB) connector() sqldriver.Connector { return countingConnector{c} }

type countingConnector struct{ db *countingDB }

func (c countingConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return countingConn{c.db}, nil
}

func (c countingConnector) Driver() sqldriver.Driver { return countingDriver{} }

type countingDriver struct{}

func (countingDriver) Open(string) (sqldriver.Conn, error) {
	return nil, errors.New("countingDriver must be used through its connector")
}

type countingConn struct{ db *countingDB }

func (countingConn) Prepare(string) (sqldriver.Stmt, error) {
	return nil, errors.New("prepared statements are not supported")
}

func (countingConn) Close() error { return nil }

func (countingConn) Begin() (sqldriver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c countingConn) QueryContext(_ context.Context, query string, _ []sqldriver.NamedValue) (sqldriver.Rows, error) {
	c.db.queries = append(c.db.queries, query)
	if c.db.rejectDerivedTable && strings.HasPrefix(query, "SELECT COUNT(*) FROM (") {
		return nil, errors.New("duplicate column name")
	}
	return &countingRows{value: c.db.count}, nil
}

type countingRows struct {
	value int64
	done  bool
}

func (*countingRows) Columns() []string { return []string{"count"} }
func (*countingRows) Close() error      { return nil }

func (r *countingRows) Next(dest []sqldriver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.value
	return nil
}

// The row count has to describe the result set the query actually returns. A
// query that carries its own LIMIT must have that LIMIT counted, otherwise the
// count reports the whole table and the batch session keeps handing out
// next-batch links for rows the query can never produce.
func TestGetRowCountCountsTheQueryResultSet(t *testing.T) {
	fake := &countingDB{count: 42}
	db := sql.OpenDB(fake.connector())
	defer db.Close()

	svc := &queryService{}

	got, err := svc.getRowCount(context.Background(), db, "SELECT * FROM users LIMIT 5")
	if err != nil {
		t.Fatalf("getRowCount returned error: %v", err)
	}
	if got != 42 {
		t.Errorf("getRowCount = %d, want 42", got)
	}

	if len(fake.queries) != 1 {
		t.Fatalf("issued %d count queries, want 1: %q", len(fake.queries), fake.queries)
	}
	if !strings.Contains(fake.queries[0], "(SELECT * FROM users LIMIT 5)") {
		t.Errorf("count query = %q, want the query's own LIMIT to be counted inside the subquery", fake.queries[0])
	}
}

// The caller's statement is embedded in the derived table as written, so whatever
// it ends with has to be legal in there. A statement terminator is not: it only
// makes the count fail, and the validator strips it from the copy it parses, not
// from the statement that is executed. The row-limiting clause is the opposite
// case -- dropping it would drop the LIMIT with it, which is the whole reason the
// derived table is used.
func TestBuildCountSQLKeepsTheQueryCountable(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want string
	}{
		{name: "trailing statement terminator", sql: "SELECT * FROM users;", want: "SELECT * FROM users"},
		{name: "terminator after whitespace", sql: "SELECT * FROM users ;  ", want: "SELECT * FROM users"},
		{name: "repeated terminators", sql: "SELECT * FROM users;;", want: "SELECT * FROM users"},
		{name: "no terminator", sql: "SELECT * FROM users", want: "SELECT * FROM users"},
		{name: "terminator inside a literal is not a terminator", sql: "SELECT ';' FROM users", want: "SELECT ';' FROM users"},
		{name: "ordering without a limit is dropped", sql: "SELECT * FROM users ORDER BY id", want: "SELECT * FROM users"},
		{name: "ordering with a limit is kept", sql: "SELECT * FROM users ORDER BY id LIMIT 5", want: "SELECT * FROM users ORDER BY id LIMIT 5"},
		{name: "ordering with an offset is kept", sql: "SELECT * FROM users ORDER BY id LIMIT 5 OFFSET 10", want: "SELECT * FROM users ORDER BY id LIMIT 5 OFFSET 10"},
		{name: "ordering by a column whose name starts with a keyword is dropped", sql: "SELECT * FROM users ORDER BY limit_count", want: "SELECT * FROM users"},
		{name: "ordering followed by a fetch is kept", sql: "SELECT * FROM users ORDER BY id FETCH FIRST 5 ROWS ONLY", want: "SELECT * FROM users ORDER BY id FETCH FIRST 5 ROWS ONLY"},
		{name: "cte ordering remains inside the cte while outer ordering is dropped", sql: "WITH recent AS (SELECT id FROM users ORDER BY created_at) SELECT id FROM recent ORDER BY id", want: "WITH recent AS (SELECT id FROM users ORDER BY created_at) SELECT id FROM recent"},
		{name: "limited subquery ordering remains when outer ordering is dropped", sql: "SELECT * FROM (SELECT id FROM users ORDER BY created_at LIMIT 10) AS recent ORDER BY id", want: "SELECT * FROM (SELECT id FROM users ORDER BY created_at LIMIT 10) AS recent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builders := []struct {
				name string
				got  string
				want string
			}{
				{name: "aliased", got: buildCountSQL(tt.sql), want: fmt.Sprintf("SELECT COUNT(*) FROM (%s) AS t", tt.want)},
				{name: "unaliased", got: buildCountSQLNoAlias(tt.sql), want: fmt.Sprintf("SELECT COUNT(*) FROM (%s)", tt.want)},
			}

			for _, builder := range builders {
				if builder.got != builder.want {
					t.Errorf("%s count query for %q = %q, want %q", builder.name, tt.sql, builder.got, builder.want)
				}
			}
		})
	}
}

// Wrapping the query in a derived table fails when its result set has two columns
// with the same name, which is what selecting every column of a join produces.
// Such a query is still countable by counting its FROM clause directly, so the
// count falls back to that rather than failing the request -- the batch query
// itself is fine, so the count must not be what breaks it.
func TestGetRowCountFallsBackForQueriesThatCannotBeWrapped(t *testing.T) {
	const query = "SELECT * FROM users u JOIN orders o ON u.id = o.user_id"

	fake := &countingDB{count: 7, rejectDerivedTable: true}
	db := sql.OpenDB(fake.connector())
	defer db.Close()

	svc := &queryService{}

	got, err := svc.getRowCount(context.Background(), db, query)
	if err != nil {
		t.Fatalf("getRowCount returned error: %v", err)
	}
	if got != 7 {
		t.Errorf("getRowCount = %d, want 7", got)
	}

	if len(fake.queries) == 0 {
		t.Fatal("no count query was issued")
	}

	const want = "SELECT COUNT(*) FROM users u JOIN orders o ON u.id = o.user_id"
	if last := fake.queries[len(fake.queries)-1]; last != want {
		t.Errorf("final count query = %q, want %q", last, want)
	}
}

// A batch that comes back empty means the query is exhausted, even when the row
// count taken earlier said otherwise (rows can disappear between batches).
// Handing out another next-batch link would repeat the same offset forever.
func TestBatchHasMoreRowsStopsOnEmptyBatch(t *testing.T) {
	tests := []struct {
		name        string
		fetchedRows int64
		totalRows   int64
		batchLen    int
		want        bool
	}{
		{name: "empty batch ends the session even with rows outstanding", fetchedRows: 5, totalRows: 100, batchLen: 0, want: false},
		{name: "partial batch continues", fetchedRows: 5, totalRows: 100, batchLen: 5, want: true},
		{name: "final batch ends the session", fetchedRows: 100, totalRows: 100, batchLen: 5, want: false},
		{name: "over-fetched session ends", fetchedRows: 120, totalRows: 100, batchLen: 5, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := batchHasMoreRows(tt.fetchedRows, tt.totalRows, tt.batchLen); got != tt.want {
				t.Errorf("batchHasMoreRows(%d, %d, %d) = %v, want %v",
					tt.fetchedRows, tt.totalRows, tt.batchLen, got, tt.want)
			}
		})
	}
}
