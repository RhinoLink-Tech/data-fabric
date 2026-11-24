package traditional

import (
	"strings"
	"testing"
)

func TestMySQLAndPostgreSQLTerminatedPagination(t *testing.T) {
	drivers := []struct {
		name   string
		driver interface {
			ApplyBatchPagination(string, int64, int64) (string, error)
		}
	}{
		{"mysql", &MySQLDriver{}}, {"postgresql", &PostgreSQLDriver{}},
	}
	cases := []struct {
		name, sql, want string
		offset          int64
	}{
		{"plain", "SELECT id FROM orders", "SELECT id FROM orders LIMIT 2", 0},
		{"terminator", "SELECT id FROM orders;", "SELECT id FROM orders LIMIT 2", 0},
		{"continuation", "SELECT id FROM orders; -- done\n", "SELECT id FROM orders LIMIT 2 OFFSET 2", 2},
		{"existing page", "SELECT id FROM orders LIMIT\n3;", "SELECT * FROM (SELECT id FROM orders LIMIT\n3) AS batch_query LIMIT 2 OFFSET 2", 2},
		{"literal semicolon", "SELECT 'a;b' AS label;", "SELECT 'a;b' AS label LIMIT 2", 0},
		{"literal keyword", "SELECT ' LIMIT ; ' AS label;", "SELECT ' LIMIT ; ' AS label LIMIT 2", 0},
		{"block comment", "SELECT id /* ; LIMIT */ FROM orders;", "SELECT id /* ; LIMIT */ FROM orders LIMIT 2", 0},
		{"trailing line comment", "SELECT id FROM orders -- end;", "SELECT id FROM orders -- end;\n LIMIT 2", 0},
		{"line comment before terminator", "SELECT id FROM orders -- end\n;", "SELECT id FROM orders -- end\n LIMIT 2", 0},
		{"wrapped line comment", "SELECT id FROM orders LIMIT 3 -- end;", "SELECT * FROM (SELECT id FROM orders LIMIT 3 -- end;\n) AS batch_query LIMIT 2 OFFSET 2", 2},
	}
	for _, d := range drivers {
		for _, tc := range cases {
			t.Run(d.name+"/"+tc.name, func(t *testing.T) {
				got, err := d.driver.ApplyBatchPagination(tc.sql, 2, tc.offset)
				if err != nil || got != tc.want {
					t.Fatalf("got %q, error %v, want %q", got, err, tc.want)
				}
			})
		}
	}
}

func TestPaginationDialectQuotedTextAndComments(t *testing.T) {
	cases := []struct {
		name, sql, want string
		mysql           bool
	}{
		{"mysql backtick", "SELECT \x60a;b\x60 FROM orders;", "SELECT \x60a;b\x60 FROM orders LIMIT 2", true},
		{"mysql hash comment", "SELECT id FROM orders # tail;", "SELECT id FROM orders # tail;\n LIMIT 2", true},
		{"postgres dollar quote", "SELECT $tag$a;b LIMIT$tag$ AS label;", "SELECT $tag$a;b LIMIT$tag$ AS label LIMIT 2", false},
		{"postgres nested comment", "SELECT 1 /* outer /* ; */ done */;", "SELECT 1 /* outer /* ; */ done */ LIMIT 2", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := paginateLimitOffset(tc.sql, 2, 0, tc.mysql)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, error %v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestPaginationRejectsMultipleAndAmbiguousStatements(t *testing.T) {
	cases := []struct {
		sql   string
		mysql bool
	}{
		{"SELECT 1; SELECT 2", true}, {"SELECT 1; SELECT 2", false},
		{"SELECT 1;;", true}, {"SELECT 1; -- comment\n DELETE FROM orders", false},
		{"SELECT 'unterminated", true}, {"SELECT 1 /* unterminated", false},
		{"SELECT $tag$unterminated", false}, {"SELECT 1; /*! SELECT 2 */", true},
		{"SELECT 1 /*M! ; DELETE FROM orders */", true},
		{"SELECT 'ambiguous\\'quote'; SELECT 2", true},
		{"SELECT 'ambiguous\\'quote'; SELECT 2", false},
		{"; -- empty", true},
	}
	for _, tc := range cases {
		got, err := paginateLimitOffset(tc.sql, 2, 0, tc.mysql)
		if err == nil {
			t.Fatalf("accepted unsafe or ambiguous input %q: %q", tc.sql, got)
		}
		if strings.Contains(err.Error(), tc.sql) {
			t.Fatal("error exposed query text")
		}
	}
}
