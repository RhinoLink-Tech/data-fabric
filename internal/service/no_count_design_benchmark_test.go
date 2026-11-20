//go:build datafabric_sqlite && cgo

package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/execution"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	sqlite3 "github.com/mattn/go-sqlite3"
)

// A counting driver wrapper records every source query actually issued by the
// measured path. Fixture setup is excluded by resetting it per operation.
type countBenchmarkConnector struct{ queries *atomic.Int64 }

func (c countBenchmarkConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := (&sqlite3.SQLiteDriver{}).Open(":memory:")
	if err != nil {
		return nil, err
	}
	return countBenchmarkConn{Conn: conn, queries: c.queries}, nil
}
func (countBenchmarkConnector) Driver() driver.Driver { return &sqlite3.SQLiteDriver{} }

type countBenchmarkConn struct {
	driver.Conn
	queries *atomic.Int64
}

func (c countBenchmarkConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.queries.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

// BenchmarkCountChoiceSynthetic compares the existing exact-count pagination
// with a test-only bounded-lookahead prototype. It is not the public API.
func BenchmarkCountChoiceSynthetic(b *testing.B) {
	ctx := context.Background()
	counter := &atomic.Int64{}
	db := sql.OpenDB(countBenchmarkConnector{queries: counter})
	var err error
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("CREATE TABLE orders (id INTEGER PRIMARY KEY, quantity INTEGER NOT NULL, description TEXT NOT NULL)"); err != nil {
		b.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	stmt, err := tx.Prepare("INSERT INTO orders(id, quantity, description) VALUES (?, ?, ?)")
	if err != nil {
		b.Fatal(err)
	}
	for i := 1; i <= 2000; i++ {
		if _, err := stmt.Exec(i, i%10, strings.Repeat("synthetic-order-", 4)); err != nil {
			b.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer pool.CloseAll()
	qs := &queryService{connPool: pool}
	query := "SELECT id, quantity, description FROM orders WHERE quantity >= 0 ORDER BY id"
	for _, full := range []bool{false, true} {
		for _, exact := range []bool{true, false} {
			name := fmt.Sprintf("full_%t/exact_%t", full, exact)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				var statements, bytes int
				b.ResetTimer()
				for iteration := 0; iteration < b.N; iteration++ {
					offset, total, responseBytes := 0, 0, 0
					counter.Store(0)
					for {
						var columns []model.ColumnInfo
						var entries []*[]interface{}
						more := false
						if exact {
							var n int64
							columns, entries, n, err = qs.executeBatchQuery(ctx, model.DatabaseTypeMySQL, db, query, 100, int64(offset), nil)
							if err != nil {
								b.Fatal(err)
							}
							if offset == 0 {
								total = int(n)
							}
							more = offset+len(entries) < total
						} else {
							sqlText, e := pool.ApplyBatchPagination(model.DatabaseTypeMySQL, query, 101, int64(offset))
							if e != nil {
								b.Fatal(e)
							}
							stream, e := execution.NewSQLDatabaseExecutor(db).ExecuteSQL(ctx, execution.SQLRequest{Query: sqlText})
							if e != nil {
								b.Fatal(e)
							}
							columns = qs.buildExecutionColumnInfo(stream.Columns(), model.DatabaseTypeMySQL)
							batch, e := stream.NextBatch(ctx, 101)
							if e != nil {
								stream.Close()
								b.Fatal(e)
							}
							if e = stream.Close(); e != nil {
								b.Fatal(e)
							}
							more = len(batch.Rows) > 100
							if more {
								batch.Rows = batch.Rows[:100]
							}
							entries = encodeExecutionRows(batch.Rows, columns)
						}
						for i, row := range entries {
							if got, ok := (*row)[0].(int64); !ok || got != int64(offset+i+1) {
								b.Fatalf("row %d: %v", offset+i+1, *row)
							}
						}
						response := model.FetchQueryResponse{Columns: columns, Entries: entries, TotalCount: total}
						var data []byte
						var e error
						if exact {
							data, e = json.Marshal(response)
						} else {
							data, e = json.Marshal(struct {
								*model.FetchQueryResponse
								TotalCount *int `json:"totalCount"`
							}{FetchQueryResponse: &response})
						}
						if e != nil {
							b.Fatal(e)
						}
						responseBytes += len(data)
						offset += len(entries)
						if !full || !more {
							break
						}
					}
					if full && offset != 2000 {
						b.Fatalf("rows=%d", offset)
					}
					if !full && offset != 100 {
						b.Fatalf("rows=%d", offset)
					}
					statements, bytes = int(counter.Load()), responseBytes
					wantStatements := 1
					if full {
						wantStatements = 20
					}
					if exact {
						wantStatements++
					}
					if statements != wantStatements {
						b.Fatalf("source statements=%d, want %d", statements, wantStatements)
					}
				}
				b.ReportMetric(float64(statements), "sql-stmts/op")
				b.ReportMetric(float64(bytes), "json-bytes/op")
			})
		}
	}
}
