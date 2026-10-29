// Command real_source_validation drives the D14 real-database operation
// matrix (#35) against live MySQL and PostgreSQL instances. It runs the
// production stack in-process — SQL validation, the read-only connection
// pool, and the pagination and streaming services behind the unified query
// service — and records one PASS/FAIL verdict with evidence per operation.
// The run log feeds docs/validation/real-source-matrix.md.
//
// All coordinates and credentials arrive via environment variables
// (DF_VALIDATE_<ENGINE>_HOST/PORT/DATABASE/ADMIN_USER/ADMIN_PASSWORD/
// READER_USER/READER_PASSWORD, plus DF_VALIDATE_ENGINES and
// DF_VALIDATE_POSTGRES_SSLMODE). scripts/validation/run_real_source_validation.sh
// provisions ephemeral containers and exports them; no fixtures or
// credentials are committed.
//
// Exit codes: 0 every check passed, 1 at least one check failed, 2 the
// environment is incomplete (missing variables, fixture setup failed).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	unifiedservice "github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
)

const (
	exitOK                 = 0
	exitValidationFailed   = 1
	exitEnvironmentMissing = 2
)

// Stable client-facing failure texts (internal/service/query_errors.go).
// The error-mapping check asserts the exact strings, so a wording change
// fails loudly here as well.
const (
	msgSyntax   = "The query is invalid or uses unsupported SQL syntax."
	msgGeneric  = "The data-source query failed. Check the SQL dialect, catalog and schema, permissions, and query-engine availability."
	msgReadOnly = "Only read-only SELECT queries are supported."
	msgCanceled = "The query was cancelled before completion."
)

// Both engines reject writes from a read-only session with a
// "read-only transaction"-shaped error (MySQL error 1792 says "READ ONLY
// transaction", PostgreSQL's GUC says "read-only transaction") — #20's
// database-side enforcement.
const readOnlyFragment = "read-only transaction"

const (
	goodSourceID = "8a2b3c4d-0001-4a41-8a41-000000000001"
	badSourceID  = "8a2b3c4d-0002-4a42-8a42-000000000002"
)

// typeCell pins one column of the types fixture: the encoded wire value it
// must produce (nil / int64 / string / bool / time.Time) and the
// standardized column type each execution mode reports. The two type
// columns differ where the pagination path (utils DataTypeMapper) and the
// streaming path (streamingService generic mapper) genuinely disagree —
// those divergences are recorded as notes for the findings section of the
// matrix doc, not silently normalized.
type typeCell struct {
	name  string
	value interface{}
	// pagination reports via utils.DataTypeMapper, streaming via the
	// service-side generic mapper; both derived from source, confirmed
	// against a real run.
	paginationType model.StandardizedType
	streamingType  model.StandardizedType
}

type engineSpec struct {
	name    string
	dbType  model.DatabaseType
	sslmode string // postgres admin DSN

	host     string
	port     int
	database string

	adminDSN   string
	readerUser string
	readerPass string

	tableMain  string
	tableTypes string
	tableSacr  string

	setup    []string
	teardown []string

	versionSQL  string
	placeholder func(n int) string
	cells       []typeCell
}

// mapSourceRepo serves the in-memory sources the harness registers. Only
// GetByID is on the query path; the embedded interface covers the rest.
type mapSourceRepo struct {
	repository.DataSourceRepository
	sources map[string]*model.DataSource
}

func (r mapSourceRepo) GetByID(_ context.Context, id string) (*model.DataSource, error) {
	if s, ok := r.sources[id]; ok {
		return s, nil
	}
	return nil, repository.ErrDataSourceNotFound
}

type validator struct {
	spec  engineSpec
	admin *sql.DB
	pool  *database.ConnectionPool
	// unified is the default gateway configuration (auto mode, streaming
	// preferred); paged pins execution_mode=pagination so Type 2 drives
	// the pagination service's continuation sessions — both are documented
	// deployments, and the matrix validates each.
	unified  unifiedservice.UnifiedQueryService
	paged    unifiedservice.UnifiedQueryService
	source   *model.DataSource
	caller   *security.Caller
	failures int
	notes    []string
}

func (v *validator) outcome(op string, pass bool, evidence string) {
	status := "PASS"
	if !pass {
		status = "FAIL"
		v.failures++
	}
	fmt.Printf("  [%s] %-14s %s\n", status, op, evidence)
}

func (v *validator) note(format string, args ...interface{}) {
	line := fmt.Sprintf(format, args...)
	v.notes = append(v.notes, line)
	fmt.Printf("  [note] %s\n", line)
}

func (v *validator) fetch(ctx context.Context, u unifiedservice.UnifiedQueryService, sourceID, query string, params []interface{}, typ, batch int) (*model.FetchQueryResponse, error) {
	return u.FetchQuery(ctx, v.caller, &model.FetchQueryRequest{
		DataSourceID: sourceID,
		SQL:          query,
		Parameters:   params,
		Type:         typ,
		BatchSize:    batch,
		Timeout:      60,
	})
}

