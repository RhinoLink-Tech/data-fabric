//go:build datafabric_sqlite && cgo

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/traditional"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

// sqliteMySQLFixture is only an isolated database/sql test fixture. It does not
// establish real MySQL or PostgreSQL behavior.
type sqliteMySQLFixture struct {
	*traditional.MySQLDriver
	db *sql.DB
}

func (driver *sqliteMySQLFixture) Open(string) (*sql.DB, error) { return driver.db, nil }

func TestNoCountPaginationAndStreamingSynthetic(t *testing.T) {
	for _, mode := range []string{"pagination", "streaming"} {
		for _, tc := range []struct {
			name         string
			count, limit int
			varySize     bool
		}{
			{"empty", 0, 0, false}, {"one", 1, 0, false}, {"exact", 2, 0, false},
			{"plus_one", 3, 0, false}, {"multiple", 4, 0, false}, {"tail", 5, 0, false},
			{"sql_limit", 5, 3, false}, {"limit_full_page", 5, 2, false}, {"vary_size", 5, 0, true},
		} {
			t.Run(fmt.Sprintf("%s/%s", mode, tc.name), func(t *testing.T) {
				count := tc.count
				counter := &atomic.Int64{}
				db := sql.OpenDB(countBenchmarkConnector{queries: counter})
				if _, err := db.Exec("CREATE TABLE orders (id INTEGER NOT NULL)"); err != nil {
					t.Fatal(err)
				}
				for i := 1; i <= count; i++ {
					if _, err := db.Exec("INSERT INTO orders VALUES (?)", i); err != nil {
						t.Fatal(err)
					}
				}
				registry := database.NewDriverRegistry()
				registry.RegisterDriver(model.DatabaseTypeMySQL, &sqliteMySQLFixture{MySQLDriver: &traditional.MySQLDriver{}, db: db})
				pool := database.NewConnectionPool(registry, nil, nil, false)
				defer pool.CloseAll()
				source := &model.DataSource{ID: "no-count", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{MaxPoolSize: 1}}
				repo := federationSourceRepository{source: source}
				ctx := context.Background()
				query := "SELECT id FROM orders WHERE id > ? ORDER BY id"
				wantCount := count
				if tc.limit > 0 {
					query += fmt.Sprintf(" LIMIT %d", tc.limit)
					wantCount = tc.limit
				}
				req := &model.FetchQueryRequest{DataSourceID: source.ID, SQL: query, Parameters: []interface{}{int64(0)}, BatchSize: 2, CountMode: model.QueryCountNone}
				var page *model.FetchQueryResponse
				var err error
				var next func(*model.FetchQueryResponse, int) (*model.FetchQueryResponse, error)
				counter.Store(0)
				if mode == "pagination" {
					q := NewQueryService(repo, pool, false, newTestSQLTranslationManager())
					defer q.Shutdown()
					page, err = q.FetchQuery(ctx, nil, req)
					next = func(p *model.FetchQueryResponse, size int) (*model.FetchQueryResponse, error) {
						return q.FetchNextBatch(ctx, nil, p.QueryID, p.Slug, p.Token, size)
					}
				} else {
					q := NewStreamingService(repo, pool, newTestSQLTranslationManager())
					defer q.Shutdown()
					page, err = q.FetchWithStreaming(ctx, nil, req)
					next = func(p *model.FetchQueryResponse, size int) (*model.FetchQueryResponse, error) {
						return q.FetchNextBatchWithStreaming(ctx, nil, p.QueryID, p.Slug, p.Token, size)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				seen, pages := 0, 0
				for {
					pages++
					raw, err := json.Marshal(page)
					if err != nil {
						t.Fatal(err)
					}
					var result map[string]interface{}
					if err := json.Unmarshal(raw, &result); err != nil {
						t.Fatal(err)
					}
					if result["countMode"] != "none" || result["totalCount"] != nil || result["hasMore"] != (page.NextURI != "") {
						t.Fatalf("invalid no-count response %s", raw)
					}
					for _, row := range page.Entries {
						seen++
						if got, ok := (*row)[0].(int64); !ok || got != int64(seen) {
							t.Fatalf("row=%v want=%d", *row, seen)
						}
					}
					if page.NextURI == "" {
						break
					}
					previous := page
					size := 0
					if tc.varySize && pages == 1 {
						size = 1
					}
					if tc.varySize && pages == 2 {
						size = 3
					}
					page, err = next(previous, size)
					if err != nil {
						t.Fatal(err)
					}
					_, err = next(previous, 0)
					if err == nil {
						t.Fatal("replayed token accepted")
					}
					if pages > 10 {
						t.Fatal("unbounded continuation")
					}
				}
				if seen != wantCount {
					t.Fatalf("seen=%d want=%d", seen, wantCount)
				}
				wantQueries := pages
				if mode == "streaming" {
					wantQueries = 1
				}
				if got := int(counter.Load()); got != wantQueries {
					t.Fatalf("source statements=%d want=%d", got, wantQueries)
				}
			})
		}
	}
}

func TestNoCountRejectsUnverifiedSourcesAndBudgetOverflow(t *testing.T) {
	if !noCountSQLSource(model.DatabaseTypeMariaDB) {
		t.Fatal("verified MariaDB source rejected")
	}
	budget := newQueryBudgetState(time.Now(), QueryBudgetLimits{MaxRows: 2, MaxBytes: 100000, MaxDuration: time.Minute})
	row := []interface{}{int64(1)}
	rows := []*[]interface{}{&row, &row}
	if err := accountNoCountPage(&budget, nil, rows, []*[]interface{}{&row}); !errors.Is(err, ErrQueryResultLimitExceeded) {
		t.Fatalf("probe budget error=%v", err)
	}
}

func TestNoCountSourceDeactivationClosesSessions(t *testing.T) {
	for _, mode := range []string{"pagination", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			counter := &atomic.Int64{}
			db := sql.OpenDB(countBenchmarkConnector{queries: counter})
			if _, err := db.Exec("CREATE TABLE orders (id INTEGER NOT NULL)"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO orders VALUES (1),(2),(3)"); err != nil {
				t.Fatal(err)
			}
			registry := database.NewDriverRegistry()
			registry.RegisterDriver(model.DatabaseTypeMySQL, &sqliteMySQLFixture{MySQLDriver: &traditional.MySQLDriver{}, db: db})
			pool := database.NewConnectionPool(registry, nil, nil, false)
			defer pool.CloseAll()
			source := &model.DataSource{ID: "no-count-revoke", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, OwnerID: "alice", TenantID: "tenant-a", Config: model.DataSourceConfig{MaxPoolSize: 1}}
			alice := &security.Caller{UserID: "alice", TenantID: "tenant-a"}
			bob := &security.Caller{UserID: "bob", TenantID: "tenant-b"}
			repo := federationSourceRepository{source: source}
			ctx := context.Background()
			req := &model.FetchQueryRequest{DataSourceID: source.ID, SQL: "SELECT id FROM orders ORDER BY id", BatchSize: 2, CountMode: model.QueryCountNone}
			var page *model.FetchQueryResponse
			var err error
			var next func(*model.FetchQueryResponse, *security.Caller) (*model.FetchQueryResponse, error)
			var live func() int
			if mode == "pagination" {
				q := NewQueryService(repo, pool, false, newTestSQLTranslationManager())
				defer q.Shutdown()
				page, err = q.FetchQuery(ctx, alice, req)
				next = func(p *model.FetchQueryResponse, caller *security.Caller) (*model.FetchQueryResponse, error) {
					return q.FetchNextBatch(ctx, caller, p.QueryID, p.Slug, p.Token, 0)
				}
				live = func() int { return q.LiveSessionCount(time.Now()) }
			} else {
				q := NewStreamingService(repo, pool, newTestSQLTranslationManager())
				defer q.Shutdown()
				page, err = q.FetchWithStreaming(ctx, alice, req)
				next = func(p *model.FetchQueryResponse, caller *security.Caller) (*model.FetchQueryResponse, error) {
					return q.FetchNextBatchWithStreaming(ctx, caller, p.QueryID, p.Slug, p.Token, 0)
				}
				live = func() int { return q.LiveSessionCount(time.Now()) }
			}
			if err != nil || page.NextURI == "" || live() != 1 {
				t.Fatalf("initial page=%+v err=%v live=%d", page, err, live())
			}
			if _, err := next(page, bob); !errors.Is(err, ErrQuerySessionUnavailable) {
				t.Fatalf("foreign caller continuation err=%v", err)
			}
			if live() != 1 {
				t.Fatalf("foreign caller destroyed authorized session")
			}
			source.Status = model.DataSourceStatusInactive
			if _, err := next(page, alice); !errors.Is(err, ErrDataSourceInactive) {
				t.Fatalf("deactivated continuation err=%v", err)
			}
			if live() != 0 {
				t.Fatalf("session retained after deactivation: %d", live())
			}
		})
	}
}

