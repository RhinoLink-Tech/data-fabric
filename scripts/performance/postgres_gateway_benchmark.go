package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
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

func processCPUTime() (time.Duration, error) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, err
	}
	user := time.Duration(usage.Utime.Sec)*time.Second + time.Duration(usage.Utime.Usec)*time.Microsecond
	system := time.Duration(usage.Stime.Sec)*time.Second + time.Duration(usage.Stime.Usec)*time.Microsecond
	return user + system, nil
}

func measureResources(fn func() error, samples int) (time.Duration, uint64, error) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	cpuBefore, err := processCPUTime()
	if err != nil {
		return 0, 0, err
	}
	for i := 0; i < samples; i++ {
		if err := fn(); err != nil {
			return 0, 0, err
		}
	}
	cpuAfter, err := processCPUTime()
	if err != nil {
		return 0, 0, err
	}
	runtime.ReadMemStats(&after)
	if after.TotalAlloc < before.TotalAlloc {
		return 0, 0, errors.New("Go allocation counter moved backwards")
	}
	return (cpuAfter - cpuBefore) / time.Duration(samples), (after.TotalAlloc - before.TotalAlloc) / uint64(samples), nil
}

func processPeakRSSBytes() (uint64, error) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "VmHWM:" {
			kib, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, err
			}
			return kib * 1024, nil
		}
	}
	return 0, errors.New("VmHWM not found in /proc/self/status")
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
	translatorOptions := sql_translator.DefaultRuntimeOptions()
	translatorOptions.PythonPath = env("SQL_TRANSLATION_PYTHON_PATH", translatorOptions.PythonPath)
	translatorOptions.ScriptPath = env("SQL_TRANSLATION_SCRIPT_PATH", translatorOptions.ScriptPath)
	translatorOptions.PythonModulePath = env("SQL_TRANSLATION_PYTHON_MODULE_PATH", translatorOptions.PythonModulePath)
	translator := sql_translator.NewRuntimeSQLTranslationManager(translatorOptions)
	qs := service.NewQueryService(sourceRepo{source: src}, pool, false, translator)
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
	resourceSamples := n
	if resourceSamples > 250 {
		resourceSamples = 250
	}
	var rawFile *os.File
	var rawCSV *csv.Writer
	if rawPath := os.Getenv("DATA_FABRIC_PERF_RAW_OUTPUT"); rawPath != "" {
		rawFile, e = os.Create(rawPath)
		if e != nil {
			return fmt.Errorf("create raw results file: %w", e)
		}
		rawCSV = csv.NewWriter(rawFile)
		if e = rawCSV.Write([]string{"rows", "workload", "path", "record_type", "sample", "latency_ns", "cpu_ns_per_request", "allocated_bytes_per_request", "process_peak_rss_bytes"}); e != nil {
			_ = rawFile.Close()
			return fmt.Errorf("write raw results header: %w", e)
		}
		defer func() {
			if rawCSV != nil {
				rawCSV.Flush()
			}
			if rawFile != nil {
				_ = rawFile.Close()
			}
		}()
		fmt.Printf("raw_results=%s\n", rawPath)
	}
	fmt.Printf("environment: PostgreSQL %s; Go %s; %s/%s; CPUs=%d\n", pgVersion, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	fmt.Printf("database=%s created_now=%t rows=%d latency_samples=%d warmups=%d resource_samples_per_path=%d; temporary synthetic tables are removed after the run\n\n", testDB, created, rowsN, n, warm, resourceSamples)
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
		verifyEquivalent := func() error {
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
			cols, e := rs.Columns()
			if e != nil {
				_ = rs.Close()
				return e
			}
			directRows := make([][]any, 0, w.batch)
			for rs.Next() {
				values := make([]any, len(cols))
				dest := make([]any, len(cols))
				for i := range values {
					dest[i] = &values[i]
				}
				if e := rs.Scan(dest...); e != nil {
					_ = rs.Close()
					return e
				}
				for i, value := range values {
					if raw, ok := value.([]byte); ok {
						values[i] = string(raw)
					}
				}
				directRows = append(directRows, values)
			}
			if e := rs.Err(); e != nil {
				_ = rs.Close()
				return e
			}
			if e := rs.Close(); e != nil {
				return e
			}
			if int64(len(directRows)) < int64(w.batch) {
				total = int64(len(directRows))
			} else if !w.fullPage {
				return fmt.Errorf("direct workload %s unexpectedly filled its page", w.name)
			}
			result, e := qs.FetchQuery(context.Background(), security.SystemCaller(), &model.FetchQueryRequest{DataSourceID: sourceID, SQL: w.sql, BatchSize: w.batch, Type: 1, Timeout: 60})
			if e != nil {
				return e
			}
			if len(cols) != len(result.Columns) {
				return fmt.Errorf("column count differs: direct=%d service=%d", len(cols), len(result.Columns))
			}
			for i, name := range cols {
				if result.Columns[i].Name != name {
					return fmt.Errorf("column %d name differs: direct=%q service=%q", i, name, result.Columns[i].Name)
				}
			}
			serviceRows := make([][]interface{}, 0, len(result.Entries))
			for _, entry := range result.Entries {
				if entry == nil {
					return errors.New("service returned a nil row")
				}
				serviceRows = append(serviceRows, *entry)
			}
			directJSON, e := json.Marshal(directRows)
			if e != nil {
				return e
			}
			serviceJSON, e := json.Marshal(serviceRows)
			if e != nil {
				return e
			}
			if !bytes.Equal(directJSON, serviceJSON) {
				return errors.New("direct and service row values or JSON types differ")
			}
			if int64(result.TotalCount) != total {
				return fmt.Errorf("total count differs: direct=%d service=%d", total, result.TotalCount)
			}
			return nil
		}
		names := []string{"direct", "data_fabric_service"}
		ops := []func() error{directQuery, gateway}
		if e := verifyEquivalent(); e != nil {
			return fmt.Errorf("equivalence check for %s failed: %w", w.name, e)
		}
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
		if rawCSV != nil {
			for _, name := range names {
				for sample, value := range got[name].vals {
					if e := rawCSV.Write([]string{strconv.Itoa(rowsN), w.name, name, "latency", strconv.Itoa(sample + 1), strconv.FormatInt(value.Nanoseconds(), 10), "", "", ""}); e != nil {
						return fmt.Errorf("write raw latency sample: %w", e)
					}
				}
			}
		}
		fmt.Printf("  resource metrics (%d serial samples per path):\n", resourceSamples)
		for pathIndex, name := range names {
			cpuPerRequest, allocatedBytes, err := measureResources(ops[pathIndex], resourceSamples)
			if err != nil {
				return fmt.Errorf("%s workload %s resource sample: %w", name, w.name, err)
			}
			fmt.Printf("    %-20s cpu=%.3f ms/request allocated=%.1f B/request\n", name, float64(cpuPerRequest.Nanoseconds())/1e6, float64(allocatedBytes))
			if rawCSV != nil {
				if e := rawCSV.Write([]string{strconv.Itoa(rowsN), w.name, name, "resource_summary", "", "", strconv.FormatInt(cpuPerRequest.Nanoseconds(), 10), strconv.FormatUint(allocatedBytes, 10), ""}); e != nil {
					return fmt.Errorf("write raw resource sample: %w", e)
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
		if rawCSV != nil {
			rawCSV.Flush()
			if err := rawCSV.Error(); err != nil {
				return fmt.Errorf("flush raw results: %w", err)
			}
		}
	}
	peakRSSBytes, e := processPeakRSSBytes()
	if e != nil {
		return fmt.Errorf("measure benchmark-process peak RSS: %w", e)
	}
	fmt.Printf("benchmark process peak RSS: %.2f MiB (shared across direct/service paths; process includes the in-process service and benchmark harness)\n", float64(peakRSSBytes)/(1024*1024))
	if rawCSV != nil {
		if e := rawCSV.Write([]string{strconv.Itoa(rowsN), "suite", "benchmark_process", "process_summary", "", "", "", "", strconv.FormatUint(peakRSSBytes, 10)}); e != nil {
			return fmt.Errorf("write process RSS summary: %w", e)
		}
		rawCSV.Flush()
		if err := rawCSV.Error(); err != nil {
			return fmt.Errorf("flush raw results: %w", err)
		}
		if e := rawFile.Close(); e != nil {
			return fmt.Errorf("close raw results: %w", e)
		}
		rawFile = nil
		rawCSV = nil
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