func (v *validator) next(ctx context.Context, u unifiedservice.UnifiedQueryService, res *model.FetchQueryResponse, batch int) (*model.FetchQueryResponse, error) {
	return u.FetchNextBatch(ctx, v.caller, res.QueryID, res.Slug, res.Token, batch)
}

// rows flattens the pointer-heavy Entries shape into plain rows.
func rows(res *model.FetchQueryResponse) [][]interface{} {
	out := make([][]interface{}, 0, len(res.Entries))
	for _, entry := range res.Entries {
		if entry != nil {
			out = append(out, *entry)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// cellMatches compares an encoded wire value against its expectation.
// time.Time compares by formatted instant, so timezone representation
// differences do not masquerade as encoding bugs.
func cellMatches(got, want interface{}) bool {
	switch w := want.(type) {
	case nil:
		return got == nil
	case int64:
		g, ok := got.(int64)
		return ok && g == w
	case string:
		g, ok := got.(string)
		return ok && g == w
	case bool:
		g, ok := got.(bool)
		return ok && g == w
	case time.Time:
		g, ok := got.(time.Time)
		return ok && g.Format(time.RFC3339Nano) == w.Format(time.RFC3339Nano)
	}
	return false
}

func describe(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return fmt.Sprintf("%q", truncate(v, 40))
	default:
		return fmt.Sprintf("%v", v)
	}
}

// 1. connect — the reader account reaches the engine through the gateway
// stack end to end.
func (v *validator) checkConnect(ctx context.Context) {
	start := time.Now()
	res, err := v.fetch(ctx, v.unified, goodSourceID, v.spec.versionSQL, nil, 1, 10)
	if err != nil {
		v.outcome("connect", false, "version query failed: "+truncate(err.Error(), 100))
		return
	}
	all := rows(res)
	if len(all) == 0 || len(all[0]) == 0 {
		v.outcome("connect", false, "version query returned no rows")
		return
	}
	v.outcome("connect", true, fmt.Sprintf("%s (%.0fms)", truncate(fmt.Sprintf("%v", all[0][0]), 64), elapsedMs(start)))
}

func elapsedMs(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000.0
}

// 2. read-only — the pool the services use opens every connection with a
// read-only session; a direct INSERT on that pool connection must be
// rejected by the engine itself (not just by SQL validation), while reads
// keep working.
func (v *validator) checkReadOnly(ctx context.Context) {
	db, err := v.pool.GetConnection(ctx, v.source)
	if err != nil {
		v.outcome("read-only", false, "pool connection failed: "+truncate(err.Error(), 100))
		return
	}

	_, err = db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (id, note) VALUES (2, 'must-not-commit')", v.spec.tableSacr))
	if err == nil {
		// The write went through — remove the evidence row and fail.
		_, _ = db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = 2", v.spec.tableSacr))
		v.outcome("read-only", false, "engine accepted an INSERT on a read-only session")
		return
	}
	if !strings.Contains(strings.ReplaceAll(strings.ToLower(err.Error()), "read only", "read-only"), readOnlyFragment) {
		v.outcome("read-only", false, "INSERT rejected with unexpected error: "+truncate(err.Error(), 120))
		return
	}

	var count int
	if err := db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", v.spec.tableSacr)).Scan(&count); err != nil || count != 1 {
		v.outcome("read-only", false, fmt.Sprintf("read on the same session broken (count=%d err=%v)", count, err))
		return
	}
	v.outcome("read-only", true, fmt.Sprintf("INSERT rejected (%s); reads still work", readOnlyFragment))
}

// 3. parameters — bind values ride through the driver, never into the SQL
// text: an integer lookup, a string lookup, and a classic injection payload
// that must come back as inert data (zero rows).
func (v *validator) checkParameters(ctx context.Context) {
	const injection = "' OR '1'='1"
	cases := []struct {
		label  string
		query  string
		params []interface{}
		want   int
	}{
		{"int", fmt.Sprintf("SELECT id FROM %s WHERE id = %s", v.spec.tableMain, v.spec.placeholder(1)), []interface{}{7}, 1},
		{"string", fmt.Sprintf("SELECT id FROM %s WHERE pad = %s", v.spec.tableMain, v.spec.placeholder(1)), []interface{}{"pad-7"}, 1},
		{"injection", fmt.Sprintf("SELECT id FROM %s WHERE pad = %s", v.spec.tableMain, v.spec.placeholder(1)), []interface{}{injection}, 0},
	}
	parts := make([]string, 0, len(cases))
	pass := true
	for _, c := range cases {
		res, err := v.fetch(ctx, v.unified, goodSourceID, c.query, c.params, 1, 10)
		if err != nil {
			parts = append(parts, c.label+"=error")
			pass = false
			continue
		}
		got := len(rows(res))
		parts = append(parts, fmt.Sprintf("%s→%d rows", c.label, got))
		if got != c.want {
			pass = false
		}
	}
	v.outcome("parameters", pass, strings.Join(parts, ", "))
}

// collectAllPages walks a result set to exhaustion with the given batch
// size and returns every row in order.
func (v *validator) collectAllPages(ctx context.Context, u unifiedservice.UnifiedQueryService, res *model.FetchQueryResponse, batch int) ([][]interface{}, error) {
	all := rows(res)
	for res.NextURI != "" {
		nextRes, err := v.next(ctx, u, res, batch)
		if err != nil {
			return all, err
		}
		all = append(all, rows(nextRes)...)
		res = nextRes
	}
	return all, nil
}

// 4. pagination — the unified service's Type 1 is the synchronous
// single-shot fetch (first batch, no continuation session); with
// execution_mode=pagination, Type 2 drives the pagination service's
// continuation sessions: deterministic ordering, page-by-page token
// rotation, exact totalCount on full pages.
func (v *validator) checkPagination(ctx context.Context) {
	start := time.Now()
	query := fmt.Sprintf("SELECT id, pad FROM %s WHERE id < 30 ORDER BY id", v.spec.tableMain)

	// Type 1 through auto mode: one batch, no session to continue.
	sync, err := v.fetch(ctx, v.unified, goodSourceID, query, nil, 1, 10)
	if err != nil {
		v.outcome("pagination", false, "type-1 fetch failed: "+truncate(err.Error(), 100))
		return
	}
	syncOK := sync.ExecutionMode == "pagination" &&
		len(rows(sync)) == 10 &&
		sync.NextURI == "" &&
		sync.TotalCount == 30

	// Type 2 with execution_mode=pagination: full continuation walk.
	first, err := v.fetch(ctx, v.paged, goodSourceID, query, nil, 2, 10)
	if err != nil {
		v.outcome("pagination", false, "paginated fetch failed: "+truncate(err.Error(), 100))
		return
	}
	if first.ExecutionMode != "pagination" {
		v.outcome("pagination", false, fmt.Sprintf("executionMode=%q, want \"pagination\"", first.ExecutionMode))
		return
	}
	if first.TotalCount != 30 {
		v.outcome("pagination", false, fmt.Sprintf("first-page totalCount=%d, want 30 (full-page contract)", first.TotalCount))
		return
	}
	all, err := v.collectAllPages(ctx, v.paged, first, 10)
	if err != nil {
		v.outcome("pagination", false, "continuation failed: "+truncate(err.Error(), 100))
		return
	}
	if !orderedRowsMatch(all) {
		v.outcome("pagination", false, fmt.Sprintf("row mismatch: %d rows, first=%v last=%v", len(all), describeID(all, 0), describeID(all, len(all)-1)))
		return
	}
	v.outcome("pagination", syncOK,
		fmt.Sprintf("type1 single-shot: 10 rows, no session, totalCount=30; type2 paginated: 30 rows / 3 pages of 10, ids 0..29 in order (%.0fms total)", elapsedMs(start)))
}

func describeID(all [][]interface{}, i int) interface{} {
	if i < 0 || i >= len(all) || len(all[i]) == 0 {
		return "-"
	}
	return all[i][0]
}

func orderedRowsMatch(all [][]interface{}) bool {
	if len(all) != 30 {
		return false
	}
	for i, row := range all {
		if len(row) != 2 || row[0] != int64(i) || row[1] != fmt.Sprintf("pad-%d", i) {
			return false
		}
	}
	return true
}

// 5. streaming — Type 2 on a streaming-capable engine must actually stream:
// the response says so, and the same rows arrive batch by batch.
func (v *validator) checkStreaming(ctx context.Context) {
	query := fmt.Sprintf("SELECT id, pad FROM %s WHERE id < 30 ORDER BY id", v.spec.tableMain)
	first, err := v.fetch(ctx, v.unified, goodSourceID, query, nil, 2, 10)
	if err != nil {
		v.outcome("streaming", false, "first batch failed: "+truncate(err.Error(), 100))
		return
	}
	if first.ExecutionMode != "streaming" {
		v.outcome("streaming", false, fmt.Sprintf("executionMode=%q, want \"streaming\" (silent fallback?)", first.ExecutionMode))
		return
	}
	all, err := v.collectAllPages(ctx, v.unified, first, 10)
	if err != nil {
		v.outcome("streaming", false, "stream continuation failed: "+truncate(err.Error(), 100))
		return
	}
	if !orderedRowsMatch(all) {
		v.outcome("streaming", false, fmt.Sprintf("row mismatch: %d rows, first=%v last=%v", len(all), describeID(all, 0), describeID(all, len(all)-1)))
		return
	}
	v.outcome("streaming", true, "30 rows / 3 streamed batches, ids 0..29 in order")
}

// 6. types — one row covering the dialect's core scalar types in both
// execution modes: NULL stays nil, DECIMAL/NUMERIC stays exact text,
// BIGINT keeps full int64 precision, text bytes stay strings.
func (v *validator) checkTypes(ctx context.Context) {
	query := fmt.Sprintf("SELECT * FROM %s", v.spec.tableTypes)
	byMode := map[string]*model.FetchQueryResponse{}
	for _, mode := range []struct {
		label string
		typ   int
	}{
		{"pagination", 1},
		{"streaming", 2},
	} {
		res, err := v.fetch(ctx, v.unified, goodSourceID, query, nil, mode.typ, 100)
		if err != nil {
			v.outcome("types", false, mode.label+" fetch failed: "+truncate(err.Error(), 100))
			return
		}
		byMode[mode.label] = res
	}

	pass := true
	var problems []string
	for mode, res := range byMode {
		all := rows(res)
		if len(all) != 1 {
			v.outcome("types", false, fmt.Sprintf("%s returned %d rows, want 1", mode, len(all)))
			return
		}
		wantType := map[string]func(typeCell) model.StandardizedType{
			"pagination": func(c typeCell) model.StandardizedType { return c.paginationType },
			"streaming":  func(c typeCell) model.StandardizedType { return c.streamingType },
		}[mode]
		for i, cell := range v.spec.cells {
			if i >= len(res.Columns) {
				problems = append(problems, fmt.Sprintf("%s: missing column %s", mode, cell.name))
				pass = false
				continue
			}
			if got := res.Columns[i].Name; got != cell.name {
				problems = append(problems, fmt.Sprintf("%s col %d name=%q want %q", mode, i, got, cell.name))
				pass = false
			}
			if got := res.Columns[i].Type; got != wantType(cell) {
				problems = append(problems, fmt.Sprintf("%s %s type=%s want %s", mode, cell.name, got, wantType(cell)))
				pass = false
			}
			var value interface{}
			if i < len(all[0]) {
				value = all[0][i]
			}
			if !cellMatches(value, cell.value) {
				problems = append(problems, fmt.Sprintf("%s %s value=%s want %s", mode, cell.name, describe(value), describe(cell.value)))
				pass = false
			}
		}
	}

	// Cross-mode standardized-type divergences are expected findings for
	// the matrix doc (BIGINT/TEXT/DATETIME style disagreements between the
	// two mapping paths), recorded as notes rather than failures.
	for _, cell := range v.spec.cells {
		if cell.paginationType != cell.streamingType {
			v.note("type-mapping divergence: %s pagination=%s streaming=%s", cell.name, cell.paginationType, cell.streamingType)
		}
	}

	typeNames := make([]string, 0, len(byMode["pagination"].Columns))
	for _, col := range byMode["pagination"].Columns {
		typeNames = append(typeNames, col.Name+"="+string(col.Type))
	}
	if pass {
		v.outcome("types", true, "10 columns exact in both modes: "+strings.Join(typeNames, ","))
	} else {
		v.outcome("types", false, strings.Join(problems, "; "))
	}
}

// 7. cancellation — a heavy self-join (≈1.25 billion index probes) must
// die promptly when the caller cancels, in both execution modes, and the
// pool must still serve queries afterwards. SLEEP-style probes are
// blacklisted by the validator, so a real workload is the only way to
// exercise cancellation.
func (v *validator) checkCancellation(ctx context.Context) {
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s a JOIN %s b ON b.id <= a.id", v.spec.tableMain, v.spec.tableMain)

	runOne := func(label string, typ int) (bool, string) {
		queryCtx, cancel := context.WithCancel(ctx)
		go func() {
			time.Sleep(700 * time.Millisecond)
			cancel()
		}()
		start := time.Now()
		_, err := v.fetch(queryCtx, v.unified, goodSourceID, query, nil, typ, 100)
		elapsed := time.Since(start)
		cancel()
		if err == nil {
			return false, fmt.Sprintf("%s: query completed despite cancel (%.1fs)", label, elapsed.Seconds())
		}
		if elapsed >= 5*time.Second {
			return false, fmt.Sprintf("%s: cancel took %.1fs (limit 5s)", label, elapsed.Seconds())
		}
		return true, fmt.Sprintf("%s canceled at %.2fs [%s]", label, elapsed.Seconds(), service.SafeQueryErrorMessage(err))
	}

	p1, e1 := runOne("pagination", 1)
	s1, e2 := runOne("streaming", 2)

	// The pool must be unharmed: a plain query right after cancellation.
	var ok int
	after, err := v.fetch(ctx, v.unified, goodSourceID, "SELECT 1", nil, 1, 10)
	if err == nil && len(rows(after)) == 1 {
		ok = 1
	}

	pass := p1 && s1 && ok == 1
	evidence := e1 + "; " + e2
	if ok == 0 {
		evidence += "; pool unusable after cancellation"
	}
	v.outcome("cancellation", pass, evidence)
}

// 8. error-mapping — three failure classes reach the caller as stable,
// credential-free text: syntax errors caught by validation, engine errors
// mapped to the generic message, and non-SELECT statements rejected.
func (v *validator) checkErrorMapping(ctx context.Context) {
	cases := []struct {
		label string
		query string
		want  string
	}{
		{"syntax", fmt.Sprintf("SELECT FROM %s", v.spec.tableMain), msgSyntax},
		{"missing-table", fmt.Sprintf("SELECT * FROM %s_missing_table", v.spec.tableMain), msgGeneric},
		{"non-select", fmt.Sprintf("DELETE FROM %s WHERE id = 1", v.spec.tableSacr), msgReadOnly},
	}

	parts := make([]string, 0, len(cases))
	pass := true
	for _, c := range cases {
		_, err := v.fetch(ctx, v.unified, goodSourceID, c.query, nil, 1, 10)
		if err == nil {
			parts = append(parts, c.label+"=unexpectedly succeeded")
			pass = false
			continue
		}
		got := service.SafeQueryErrorMessage(err)
		if got != c.want {
			parts = append(parts, fmt.Sprintf("%s=%q", c.label, got))
			pass = false
			continue
		}
		// The mapped text must never carry credentials.
		if strings.Contains(got, v.spec.readerPass) || strings.Contains(got, v.spec.adminDSN) {
			parts = append(parts, c.label+"=leaked credentials")
			pass = false
			continue
		}
		parts = append(parts, c.label+"=stable")
	}
	v.outcome("error-mapping", pass, strings.Join(parts, ", "))
}

// 9. isolation — two concurrent result sets on the same source interleave
// their continuations without crossing pages, and a failing second source
// (bad credentials) must not disturb the first.
func (v *validator) checkIsolation(ctx context.Context, badSource *model.DataSource) {
	asc := fmt.Sprintf("SELECT id FROM %s WHERE id < 20 ORDER BY id ASC", v.spec.tableMain)
	desc := fmt.Sprintf("SELECT id FROM %s WHERE id < 20 ORDER BY id DESC", v.spec.tableMain)

	// Both queries run as paginated sessions (execution_mode=pagination,
	// Type 2) on the same source and pool.
	a1, err := v.fetch(ctx, v.paged, goodSourceID, asc, nil, 2, 7)
	if err != nil {
		v.outcome("isolation", false, "ascending query failed: "+truncate(err.Error(), 100))
		return
	}
	b1, err := v.fetch(ctx, v.paged, goodSourceID, desc, nil, 2, 7)
	if err != nil {
		v.outcome("isolation", false, "descending query failed: "+truncate(err.Error(), 100))
		return
	}

	// A source with wrong credentials fails — through the mapped message,
	// not a raw driver error — while the good source keeps paging.
	badParts := "bad-creds=stable"
	if _, err := v.fetch(ctx, v.unified, badSourceID, "SELECT 1", nil, 1, 10); err == nil {
		badParts = "bad-creds=unexpectedly connected"
	} else if mapped := service.SafeQueryErrorMessage(err); strings.Contains(mapped, badSource.Config.Password) || mapped == "" {
		badParts = "bad-creds=unsafe message"
	}

	// Interleave the continuations: A, B, A, B, A, B.
	responses := []*model.FetchQueryResponse{a1, b1}
	ordered := map[string][][]interface{}{"A": rows(a1), "B": rows(b1)}
	pass := true
	for i := 0; i < 2; i++ {
		for j, key := range []string{"A", "B"} {
			nextRes, err := v.next(ctx, v.paged, responses[j], 7)
			if err != nil {
				v.outcome("isolation", false, fmt.Sprintf("%s continuation %d failed: %s", key, i+2, truncate(err.Error(), 100)))
				return
			}
			ordered[key] = append(ordered[key], rows(nextRes)...)
			responses[j] = nextRes
		}
	}

	checkOrder := func(key string, wantID func(i int) int64) bool {
		got := ordered[key]
		if len(got) != 20 {
			return false
		}
		for i, row := range got {
			if len(row) != 1 || row[0] != wantID(i) {
				return false
			}
		}
		return true
	}
	ascOK := checkOrder("A", func(i int) int64 { return int64(i) })
	descOK := checkOrder("B", func(i int) int64 { return int64(19 - i) })

	pass = ascOK && descOK && badParts == "bad-creds=stable"
	evidence := fmt.Sprintf("interleaved 20+20 rows, A ascending=%v B descending=%v, %s", ascOK, descOK, badParts)
	v.outcome("isolation", pass, evidence)
}

// setup creates the three fixture tables (main 50k rows for cancellation
// and paging, types one row per scalar type, sacrificial for the read-only
// probe) with a run-unique prefix, all torn down afterwards.
func setup(admin *sql.DB, spec engineSpec) error {
	for _, stmt := range spec.setup {
		if _, err := admin.Exec(stmt); err != nil {
			return fmt.Errorf("fixture statement failed (%s): %w", truncate(stmt, 60), err)
		}
	}
	return nil
}

func teardown(admin *sql.DB, spec engineSpec) {
	for _, stmt := range spec.teardown {
		_, _ = admin.Exec(stmt)
	}
}

func buildSpec(name, runTag string, env map[string]string) engineSpec {
	prefix := "dfval_" + runTag + "_"
	spec := engineSpec{
		name:       name,
		tableMain:  prefix + "main",
		tableTypes: prefix + "types",
		tableSacr:  prefix + "sacr",
	}
	spec.host = env["HOST"]
	spec.port = mustAtoi(envOr(env, "PORT", defaultPort(name)))
	spec.database = envOr(env, "DATABASE", "data_fabric_validation")
	adminUser := envOr(env, "ADMIN_USER", defaultAdminUser(name))
	adminPass := env["ADMIN_PASSWORD"]
	spec.readerUser = envOr(env, "READER_USER", "df_reader")
	spec.readerPass = env["READER_PASSWORD"]

	switch name {
	case "mysql":
		spec.dbType = model.DatabaseTypeMySQL
		spec.adminDSN = fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", adminUser, adminPass, spec.host, spec.port, spec.database)
		spec.versionSQL = "SELECT VERSION()"
		spec.placeholder = func(int) string { return "?" }
		spec.setup = []string{
			"SET SESSION cte_max_recursion_depth = 10000",
			fmt.Sprintf("CREATE TABLE %s (id BIGINT PRIMARY KEY, pad VARCHAR(48) NOT NULL)", spec.tableMain),
			fmt.Sprintf("INSERT INTO %s WITH RECURSIVE seq(n) AS (SELECT 0 UNION ALL SELECT n + 1 FROM seq WHERE n < 999) SELECT a.n * 1000 + b.n, CONCAT('pad-', a.n * 1000 + b.n) FROM seq a JOIN seq b ON b.n < 50", spec.tableMain),
			fmt.Sprintf("CREATE TABLE %s (c_tinyint TINYINT NOT NULL, c_int INT NOT NULL, c_bigint BIGINT NOT NULL, c_dec DECIMAL(10,2) NOT NULL, c_vc VARCHAR(64) NOT NULL, c_text TEXT NOT NULL, c_date DATE NOT NULL, c_dt DATETIME NOT NULL, c_bool TINYINT(1) NOT NULL, c_null INT NULL)", spec.tableTypes),
			fmt.Sprintf("INSERT INTO %s VALUES (7, 42, 9223372036854775807, 250.10, 'hello types', 'long text value for type mapping', '2026-01-31', '2026-01-31 12:34:56', 1, NULL)", spec.tableTypes),
			fmt.Sprintf("CREATE TABLE %s (id INT PRIMARY KEY, note VARCHAR(64) NOT NULL)", spec.tableSacr),
			fmt.Sprintf("INSERT INTO %s VALUES (1, 'sacrificial row')", spec.tableSacr),
		}
		// MySQL 8.4 without parseTime: DATE and DATETIME scan as text bytes
		// and reach the wire as strings (BuildDSN sets no parseTime).
		spec.cells = []typeCell{
			{"c_tinyint", int64(7), model.TypeInteger, model.TypeInteger},
			{"c_int", int64(42), model.TypeInteger, model.TypeInteger},
			{"c_bigint", int64(9223372036854775807), model.TypeBigInt, model.TypeBigInt},
			{"c_dec", "250.10", model.TypeDecimal, model.TypeDecimal},
			{"c_vc", "hello types", model.TypeString, model.TypeString},
			{"c_text", "long text value for type mapping", model.TypeText, model.TypeText},
			{"c_date", "2026-01-31", model.TypeDate, model.TypeDate},
			{"c_dt", "2026-01-31 12:34:56", model.TypeDateTime, model.TypeDateTime},
			{"c_bool", int64(1), model.TypeInteger, model.TypeInteger},
			{"c_null", nil, model.TypeInteger, model.TypeInteger},
		}
	case "postgres":
		spec.dbType = model.DatabaseTypePostgreSQL
		spec.sslmode = envOr(env, "SSLMODE", "disable")
		u := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(adminUser, adminPass),
			Host:     net.JoinHostPort(spec.host, strconv.Itoa(spec.port)),
			Path:     "/" + spec.database,
			RawQuery: "sslmode=" + url.QueryEscape(spec.sslmode),
		}
		spec.adminDSN = u.String()
		spec.versionSQL = "SELECT version()"
		spec.placeholder = func(n int) string { return "$" + strconv.Itoa(n) }
		spec.setup = []string{
			fmt.Sprintf("CREATE TABLE %s (id BIGINT PRIMARY KEY, pad VARCHAR(48) NOT NULL)", spec.tableMain),
			fmt.Sprintf("INSERT INTO %s SELECT g, 'pad-' || g FROM generate_series(0, 49999) AS g", spec.tableMain),
			fmt.Sprintf("CREATE TABLE %s (c_small SMALLINT NOT NULL, c_int INTEGER NOT NULL, c_bigint BIGINT NOT NULL, c_dec NUMERIC(10,2) NOT NULL, c_vc VARCHAR(64) NOT NULL, c_text TEXT NOT NULL, c_date DATE NOT NULL, c_ts TIMESTAMP NOT NULL, c_bool BOOLEAN NOT NULL, c_null INTEGER NULL)", spec.tableTypes),
			fmt.Sprintf("INSERT INTO %s VALUES (7, 42, 9223372036854775807, 250.10, 'hello types', 'long text value for type mapping', '2026-01-31', '2026-01-31 12:34:56', true, NULL)", spec.tableTypes),
			fmt.Sprintf("CREATE TABLE %s (id INT PRIMARY KEY, note VARCHAR(64) NOT NULL)", spec.tableSacr),
			fmt.Sprintf("INSERT INTO %s VALUES (1, 'sacrificial row')", spec.tableSacr),
		}
		spec.cells = []typeCell{
			// lib/pq reports int2/int4/int8; the pagination mapper's
			// trailing "int" prefix folds all three to integer.
			{"c_small", int64(7), model.TypeInteger, model.TypeInteger},
			{"c_int", int64(42), model.TypeInteger, model.TypeInteger},
			{"c_bigint", int64(9223372036854775807), model.TypeInteger, model.TypeInteger},
			{"c_dec", "250.10", model.TypeDecimal, model.TypeDecimal},
			{"c_vc", "hello types", model.TypeString, model.TypeString},
			{"c_text", "long text value for type mapping", model.TypeText, model.TypeText},
			{"c_date", time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC), model.TypeDate, model.TypeDate},
			{"c_ts", time.Date(2026, 1, 31, 12, 34, 56, 0, time.UTC), model.TypeTimestamp, model.TypeTimestamp},
			{"c_bool", true, model.TypeBoolean, model.TypeBoolean},
			{"c_null", nil, model.TypeInteger, model.TypeInteger},
		}
	}

	spec.teardown = []string{
		fmt.Sprintf("DROP TABLE IF EXISTS %s", spec.tableSacr),
		fmt.Sprintf("DROP TABLE IF EXISTS %s", spec.tableTypes),
		fmt.Sprintf("DROP TABLE IF EXISTS %s", spec.tableMain),
	}
	return spec
}

