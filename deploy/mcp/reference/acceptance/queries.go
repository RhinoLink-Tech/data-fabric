package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
)

const (
	reader         = "app-b"
	outsider       = "app-c"
	orderedIDs     = "SELECT id FROM orders ORDER BY id"
	sessionError   = "The query session is invalid or has expired. Submit the query again."
	sourceDenied   = "You do not have access to this data source"
	tableDenied    = "You do not have permission to access one or more tables"
	readonlyError  = "Only read-only SELECT queries are supported."
	invalidQuery   = "The query is invalid or uses unsupported SQL syntax."
	unsafeQuery    = "The query did not pass SQL safety validation."
	sourceFailure  = "The data-source query failed. Check the SQL dialect, catalog and schema, permissions, and query-engine availability."
	queryTimeout   = "The query timed out. Reduce the query scope or increase the request timeout."
	queryCancelled = "The query was cancelled before completion."
	sourceHidden   = "data source not found or access denied"
	tableHidden    = "table not found or access denied"
	capacityError  = "The gateway is handling the maximum number of queries for this tenant. Retry shortly."
	malformed      = "SELECT id FROM orders WHERE ("
)

func forEachSource(w wire, check func(string, reference.Source) error) error {
	var failures []error
	for _, key := range []string{"mysql", "postgresql"} {
		if err := check(key, w.env.Sources[key]); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", key, err))
		}
	}
	return errors.Join(failures...)
}

func readBatch(result reply, err error) (batch, error) {
	var page batch
	if err != nil {
		return page, err
	}
	err = result.decode(&page)
	return page, err
}

func readQuery(ctx context.Context, w wire, source reference.Source, sql string, params []any, size int) (batch, error) {
	return readBatch(w.query(ctx, reader, "", source, sql, params, size, 5))
}

func rows(page batch, expected ...[]any) error {
	if len(page.Entries) != len(expected) {
		return fmt.Errorf("expected %d rows, observed %d", len(expected), len(page.Entries))
	}
	for i, entry := range page.Entries {
		if !reflect.DeepEqual(entry, expected[i]) {
			return fmt.Errorf("fixture value or JSON type differs in row %d", i+1)
		}
	}
	return nil
}

func id(n string) []any { return []any{json.Number(n)} }

func expectPage(page batch, queryID, expectedID string, total int, hasNext bool) error {
	if page.QueryID == "" || (queryID != "" && page.QueryID != queryID) ||
		page.TotalCount != total || (page.NextURI != "") != hasNext {
		return fmt.Errorf("unexpected session identity, total count or continuation state")
	}
	return rows(page, id(expectedID))
}

func bindParameters(ctx context.Context, w wire) error {
	return forEachSource(w, func(key string, source reference.Source) error {
		placeholder := "?"
		if key == "postgresql" {
			placeholder = "$1"
		}
		sql := "SELECT id FROM orders WHERE status = " + placeholder + " ORDER BY id"
		page, err := readQuery(ctx, w, source, sql, []any{"shipped"}, 10)
		if err != nil {
			return err
		}
		if err := rows(page, id("1"), id("3")); err != nil {
			return fmt.Errorf("ordinary bind: %w", err)
		}
		page, err = readQuery(ctx, w, source, sql, []any{"shipped' OR 1=1 --"}, 10)
		if err != nil {
			return err
		}
		return rows(page)
	})
}

func normalizedValues(ctx context.Context, w wire) error {
	return forEachSource(w, func(_ string, source reference.Source) error {
		page, err := readQuery(ctx, w, source, "SELECT id, discount FROM order_items ORDER BY id", nil, 10)
		if err != nil {
			return err
		}
		if len(page.Columns) != 2 || page.Columns[0].Name != "id" || page.Columns[0].Type != "bigint" ||
			page.Columns[1].Name != "discount" || page.Columns[1].Type != "decimal" {
			return fmt.Errorf("unexpected normalized bigint/decimal schema")
		}
		return rows(page, []any{json.Number("1"), nil}, []any{json.Number("2"), "1.25"}, []any{json.Number("3"), "0.00"})
	})
}

func filterAggregateJoin(ctx context.Context, w wire) error {
	return forEachSource(w, func(_ string, source reference.Source) error {
		const sql = "SELECT o.id, COUNT(i.id) AS item_count FROM orders o JOIN order_items i ON o.id = i.order_id WHERE o.status = 'shipped' GROUP BY o.id ORDER BY o.id"
		page, err := readQuery(ctx, w, source, sql, nil, 10)
		if err != nil {
			return err
		}
		return rows(page, []any{json.Number("1"), json.Number("2")})
	})
}