func TestNoCountUnsupportedSourceFailsBeforeExecution(t *testing.T) {
	source := &model.DataSource{ID: "unsupported-no-count", Type: model.DatabaseTypeMariaDB, Status: model.DataSourceStatusActive}
	repo := federationSourceRepository{source: source}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer pool.CloseAll()
	source.Type = model.DatabaseTypeMongoDB
	req := &model.FetchQueryRequest{DataSourceID: source.ID, SQL: "SELECT id FROM orders", BatchSize: 2, CountMode: model.QueryCountNone}
	paging := NewQueryService(repo, pool, false, newTestSQLTranslationManager())
	defer paging.Shutdown()
	if _, err := paging.FetchQuery(context.Background(), nil, req); !errors.Is(err, ErrNoCountUnsupported) {
		t.Fatalf("pagination err=%v", err)
	}
	streaming := NewStreamingService(repo, pool, newTestSQLTranslationManager())
	defer streaming.Shutdown()
	if _, err := streaming.FetchWithStreaming(context.Background(), nil, req); !errors.Is(err, ErrNoCountUnsupported) {
		t.Fatalf("streaming err=%v", err)
	}
}

func TestNoCountConcurrentReplayAndBudget(t *testing.T) {
	for _, mode := range []string{"pagination", "streaming"} {
		for _, scenario := range []string{"replay", "budget"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				db := sql.OpenDB(countBenchmarkConnector{queries: &atomic.Int64{}})
				if _, err := db.Exec("CREATE TABLE orders (id INTEGER NOT NULL)"); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("INSERT INTO orders VALUES (1),(2),(3),(4),(5)"); err != nil {
					t.Fatal(err)
				}
				registry := database.NewDriverRegistry()
				registry.RegisterDriver(model.DatabaseTypeMySQL, &sqliteMySQLFixture{MySQLDriver: &traditional.MySQLDriver{}, db: db})
				pool := database.NewConnectionPool(registry, nil, nil, false)
				defer pool.CloseAll()
				source := &model.DataSource{ID: "no-count-replay", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{MaxPoolSize: 1}}
				repo := federationSourceRepository{source: source}
				ctx := context.Background()
				req := &model.FetchQueryRequest{DataSourceID: source.ID, SQL: "SELECT id FROM orders ORDER BY id", BatchSize: 2, CountMode: model.QueryCountNone}
				limits := QueryBudgetLimits{MaxRows: 2, MaxBytes: 100000, MaxDuration: time.Minute}
				var page *model.FetchQueryResponse
				var err error
				var next func(*model.FetchQueryResponse) error
				var live func() int
				if mode == "pagination" {
					q := NewQueryService(repo, pool, false, newTestSQLTranslationManager()).(*queryService)
					defer q.Shutdown()
					if scenario == "budget" {
						q.budgetLimits = limits
					}
					page, err = q.FetchQuery(ctx, nil, req)
					next = func(p *model.FetchQueryResponse) error {
						_, e := q.FetchNextBatch(ctx, nil, p.QueryID, p.Slug, p.Token, 0)
						return e
					}
					live = func() int { return q.LiveSessionCount(time.Now()) }
				} else {
					q := NewStreamingService(repo, pool, newTestSQLTranslationManager()).(*streamingService)
					defer q.Shutdown()
					if scenario == "budget" {
						q.budgetLimits = limits
					}
					page, err = q.FetchWithStreaming(ctx, nil, req)
					next = func(p *model.FetchQueryResponse) error {
						_, e := q.FetchNextBatchWithStreaming(ctx, nil, p.QueryID, p.Slug, p.Token, 0)
						return e
					}
					live = func() int { return q.LiveSessionCount(time.Now()) }
				}
				if scenario == "budget" {
					if !errors.Is(err, ErrQueryResultLimitExceeded) || page != nil || live() != 0 {
						t.Fatalf("page=%+v err=%v sessions=%d", page, err, live())
					}
					return
				}
				if err != nil || page.NextURI == "" {
					t.Fatalf("initial page=%+v err=%v", page, err)
				}
				results := make(chan error, 2)
				var wg sync.WaitGroup
				for i := 0; i < 2; i++ {
					wg.Add(1)
					go func() { defer wg.Done(); results <- next(page) }()
				}
				wg.Wait()
				close(results)
				successes, failures := 0, 0
				for result := range results {
					if result == nil {
						successes++
					} else if errors.Is(result, ErrInvalidQuerySession) || errors.Is(result, ErrQuerySessionUnavailable) {
						failures++
					} else {
						t.Fatalf("unexpected replay error: %v", result)
					}
				}
				if successes != 1 || failures != 1 {
					t.Fatalf("successes=%d failures=%d", successes, failures)
				}
			})
		}
	}
}