func defaultPort(name string) string {
	if name == "postgres" {
		return "5432"
	}
	return "3306"
}

func defaultAdminUser(name string) string {
	if name == "postgres" {
		return "postgres"
	}
	return "root"
}

func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid port %q: %v\n", s, err)
		os.Exit(exitEnvironmentMissing)
	}
	return n
}

func loadEnv(name string) (map[string]string, []string) {
	prefix := "DF_VALIDATE_" + strings.ToUpper(name) + "_"
	keys := []string{"HOST", "PORT", "DATABASE", "ADMIN_USER", "ADMIN_PASSWORD", "READER_USER", "READER_PASSWORD", "SSLMODE"}
	env := map[string]string{}
	var missing []string
	for _, key := range keys {
		env[key] = os.Getenv(prefix + key)
	}
	if env["HOST"] == "" {
		missing = append(missing, prefix+"HOST")
	}
	if env["ADMIN_PASSWORD"] == "" {
		missing = append(missing, prefix+"ADMIN_PASSWORD")
	}
	if env["READER_PASSWORD"] == "" {
		missing = append(missing, prefix+"READER_PASSWORD")
	}
	return env, missing
}

func envOr(env map[string]string, key, def string) string {
	if v := env[key]; v != "" {
		return v
	}
	return def
}