func interleavedBatches(ctx context.Context, w wire) error {
	return forEachSource(w, func(_ string, source reference.Source) error {
		first, err := readQuery(ctx, w, source, orderedIDs, nil, 1)
		if err != nil {
			return err
		}
		second, err := readQuery(ctx, w, source, "SELECT id FROM orders WHERE id >= 2 ORDER BY id DESC", nil, 1)
		if err != nil {
			return err
		}
		if first.QueryID == second.QueryID {
			return fmt.Errorf("different queries returned the same session identity")
		}
		if err := expectPage(first, "", "1", 3, true); err != nil {
			return err
		}
		if err := expectPage(second, "", "3", 2, true); err != nil {
			return err
		}
		firstNext, err := readBatch(w.next(ctx, reader, first))
		if err != nil {
			return err
		}
		if err := expectPage(firstNext, first.QueryID, "2", 3, true); err != nil {
			return err
		}
		secondLast, err := readBatch(w.next(ctx, reader, second))
		if err != nil {
			return err
		}
		if err := expectPage(secondLast, second.QueryID, "2", 2, false); err != nil {
			return err
		}
		return finishThirdRow(ctx, w, firstNext, first.QueryID)
	})
}

func finishThirdRow(ctx context.Context, w wire, page batch, queryID string) error {
	current := page
	for {
		if current.QueryID == "" || (queryID != "" && current.QueryID != queryID) ||
			current.TotalCount != 3 || len(current.Entries) != 1 || len(current.Entries[0]) != 1 {
			return fmt.Errorf("unexpected query identity or page while finishing the fixture")
		}
		value, ok := current.Entries[0][0].(json.Number)
		if !ok {
			return fmt.Errorf("fixture row id is not numeric")
		}
		row, err := strconv.Atoi(value.String())
		if err != nil || row < 1 || row > 3 {
			return fmt.Errorf("fixture row id is outside the expected range")
		}
		if row == 3 {
			if current.NextURI != "" {
				return fmt.Errorf("terminal fixture row retained a continuation")
			}
			return nil
		}
		if current.NextURI == "" {
			return fmt.Errorf("query ended before the final fixture row")
		}
		next, err := readBatch(w.next(ctx, reader, current))
		if err != nil {
			return err
		}
		if err := expectPage(next, current.QueryID, strconv.Itoa(row+1), 3, row+1 < 3); err != nil {
			return err
		}
		current = next
	}
}

func rejectReplay(ctx context.Context, w wire) error {
	return forEachSource(w, func(_ string, source reference.Source) error {
		first, err := readQuery(ctx, w, source, orderedIDs, nil, 1)
		if err != nil {
			return err
		}
		next, err := readBatch(w.next(ctx, reader, first))
		if err != nil {
			return err
		}
		if next.Token == first.Token {
			return fmt.Errorf("continuation token did not rotate")
		}
		if err := expectPage(next, first.QueryID, "2", 3, true); err != nil {
			return err
		}
		replay, err := w.next(ctx, reader, first)
		if err != nil {
			return err
		}
		if err := replay.expectError(w.kind, http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", sessionError); err != nil {
			return err
		}
		return finishThirdRow(ctx, w, next, first.QueryID)
	})
}

func concurrentFetch(ctx context.Context, w wire) error {
	return forEachSource(w, func(_ string, source reference.Source) error {
		first, err := readQuery(ctx, w, source, orderedIDs, nil, 1)
		if err != nil {
			return err
		}
		type attempt struct {
			response reply
			err      error
		}
		fetch := func() (reply, error) { return w.next(ctx, reader, first) }
		fetches := []func() (reply, error){fetch, fetch}
		start := make(chan struct{})
		finished := make(chan attempt, len(fetches))
		for _, fetch := range fetches {
			go func() {
				<-start
				r, err := fetch()
				finished <- attempt{response: r, err: err}
			}()
		}
		close(start)
		a, b := <-finished, <-finished
		if err := errors.Join(a.err, b.err); err != nil {
			return err
		}
		if a.response.failed == b.response.failed {
			return fmt.Errorf("expected one successful fetch and one rejected fetch")
		}
		if a.response.failed {
			a, b = b, a
		}
		if err := b.response.expectError(w.kind, http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", sessionError); err != nil {
			return err
		}
		next, err := readBatch(a.response, nil)
		if err != nil {
			return err
		}
		if err := expectPage(next, first.QueryID, "2", 3, true); err != nil {
			return err
		}
		return finishThirdRow(ctx, w, next, first.QueryID)
	})
}

