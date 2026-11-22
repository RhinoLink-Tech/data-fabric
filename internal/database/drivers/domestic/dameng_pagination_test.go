package domestic

import (
	"errors"
	"testing"
)

func TestDaMengApplyBatchPaginationStatementTerminator(t *testing.T) {
	adapter := &DaMengDBAdapter{}
	cases := []struct {
		name   string
		sql    string
		offset int64
		want   string
	}{
		{"no terminator first page", "SELECT id FROM orders", 0,
			"SELECT id FROM orders FETCH FIRST 100 ROWS ONLY"},
		{"terminator first page", "SELECT id FROM orders;", 0,
			"SELECT id FROM orders FETCH FIRST 100 ROWS ONLY"},
		{"terminator continuation page", "  SELECT id FROM orders ;\n", 200,
			"SELECT id FROM orders OFFSET 200 ROWS FETCH FIRST 100 ROWS ONLY"},
		{"terminator then comments", "SELECT id FROM orders; -- done\n/* end */", 0,
			"SELECT id FROM orders FETCH FIRST 100 ROWS ONLY"},
		{"existing pagination with terminator", "SELECT id FROM orders OFFSET 5 ROWS FETCH FIRST 10 ROWS ONLY;", 0,
			"SELECT * FROM (SELECT id FROM orders OFFSET 5 ROWS FETCH FIRST 10 ROWS ONLY) AS batch_query OFFSET 0 ROWS FETCH FIRST 100 ROWS ONLY"},
		{"semicolon in literal", "SELECT id FROM orders WHERE note = 'a;b' AND tag = 'it''s;'", 0,
			"SELECT id FROM orders WHERE note = 'a;b' AND tag = 'it''s;' FETCH FIRST 100 ROWS ONLY"},
		{"semicolon in quoted identifier", `SELECT "a;b" FROM orders;`, 0,
			`SELECT "a;b" FROM orders FETCH FIRST 100 ROWS ONLY`},
		{"semicolon in comments", "SELECT id /* x; y */ FROM orders -- z;\nWHERE id > 1;", 0,
			"SELECT id /* x; y */ FROM orders -- z;\nWHERE id > 1 FETCH FIRST 100 ROWS ONLY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := adapter.ApplyBatchPagination(tc.sql, 100, tc.offset)
			if err != nil {
				t.Fatalf("ApplyBatchPagination(%q) error = %v", tc.sql, err)
			}
			if got != tc.want {
				t.Fatalf("ApplyBatchPagination(%q)\n got  %q\n want %q", tc.sql, got, tc.want)
			}
		})
	}
}

func TestDaMengApplyBatchPaginationRefusesMultipleStatements(t *testing.T) {
	adapter := &DaMengDBAdapter{}
	for _, sql := range []string{
		"SELECT id FROM orders; DELETE FROM orders",
		"SELECT id FROM orders;;",
		"SELECT 1; SELECT 2;",
	} {
		got, err := adapter.ApplyBatchPagination(sql, 100, 0)
		if !errors.Is(err, errMultipleStatements) {
			t.Fatalf("ApplyBatchPagination(%q) = %q, %v; want errMultipleStatements", sql, got, err)
		}
	}
}