func runEngine(ctx context.Context, spec engineSpec) (failures int, err error) {
	fmt.Printf("== %s (%s:%d/%s, reader %s) ==\n", spec.name, spec.host, spec.port, spec.database, spec.readerUser)

	admin, err := sql.Open(adminDriver(spec.name), spec.adminDSN)
	if err != nil {
		return 0, fmt.Errorf("%s: open admin connection: %w", spec.name, err)
	}
	defer admin.Close()
	admin.SetMaxOpenConns(2)
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := admin.PingContext(pingCtx); err != nil {
		return 0, fmt.Errorf("%s: admin ping failed (is the engine reachable?): %w", spec.name, err)
	}

	start := time.Now()
	if err := setup(admin, spec); err != nil {
		return 0, err
	}
	defer teardown(admin, spec)
	fmt.Printf("  fixtures ready in %.1fs: %s (50k rows), %s, %s\n", time.Since(start).Seconds(), spec.tableMain, spec.tableTypes, spec.tableSacr)

	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, true)
	defer pool.CloseAll()

	readerConfig := model.DataSourceConfig{
		Host:        spec.host,
		Port:        spec.port,
		Database:    spec.database,
		Username:    spec.readerUser,
		Password:    spec.readerPass,
		SSL:         false,
		Timeout:     30,
		MaxPoolSize: 4,
		IdleTimeout: 600,
		MaxLifetime: 1800,
	}
	source := &model.DataSource{
		ID:     goodSourceID,
		Name:   "validation-" + spec.name,
		Type:   spec.dbType,
		Config: readerConfig,
		Status: model.DataSourceStatusActive,
	}
	badSource := &model.DataSource{
		ID:     badSourceID,
		Name:   "validation-" + spec.name + "-bad-creds",
		Type:   spec.dbType,
		Config: withPassword(readerConfig, "definitely-not-the-password"),
		Status: model.DataSourceStatusActive,
	}

	repo := mapSourceRepo{sources: map[string]*model.DataSource{
		goodSourceID: source,
		badSourceID:  badSource,
	}}

	// The stack under validation: read-only pool, both execution services,
	// and two unified routings — the default auto mode (streaming
	// preferred) and execution_mode=pagination — the same wiring the
	// gateway builds, minus HTTP.
	translator := sql_translator.NewRuntimeSQLTranslationManager(sql_translator.RuntimeOptions{Enabled: false})
	pagination := service.NewQueryService(repo, pool, false, translator)
	streaming := service.NewStreamingService(repo, pool)
	unified := unifiedservice.NewUnifiedQueryService(pagination, streaming, &config.Config{
		Query: config.QueryConfig{PreferStreaming: true, ExecutionMode: "auto"},
	})
	paged := unifiedservice.NewUnifiedQueryService(pagination, streaming, &config.Config{
		Query: config.QueryConfig{PreferStreaming: false, ExecutionMode: "pagination"},
	})

	v := &validator{
		spec:    spec,
		admin:   admin,
		pool:    pool,
		unified: unified,
		paged:   paged,
		source:  source,
		caller:  security.SystemCaller(),
	}

	v.checkConnect(ctx)
	v.checkReadOnly(ctx)
	v.checkParameters(ctx)
	v.checkPagination(ctx)
	v.checkStreaming(ctx)
	v.checkTypes(ctx)
	v.checkCancellation(ctx)
	v.checkErrorMapping(ctx)
	v.checkIsolation(ctx, badSource)

	return v.failures, nil
}