func openAtCapacity(ctx context.Context, w wire, source reference.Source) ([]batch, error) {
	var streams []batch
	for range TenantCapacity {
		page, err := readQuery(ctx, w, source, orderedIDs, nil, 1)
		if err != nil {
			return nil, err
		}
		if err := expectPage(page, "", "1", 3, true); err != nil {
			return nil, err
		}
		streams = append(streams, page)
	}
	excess, err := w.query(ctx, reader, "", source, orderedIDs, nil, 1, 5)
	if err != nil {
		return nil, err
	}
	if err := excess.expectError(w.kind, http.StatusBadGateway, "QUERY_ENGINE_ERROR", capacityError); err != nil {
		return nil, err
	}
	return streams, nil
}

func verifyReleasedCapacity(ctx context.Context, w wire, source reference.Source) error {
	streams, err := openAtCapacity(ctx, w, source)
	if err != nil {
		return fmt.Errorf("could not reclaim all configured capacity: %w", err)
	}
	for _, stream := range streams {
		next, err := readBatch(w.next(ctx, reader, stream))
		if err != nil {
			return err
		}
		if err := expectPage(next, stream.QueryID, "2", 3, true); err != nil {
			return err
		}
		if err := finishThirdRow(ctx, w, next, stream.QueryID); err != nil {
			return err
		}
	}
	return nil
}

func expireSessions(ctx context.Context, w wire) error {
	source := w.env.Sources["postgresql"]
	before, err := logicalQueryMetricSnapshot(ctx, w)
	if err != nil {
		return err
	}
	expiring, err := openAtCapacity(ctx, w, source)
	if err != nil {
		return err
	}
	// A session may expire just after a cleanup tick. Allow one full TTL
	// for expiry and another for the scheduled sweep.
	timer := time.NewTimer(time.Duration(2*SessionTTLSeconds+1) * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return ctx.Err()
	}
	// The query session lifetime is intentionally shorter than this wait.
	// The lifecycle tracker should emit one expiry outcome per retained result.
	afterExpiry, err := logicalQueryMetricSnapshot(ctx, w)
	if err != nil {
		return err
	}
	if err := assertLogicalMetricDelta(before, afterExpiry, "session_expired", len(expiring), len(expiring), len(expiring)); err != nil {
		return err
	}
	// Refresh the independent MCP transport session before issuing new work.
	if err := w.releaseMCPActor(ctx, reader); err != nil {
		return fmt.Errorf("renew acceptance session after query expiry wait: %w", err)
	}
	// First verify the sweeper can reclaim capacity without an expired
	// continuation request doing the cleanup on its behalf.
	if err := verifyReleasedCapacity(ctx, w, source); err != nil {
		return err
	}
	for _, stream := range expiring {
		result, err := w.next(ctx, reader, stream)
		if err != nil {
			return err
		}
		if err := result.expectError(w.kind, http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", sessionError); err != nil {
			return err
		}
	}
	return nil
}

func deniedQuery(ctx context.Context, w wire, actor string, source reference.Source, sql string) error {
	result, err := w.query(ctx, actor, "", source, sql, nil, 10, 5)
	if err != nil {
		return err
	}
	return result.expectError(w.kind, http.StatusForbidden, "TABLE_ACCESS_DENIED", tableDenied)
}

func deniedColumns(ctx context.Context, w wire) error {
	statements := []struct{ position, sql string }{
		{"projection", "SELECT customer FROM orders"},
		{"filter", "SELECT id FROM orders WHERE customer = 'Ada Lovelace'"},
		{"join", "SELECT o.id FROM orders o JOIN order_items i ON o.customer = i.item"},
		{"order", "SELECT id FROM orders ORDER BY customer"},
	}
	return forEachSource(w, func(_ string, source reference.Source) error {
		for _, statement := range statements {
			if err := deniedQuery(ctx, w, reader, source, statement.sql); err != nil {
				return fmt.Errorf("%s: %w", statement.position, err)
			}
		}
		return nil
	})
}

func unsafeStatements(ctx context.Context, w wire) error {
	return forEachSource(w, func(key string, source reference.Source) error {
		danger := "SELECT SLEEP(10)"
		if key == "postgresql" {
			danger = "SELECT pg_sleep(10)"
		}
		for _, statement := range []struct{ sql, message string }{
			{"DELETE FROM orders WHERE id = 1", readonlyError},
			{danger, invalidQuery},
		} {
			result, err := w.query(ctx, reader, "", source, statement.sql, nil, 10, 1)
			if err != nil {
				return err
			}
			if err := result.expectError(w.kind, http.StatusBadRequest, "INVALID_QUERY", statement.message); err != nil {
				return err
			}
		}
		return healthyRows(ctx, w, source)
	})
}

