package execution

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestSliceStreamReturnsBoundedBatchesAndPreservesUnknownCounts(t *testing.T) {
	stream, err := NewSliceStream(
		[]Column{{Name: "id", Type: "BIGINT"}},
		[][]any{{int64(1)}, {int64(2)}},
		ResultMetadata{TotalRows: -1},
	)
	if err != nil {
		t.Fatalf("NewSliceStream() error = %v", err)
	}
	if got := stream.Metadata().TotalRows; got != -1 {
		t.Fatalf("TotalRows = %d, want unknown (-1)", got)
	}
	first, err := stream.NextBatch(context.Background(), 1)
	if err != nil {
		t.Fatalf("NextBatch(first) error = %v", err)
	}
	if !first.HasMore || first.Cursor != "1" || !reflect.DeepEqual(first.Rows, [][]any{{int64(1)}}) {
		t.Fatalf("first batch = %#v", first)
	}
	second, err := stream.NextBatch(context.Background(), 1)
	if err != nil {
		t.Fatalf("NextBatch(second) error = %v", err)
	}
	if second.HasMore || !reflect.DeepEqual(second.Rows, [][]any{{int64(2)}}) {
		t.Fatalf("second batch = %#v", second)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := stream.NextBatch(context.Background(), 1); err == nil {
		t.Fatal("NextBatch() succeeded after Close()")
	}
}

func TestSliceStreamRejectsMismatchedRows(t *testing.T) {
	_, err := NewSliceStream([]Column{{Name: "id"}}, [][]any{{int64(1), "extra"}}, ResultMetadata{})
	var executionErr *Error
	if !errors.As(err, &executionErr) || executionErr.Kind != ErrorDecode {
		t.Fatalf("NewSliceStream() error = %v, want decode error", err)
	}
}

func TestSQLDatabaseExecutorStreamsRowsAndCountsAtEOF(t *testing.T) {
	db := sql.OpenDB(contractConnector{})
	defer db.Close()

	stream, err := NewSQLDatabaseExecutor(db).ExecuteSQL(context.Background(), SQLRequest{
		Query: "SELECT id FROM rows WHERE id > ?", Parameters: []any{int64(0)},
	})
	if err != nil {
		t.Fatalf("ExecuteSQL() error = %v", err)
	}
	defer stream.Close()

	if columns := stream.Columns(); len(columns) != 1 || columns[0].Name != "id" || columns[0].Type != "BIGINT" {
		t.Fatalf("Columns() = %#v", columns)
	}
	if got := stream.Metadata().TotalRows; got != -1 {
		t.Fatalf("initial TotalRows = %d, want unknown (-1)", got)
	}
	first, err := stream.NextBatch(context.Background(), 1)
	if err != nil {
		t.Fatalf("NextBatch(first) error = %v", err)
	}
	if !first.HasMore || !reflect.DeepEqual(first.Rows, [][]any{{int64(1)}}) {
		t.Fatalf("first batch = %#v", first)
	}
	second, err := stream.NextBatch(context.Background(), 1)
	if err != nil {
		t.Fatalf("NextBatch(second) error = %v", err)
	}
	if second.HasMore || !reflect.DeepEqual(second.Rows, [][]any{{int64(2)}}) {
		t.Fatalf("second batch = %#v", second)
	}
	if got := stream.Metadata().TotalRows; got != 2 {
		t.Fatalf("final TotalRows = %d, want 2", got)
	}
}

func TestSQLDatabaseExecutorClassifiesContextCancellation(t *testing.T) {
	db := sql.OpenDB(contractConnector{})
	defer db.Close()
	stream, err := NewSQLDatabaseExecutor(db).ExecuteSQL(context.Background(), SQLRequest{Query: "SELECT id"})
	if err != nil {
		t.Fatalf("ExecuteSQL() error = %v", err)
	}
	defer stream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = stream.NextBatch(ctx, 1)
	var executionErr *Error
	if !errors.As(err, &executionErr) || executionErr.Kind != ErrorCancelled || !errors.Is(err, context.Canceled) {
		t.Fatalf("NextBatch(cancelled) error = %v, want classified context cancellation", err)
	}
}

type contractConnector struct{}

func (contractConnector) Connect(context.Context) (driver.Conn, error) { return contractConn{}, nil }
func (contractConnector) Driver() driver.Driver                        { return contractDriver{} }

type contractDriver struct{}

func (contractDriver) Open(string) (driver.Conn, error) { return contractConn{}, nil }

type contractConn struct{}

func (contractConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare is not supported")
}
func (contractConn) Close() error { return nil }
func (contractConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}
func (contractConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query == "SELECT id" && len(args) == 0 {
		return &contractRows{}, nil
	}
	if query != "SELECT id FROM rows WHERE id > ?" || len(args) != 1 || args[0].Value != int64(0) {
		return nil, errors.New("unexpected SQL query or parameters")
	}
	return &contractRows{}, nil
}

type contractRows struct{ next int }

func (*contractRows) Columns() []string                     { return []string{"id"} }
func (*contractRows) Close() error                          { return nil }
func (*contractRows) ColumnTypeDatabaseTypeName(int) string { return "BIGINT" }
func (*contractRows) ColumnTypeNullable(int) (bool, bool)   { return false, true }
func (r *contractRows) Next(dest []driver.Value) error {
	if r.next == 2 {
		return io.EOF
	}
	r.next++
	dest[0] = int64(r.next)
	return nil
}
