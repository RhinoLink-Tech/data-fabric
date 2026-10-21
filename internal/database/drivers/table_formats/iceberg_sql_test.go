package table_formats

import (
	"database/sql/driver"
	"strings"
	"testing"

	"vitess.io/vitess/go/vt/sqlparser"
)

func TestParseIcebergSQLBoundedProjectionAndFilter(t *testing.T) {
	args := []driver.NamedValue{
		{Ordinal: 1, Value: true},
		{Ordinal: 2, Value: int64(15)},
		{Ordinal: 3, Value: int64(20)},
		{Ordinal: 4, Value: int64(5)},
	}
	plan, err := parseIcebergSQL(
		"SELECT o.id AS order_id, amount FROM analytics.orders o WHERE active = ? AND amount >= ? LIMIT ? OFFSET ?",
		args, "default", 1000, false,
	)
	if err != nil {
		t.Fatalf("parse bounded query: %v", err)
	}
	if got := strings.Join(plan.namespace, "."); got != "analytics" {
		t.Fatalf("namespace = %q, want analytics", got)
	}
	if plan.table != "orders" || plan.allColumns {
		t.Fatalf("unexpected table/projection mode: %#v", plan)
	}
	if len(plan.projections) != 2 || plan.projections[0].source != "id" || plan.projections[0].output != "order_id" {
		t.Fatalf("unexpected projections: %#v", plan.projections)
	}
	if plan.filter == nil || plan.limit != 20 || plan.offset != 5 || !plan.hasLimit {
		t.Fatalf("unexpected filter or page bounds: %#v", plan)
	}
	if !strings.Contains(plan.filter.String(), "And(") {
		t.Fatalf("filter = %s, want AND expression", plan.filter)
	}
}

func TestParseIcebergSQLCountQueries(t *testing.T) {
	t.Run("direct count is unbounded and ignores aggregate limit", func(t *testing.T) {
		plan, err := parseIcebergSQL("SELECT COUNT(*) FROM sales.orders WHERE active = true LIMIT 1", nil, "", 1000, false)
		if err != nil {
			t.Fatalf("parse direct count: %v", err)
		}
		if !plan.count || plan.countColumn != "count(*)" || plan.hasLimit {
			t.Fatalf("unexpected count plan: %#v", plan)
		}
	})

	t.Run("derived count preserves inner limit and offset", func(t *testing.T) {
		query := "SELECT COUNT(*) AS total FROM (SELECT id FROM sales.orders WHERE active = true LIMIT 10 OFFSET 2) AS t"
		plan, err := parseIcebergSQL(query, nil, "", 1000, false)
		if err != nil {
			t.Fatalf("parse derived count: %v", err)
		}
		if !plan.count || plan.countColumn != "total" || !plan.hasLimit || plan.limit != 10 || plan.offset != 2 {
			t.Fatalf("unexpected derived count plan: %#v", plan)
		}
	})
}

func TestParseIcebergSQLRejectsUnboundedAndUnsupportedQueries(t *testing.T) {
	for _, query := range []string{
		"SELECT id FROM sales.orders",
		"SELECT id FROM sales.orders ORDER BY id LIMIT 5",
		"SELECT id FROM sales.orders JOIN sales.customers ON orders.id = customers.id LIMIT 5",
		"DELETE FROM sales.orders",
	} {
		t.Run(query, func(t *testing.T) {
			if _, err := parseIcebergSQL(query, nil, "", 1000, false); err == nil {
				t.Fatalf("expected query to be rejected")
			}
		})
	}
}

func TestIcebergApplyBatchPaginationReplacesExistingLimit(t *testing.T) {
	driver := &IcebergDriver{}
	query, err := driver.ApplyBatchPagination("SELECT id FROM sales.orders LIMIT 10", 25, 50)
	if err != nil {
		t.Fatalf("apply pagination: %v", err)
	}
	stmt, err := sqlparser.NewTestParser().Parse(query)
	if err != nil {
		t.Fatalf("parse paginated query %q: %v", query, err)
	}
	selectStmt, ok := stmt.(*sqlparser.Select)
	if !ok || selectStmt.Limit == nil || sqlparser.String(selectStmt.Limit.Rowcount) != "25" || sqlparser.String(selectStmt.Limit.Offset) != "50" {
		t.Fatalf("unexpected paginated query: %s", query)
	}
}

func TestIcebergScanBoundRejectsOffsetLimitOverflow(t *testing.T) {
	_, err := parseIcebergSQL(
		"SELECT COUNT(*) FROM (SELECT id FROM sales.orders LIMIT ? OFFSET ?) AS page",
		[]driver.NamedValue{{Ordinal: 1, Value: int64(9223372036854775807)}, {Ordinal: 2, Value: int64(1)}},
		"", icebergDefaultMaxRows, true,
	)
	if err == nil {
		t.Fatal("expected an oversized inner scan bound to be rejected")
	}
}