func malformedSQL(ctx context.Context, w wire) error {
	return forEachSource(w, func(_ string, source reference.Source) error {
		r, err := w.query(ctx, reader, "", source, malformed, nil, 10, 5)
		if err != nil {
			return err
		}
		return r.expectError(w.kind, http.StatusBadRequest, "INVALID_QUERY", invalidQuery)
	})
}

func healthyRows(ctx context.Context, w wire, source reference.Source) error {
	page, err := readQuery(ctx, w, source, orderedIDs, nil, 10)
	if err != nil {
		return err
	}
	return rows(page, id("1"), id("2"), id("3"))
}

func sourceOutage(ctx context.Context, w wire) error {
	if err := w.env.SetSourceAvailable(ctx, "mysql", false); err != nil {
		return fmt.Errorf("stop synthetic MySQL")
	}
	mysql := w.env.Sources["mysql"]
	check := func() error {
		r, err := w.query(ctx, reader, "", mysql, orderedIDs, nil, 10, 5)
		if err != nil {
			return err
		}
		// A stopped container can refuse a connection immediately or leave
		// the dial waiting until the gateway's configured request deadline.
		status, code, message := http.StatusBadGateway, "QUERY_ENGINE_ERROR", sourceFailure
		if r.message == queryTimeout {
			status, code, message = http.StatusGatewayTimeout, "QUERY_TIMEOUT", queryTimeout
		}
		if err := r.expectError(w.kind, status, code, message); err != nil {
			return fmt.Errorf("unavailable MySQL: %w", err)
		}
		if err := healthyRows(ctx, w, w.env.Sources["postgresql"]); err != nil {
			return fmt.Errorf("unaffected PostgreSQL: %w", err)
		}
		denied, err := w.query(ctx, outsider, "", mysql, orderedIDs, nil, 10, 5)
		if err != nil {
			return err
		}
		return denied.expectError(w.kind, http.StatusForbidden, "DATASOURCE_ACCESS_DENIED", sourceDenied)
	}()
	restore, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	if err := w.env.SetSourceAvailable(restore, "mysql", true); err != nil {
		return errors.Join(check, fmt.Errorf("restore synthetic MySQL"))
	}
	// Stopping and restoring the fixture can outlive the MCP transport TTL.
	// Start the recovery query on a fresh transport session.
	if err := w.releaseMCPActor(restore, reader); err != nil {
		return errors.Join(check, fmt.Errorf("renew acceptance session after MySQL recovery: %w", err))
	}
	return errors.Join(check, healthyRows(restore, w, mysql))
}

func queryDeadline(ctx context.Context, w wire) error {
	source := w.env.Sources["postgresql"]
	before, err := logicalQueryMetricSnapshot(ctx, w)
	if err != nil {
		return err
	}
	err = w.env.WithPostgresOrdersLock(ctx, func(locked context.Context) error {
		for range TenantCapacity {
			started := time.Now()
			r, err := w.query(locked, reader, "", source, orderedIDs, nil, 1, 1)
			if err != nil {
				return err
			}
			if err := r.expectError(w.kind, http.StatusGatewayTimeout, "QUERY_TIMEOUT", queryTimeout); err != nil {
				return err
			}
			if elapsed := time.Since(started); elapsed < 850*time.Millisecond || elapsed > 8*time.Second {
				return fmt.Errorf("blocked query missed deadline window (%dms)", elapsed.Milliseconds())
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("PostgreSQL lock/timeout check: %w", err)
	}
	afterTimeout, err := logicalQueryMetricSnapshot(ctx, w)
	if err != nil {
		return err
	}
	if err := assertLogicalMetricDelta(before, afterTimeout, "timeout", TenantCapacity, 0, 0); err != nil {
		return err
	}
	return verifyReleasedCapacity(ctx, w, source)
}

func matchesColumns(actual []column, names ...string) bool {
	var observed []string
	for _, col := range actual {
		if col.Type == "" || col.Nullable {
			return false
		}
		observed = append(observed, col.Name)
	}
	slices.Sort(observed)
	return slices.Equal(observed, names)
}

func containsAny(raw string, values ...string) bool {
	for _, value := range values {
		if value != "" && strings.Contains(raw, value) {
			return true
		}
	}
	return false
}
