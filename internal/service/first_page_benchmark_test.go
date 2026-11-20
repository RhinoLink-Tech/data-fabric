//go:build datafabric_sqlite && cgo

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	_ "github.com/mattn/go-sqlite3"
)

// BenchmarkFirstPageSynthetic measures the actual database/sql pagination and
// response encoding path on a disposable in-memory SQLite fixture. It is not an
// MCP network, other database, production workload, or peak-RSS benchmark.
func BenchmarkFirstPageSynthetic(b *testing.B) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec("CREATE TABLE orders (id INTEGER PRIMARY KEY, status TEXT NOT NULL, quantity INTEGER NOT NULL, description TEXT NOT NULL)")
	if err != nil {
		b.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	stmt, err := tx.Prepare("INSERT INTO orders(id, status, quantity, description) VALUES (?, ?, ?, ?)")
	if err != nil {
		b.Fatal(err)
	}
	for i := 1; i <= 20000; i++ {
		if _, err := stmt.Exec(i, "fulfilled", i%10, strings.Repeat("synthetic-order-", 4)); err != nil {
			b.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	query := "SELECT id, status, quantity, description FROM orders WHERE quantity >= 0 ORDER BY id"
	plan, err := db.Query("EXPLAIN QUERY PLAN SELECT COUNT(*) FROM (" + query + ") AS data_fabric_count")
	if err != nil {
		b.Fatal(err)
	}
	for plan.Next() {
		var id, parent, unused int
		var detail string
		if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
			b.Fatal(err)
		}
		b.Logf("SQLite count plan: %s", detail)
	}
	if err := plan.Err(); err != nil {
		b.Fatal(err)
	}
	plan.Close()
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer pool.CloseAll()
	qs := &queryService{connPool: pool}
	for _, size := range []int{100, 10000} {
		b.Run(fmt.Sprintf("rows_%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var responseBytes int
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				columns, rows, total, err := qs.executeBatchQuery(context.Background(), model.DatabaseTypeMySQL, db, query, int64(size), 0, nil)
				if err != nil {
					b.Fatal(err)
				}
				if len(rows) != size || total != 20000 {
					b.Fatalf("rows=%d total=%d", len(rows), total)
				}
				response := model.FetchQueryResponse{QueryID: "synthetic-query", Slug: "synthetic-slug", Token: "synthetic-token", Columns: columns, Entries: rows, TotalCount: int(total), NextURI: "/api/v1/fetch/synthetic-query/synthetic-slug/synthetic-token"}
				payload, err := json.Marshal(response)
				if err != nil {
					b.Fatal(err)
				}
				responseBytes = len(payload)
			}
			b.ReportMetric(float64(responseBytes), "json-bytes/op")
		})
	}
}
