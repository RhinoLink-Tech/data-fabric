package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
)

const (
	sourceID = "11111111-1111-4111-8111-111111111111"
	testDB   = "data_fabric_perf"
)

type sourceRepo struct {
	repository.DataSourceRepository
	source *model.DataSource
}

func (r sourceRepo) GetByID(_ context.Context, id string) (*model.DataSource, error) {
	if id != r.source.ID {
		return nil, repository.ErrDataSourceNotFound
	}
	return r.source, nil
}

type stat struct {
	vals []time.Duration
}

func (s *stat) measure(fn func() error) error {
	start := time.Now()
	err := fn()
	d := time.Since(start)
	s.vals = append(s.vals, d)
	return err
}
func (s stat) report() (time.Duration, time.Duration) {
	v := append([]time.Duration(nil), s.vals...)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	if len(v) == 0 {
		return 0, 0
	}
	p95 := int(math.Ceil(float64(len(v))*.95)) - 1
	if p95 < 0 {
		p95 = 0
	}
	return v[(len(v)-1)/2], v[p95]
}
func uniqueTableName(prefix string) string {
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	return prefix + "_" + strings.Join(strings.Split(suffix, ""), "_")
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envInt(k string, d int) int {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	n, e := strconv.Atoi(v)
	if e != nil {
		return d
	}
	return n
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "benchmark:", e)
		os.Exit(1)
	}
}
func run() error {
	host := env("PGHOST", "localhost")
	port := envInt("PGPORT", 5432)
	user := env("PGUSER", "postgres")
	pass := os.Getenv("PGPASSWORD")
	ssl := env("PGSSLMODE", "disable")
	adminDB := env("DATA_FABRIC_PERF_ADMIN_DB", "postgres")
	n := envInt("DATA_FABRIC_PERF_SAMPLES", 100)
	warm := envInt("DATA_FABRIC_PERF_WARMUPS", 5)
	rowsN := envInt("DATA_FABRIC_PERF_ROWS", 500)
	if n < 20 || warm < 1 || rowsN < 1 || rowsN > 9000 {
		return errors.New("samples >=20, warmups >=1, rows 1..9000 required")
	}
	if ssl != "disable" && ssl != "require" {
		return fmt.Errorf("current driver supports PGSSLMODE=disable or require, got %q", ssl)
	}
	cfg := model.DataSourceConfig{Host: host, Port: port, Database: adminDB, Username: user, Password: pass, SSL: ssl == "require", MaxPoolSize: 4, Timeout: 30}
	drv, e := database.GetDriverRegistry().GetDriver(model.DatabaseTypePostgreSQL)
	if e != nil {
		return e
	}
	admin, e := drv.Open(drv.BuildDSN(&cfg))
	if e != nil {
		return e
	}
	defer admin.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e = admin.PingContext(ctx); e != nil {
		return fmt.Errorf("connect as %q: %w", user, e)
	}
	var exists bool
	if e = admin.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", testDB).Scan(&exists); e != nil {
		return e
	}
	created := false
	if !exists {
		if _, e = admin.Exec("CREATE DATABASE " + testDB); e != nil {
			return fmt.Errorf("create %s (account needs CREATEDB): %w", testDB, e)
		}
		created = true
	}
	cfg.Database = testDB
	src := &model.DataSource{ID: sourceID, Name: "temporary-perf-source", Type: model.DatabaseTypePostgreSQL, Status: model.DataSourceStatusActive, Config: cfg}
	direct, e := drv.Open(drv.BuildDSN(&cfg))
	if e != nil {
		return e
	}
	defer direct.Close()
	direct.SetMaxOpenConns(4)
	direct.SetMaxIdleConns(4)
	if e = direct.PingContext(ctx); e != nil {
		return e
	}
	var pgVersion string
	if e = direct.QueryRowContext(ctx, "SHOW server_version").Scan(&pgVersion); e != nil {
		return fmt.Errorf("read PostgreSQL version: %w", e)
	}
	tableName := uniqueTableName("df_perf_rows")
	if _, e = direct.Exec("CREATE TABLE " + tableName + " (id BIGINT PRIMARY KEY, payload TEXT NOT NULL)"); e != nil {
		return fmt.Errorf("create isolated benchmark table: %w", e)
	}
	defer func() {
		if _, cleanupErr := direct.Exec("DROP TABLE IF EXISTS " + tableName); cleanupErr != nil {
			fmt.Fprintf(os.Stderr, "temporary table cleanup failed: %v\n", cleanupErr)
		}
	}()
	if _, e = direct.Exec(fmt.Sprintf("INSERT INTO %s SELECT g, repeat('x',112) || lpad(g::text,16,'0') FROM generate_series(1,%d) AS g", tableName, rowsN)); e != nil {
		return fmt.Errorf("populate isolated benchmark table: %w", e)
	}
	dimTableName := uniqueTableName("df_perf_dim")
	if _, e = direct.Exec("CREATE TABLE " + dimTableName + " (id BIGINT PRIMARY KEY, category TEXT NOT NULL)"); e != nil {
		return fmt.Errorf("create isolated benchmark dimension table: %w", e)
	}
	defer func() {
		if _, cleanupErr := direct.Exec("DROP TABLE IF EXISTS " + dimTableName); cleanupErr != nil {
			fmt.Fprintf(os.Stderr, "temporary dimension table cleanup failed: %v\n", cleanupErr)
		}
	}()
	dimRows := rowsN / 2
	if dimRows < 1 {
		dimRows = 1
	}
	if _, e = direct.Exec(fmt.Sprintf("INSERT INTO %s SELECT g, 'category-' || (g %% 10)::text FROM generate_series(1,%d) AS g", dimTableName, dimRows)); e != nil {
		return fmt.Errorf("populate isolated benchmark dimension table: %w", e)
	}
	pool := database.NewConnectionPool(nil, nil, true)
	defer pool.CloseAll()
	qs := service.NewQueryService(sourceRepo{source: src}, pool, false)
	fullPageRows := rowsN/5 - 7
	if fullPageRows < 1 {
		fullPageRows = 1
	}
	pointID := rowsN / 2
	if pointID < 1 {
		pointID = 1
	}
	selectiveCutoff := rowsN - rowsN/10
	if selectiveCutoff >= rowsN {
		selectiveCutoff = rowsN - 1
	}
	offsetLimit := rowsN - 20
	if offsetLimit < 1 {
		offsetLimit = 1
	}
	broadLimit := rowsN * 79 / 100
	if broadLimit < 1 {
		broadLimit = 1
	}
	workloads := []struct {
		name, sql string
		batch     int
		fullPage  bool
		wantRows  int
	}{
		{"small_constant", "SELECT 1 AS id, 'data-fabric' AS payload", 10, false, 0},
		{"point_lookup", fmt.Sprintf("SELECT id, payload FROM %s WHERE id = %d", tableName, pointID), rowsN + 10, false, 0},
		{"selective_filter", fmt.Sprintf("SELECT id, payload FROM %s WHERE id > %d ORDER BY id", tableName, selectiveCutoff), rowsN + 10, false, 0},
		{"broad_filter", fmt.Sprintf("SELECT id, payload FROM %s WHERE id <= %d ORDER BY id", tableName, broadLimit), rowsN + 10, false, 0},
		{"ordered_scan", fmt.Sprintf("SELECT id, payload FROM %s ORDER BY id", tableName), rowsN + 10, false, 0},
		{"sort_unindexed_text", fmt.Sprintf("SELECT id, payload FROM %s ORDER BY payload DESC", tableName), rowsN + 10, false, 0},
		{"offset_page", fmt.Sprintf("SELECT id, payload FROM %s ORDER BY id LIMIT %d OFFSET %d", tableName, offsetLimit, rowsN/2), rowsN + 10, false, 0},
		{"wide_projection", fmt.Sprintf("SELECT id, payload || payload || payload || payload AS wide_payload FROM %s ORDER BY id", tableName), rowsN + 10, false, 0},
		{"aggregate_statistics", fmt.Sprintf("SELECT COUNT(*) AS row_count, SUM(id) AS sum_id, AVG(id) AS avg_id FROM %s", tableName), rowsN + 10, false, 0},
		{"grouped_aggregate", fmt.Sprintf("SELECT MOD(id, 10) AS bucket, COUNT(*) AS row_count, SUM(id) AS sum_id FROM %s GROUP BY MOD(id, 10) ORDER BY bucket", tableName), rowsN + 10, false, 0},
		{"distinct_values", fmt.Sprintf("SELECT DISTINCT MOD(id, 10) AS bucket FROM %s ORDER BY bucket", tableName), rowsN + 10, false, 0},
		{"inner_join", fmt.Sprintf("SELECT f.id, d.category, f.payload FROM %s AS f INNER JOIN %s AS d ON d.id = f.id WHERE f.id < %d ORDER BY f.id", tableName, dimTableName, rowsN+1), rowsN + 10, false, 0},
		{"left_join", fmt.Sprintf("SELECT f.id, d.category, f.payload FROM %s AS f LEFT JOIN %s AS d ON d.id = f.id ORDER BY f.id", tableName, dimTableName), rowsN + 10, false, 0},
		{"full_page_count", fmt.Sprintf("SELECT id, payload FROM %s WHERE id <= %d ORDER BY id", tableName, fullPageRows), fullPageRows, true, fullPageRows},
	}
	fmt.Printf("environment: PostgreSQL %s; Go %s; %s/%s; CPUs=%d\n", pgVersion, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	fmt.Printf("database=%s created_now=%t rows=%d samples=%d warmups=%d; temporary synthetic table is removed after the run\n\n", testDB, created, rowsN, n, warm)
	for _, w := range workloads {
		directQuery := func() error {
			total := int64(0)
			if w.fullPage {
				if e := direct.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM ("+w.sql+") AS df_count").Scan(&total); e != nil {
					return e
				}
			}
			directSQL := fmt.Sprintf("%s LIMIT %d", w.sql, w.batch)
			queryUpper := strings.ToUpper(w.sql)
			if strings.Contains(queryUpper, " LIMIT ") || strings.Contains(queryUpper, " OFFSET ") {
				directSQL = fmt.Sprintf("SELECT * FROM (%s) AS benchmark_query LIMIT %d OFFSET 0", w.sql, w.batch)
			}
			rs, e := direct.QueryContext(context.Background(), directSQL)
			if e != nil {
				return e
			}
			defer rs.Close()
			cols, e := rs.Columns()
			if e != nil {
				return e
			}
			data := make([][]any, 0, w.batch)
			for rs.Next() {
				v := make([]any, len(cols))
				ptr := make([]any, len(cols))
				for i := range v {
					ptr[i] = &v[i]
				}
				if e = rs.Scan(ptr...); e != nil {
					return e
				}
				for i, x := range v {
					if b, ok := x.([]byte); ok {
						v[i] = string(b)
					}
				}
				data = append(data, v)
			}
			if e = rs.Err(); e != nil {
				return e
			}
			if len(data) < w.batch {
				total = int64(len(data))
			} else if !w.fullPage {
				return fmt.Errorf("workload %s unexpectedly filled its page", w.name)
			}
			_, e = json.Marshal(map[string]any{"columns": cols, "rows": data, "totalCount": total})
			return e
		}
		gateway := func() error {
			r, e := qs.FetchQuery(context.Background(), security.SystemCaller(), &model.FetchQueryRequest{DataSourceID: sourceID, SQL: w.sql, BatchSize: w.batch, Type: 1, Timeout: 60})
			if e != nil {
				return e
			}
			if w.fullPage {
				if len(r.Entries) != w.batch || r.TotalCount != w.wantRows {
					return fmt.Errorf("gateway full-page rows=%d totalCount=%d, want rows=%d totalCount=%d", len(r.Entries), r.TotalCount, w.batch, w.wantRows)
				}
			} else if len(r.Entries) >= w.batch || r.TotalCount != len(r.Entries) {
				return fmt.Errorf("gateway rows=%d totalCount=%d, expected a partial page with an exact inferred total", len(r.Entries), r.TotalCount)
			}
			_, e = json.Marshal(r)
			return e
		}
		names := []string{"direct", "data_fabric_service"}
		ops := []func() error{directQuery, gateway}
		for i := 0; i < warm; i++ {
			for j, f := range ops {
				if e := f(); e != nil {
					return fmt.Errorf("%s workload %s warmup %d: %w", names[j], w.name, i, e)
				}
			}
		}
		got := map[string]*stat{}
		for _, name := range names {
			got[name] = &stat{}
		}
		for i := 0; i < n; i++ {
			for k := 0; k < len(ops); k++ {
				j := (k + i) % len(ops)
				if e := got[names[j]].measure(ops[j]); e != nil {
					return fmt.Errorf("%s workload %s sample: %w", names[j], w.name, e)
				}
			}
		}
		fmt.Printf("[%s]\n", w.name)
		for _, name := range names {
			p50, p95 := got[name].report()
			fmt.Printf("  %-20s p50=%s p95=%s\n", name, p50.Round(time.Microsecond), p95.Round(time.Microsecond))
		}
		g50, g95 := got["data_fabric_service"].report()
		d50, d95 := got["direct"].report()
		fmt.Printf("  percentile value gap: p50=%s p95=%s\n\n", (g50 - d50).Round(time.Microsecond), (g95 - d95).Round(time.Microsecond))
	}
	if _, e = direct.Exec("DROP TABLE IF EXISTS " + tableName); e != nil {
		return fmt.Errorf("drop temporary benchmark table: %w", e)
	}
	var tableRemains bool
	if e = direct.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname = current_schema() AND tablename = $1)", tableName).Scan(&tableRemains); e != nil {
		return fmt.Errorf("verify temporary table cleanup: %w", e)
	}
	if tableRemains {
		return errors.New("temporary benchmark table remains after cleanup")
	}
	if _, e = direct.Exec("DROP TABLE IF EXISTS " + dimTableName); e != nil {
		return fmt.Errorf("drop temporary dimension table: %w", e)
	}
	if e = direct.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname = current_schema() AND tablename = $1)", dimTableName).Scan(&tableRemains); e != nil {
		return fmt.Errorf("verify temporary dimension table cleanup: %w", e)
	}
	if tableRemains {
		return errors.New("temporary dimension table remains after cleanup")
	}
	fmt.Println("Temporary synthetic tables removed and verified.")
	fmt.Println("Scope: in-process query service; excludes HTTP, auth middleware, and client-to-gateway network hop.")
	fmt.Println("Workloads cover point lookup, selective and broad filters, indexed and unindexed ordering, offset paging, narrow and wide projections, scalar/grouped aggregates, DISTINCT, inner/left joins, and full-page count behavior.")
	return nil
}