func withPassword(cfg model.DataSourceConfig, password string) model.DataSourceConfig {
	cfg.Password = password
	return cfg
}

func adminDriver(name string) string {
	if name == "postgres" {
		return "postgres"
	}
	return "mysql"
}

func main() {
	// Engine selection accepts comma or space separation.
	enginesCSV := strings.TrimSpace(envVarOr("DF_VALIDATE_ENGINES", "mysql,postgres"))
	var names []string
	for _, name := range strings.Fields(strings.ReplaceAll(enginesCSV, ",", " ")) {
		names = append(names, strings.ToLower(name))
	}
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "no engines selected (DF_VALIDATE_ENGINES)")
		os.Exit(exitEnvironmentMissing)
	}

	runTag := fmt.Sprintf("%s%d", strconv.FormatInt(time.Now().Unix(), 36), os.Getpid()%1296)

	var missingAll []string
	specs := make([]engineSpec, 0, len(names))
	for _, name := range names {
		env, missing := loadEnv(name)
		if len(missing) > 0 {
			missingAll = append(missingAll, missing...)
			continue
		}
		specs = append(specs, buildSpec(name, runTag, env))
	}
	if len(missingAll) > 0 {
		fmt.Fprintf(os.Stderr, "environment incomplete — set: %s\n", strings.Join(missingAll, " "))
		os.Exit(exitEnvironmentMissing)
	}

	fmt.Printf("data-fabric real-source validation (run tag %s, engines %s)\n\n", runTag, strings.Join(names, ","))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	totalFailures := 0
	for _, spec := range specs {
		failures, err := runEngine(ctx, spec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "environment error: %v\n", err)
			os.Exit(exitEnvironmentMissing)
		}
		totalFailures += failures
		fmt.Println()
	}

	if totalFailures > 0 {
		fmt.Printf("result: %d failing check(s) — validation FAILED (exit 1)\n", totalFailures)
		os.Exit(exitValidationFailed)
	}
	fmt.Println("result: all checks passed (exit 0)")
	os.Exit(exitOK)
}

func envVarOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
