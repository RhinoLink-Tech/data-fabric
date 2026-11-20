package service

import (
	"errors"
	"strings"
	"testing"
)

func TestNoCountTrinoUnsupportedWindowHasActionableError(t *testing.T) {
	_, err := noCountTrinoPagination("SELECT id FROM orders LIMIT ?", 2, 0)
	if !errors.Is(err, ErrNoCountUnsupported) || !strings.Contains(SafeQueryErrorMessage(err), "request streaming") {
		t.Fatalf("unsupported window error=%v message=%q", err, SafeQueryErrorMessage(err))
	}
}

func TestNoCountTrinoPaginationRespectsUserLimitAndOrder(t *testing.T) {
	for _, tc := range []struct {
		name, query  string
		size, offset int64
		want         string
		valid        bool
	}{
		{"plain", "SELECT id FROM orders ORDER BY id", 3, 2, "SELECT id FROM orders ORDER BY id\nOFFSET 2 LIMIT 3", true},
		{"literal limit first", "SELECT id FROM orders ORDER BY id LIMIT 2", 3, 0, "SELECT id FROM orders ORDER BY id\nOFFSET 0 LIMIT 2", true},
		{"literal limit next", "SELECT id FROM orders ORDER BY id OFFSET 3 LIMIT 5", 3, 4, "SELECT id FROM orders ORDER BY id\nOFFSET 7 LIMIT 1", true},
		{"offset only", "SELECT id FROM orders ORDER BY id OFFSET 3 ROWS", 3, 4, "SELECT id FROM orders ORDER BY id\nOFFSET 7 LIMIT 3", true},
		{"limit all", "SELECT id FROM orders ORDER BY id OFFSET 3 LIMIT ALL", 3, 4, "SELECT id FROM orders ORDER BY id\nOFFSET 7 LIMIT 3", true},
		{"limit exhausted", "SELECT id FROM orders ORDER BY id LIMIT 5", 3, 5, "SELECT id FROM orders ORDER BY id\nOFFSET 5 LIMIT 0", true},
		{"quoted keyword", "SELECT 'LIMIT' AS value FROM orders ORDER BY id", 3, 0, "SELECT 'LIMIT' AS value FROM orders ORDER BY id\nOFFSET 0 LIMIT 3", true},
		{"nested limit", "SELECT id FROM (SELECT id FROM orders LIMIT 4) AS x ORDER BY id", 3, 1, "SELECT id FROM (SELECT id FROM orders LIMIT 4) AS x ORDER BY id\nOFFSET 1 LIMIT 3", true},
		{"semicolon", "SELECT id FROM orders ORDER BY id LIMIT 5;", 3, 0, "SELECT id FROM orders ORDER BY id\nOFFSET 0 LIMIT 3", true},
		{"parameterized limit", "SELECT id FROM orders LIMIT ?", 3, 0, "", false},
		{"trailing comment", "SELECT id FROM orders -- LIMIT 9", 3, 0, "SELECT id FROM orders -- LIMIT 9\nOFFSET 0 LIMIT 3", true},
		{"limit before offset", "SELECT id FROM orders LIMIT 5 OFFSET 2", 3, 0, "", false},
		{"fetch syntax", "SELECT id FROM orders FETCH FIRST 2 ROWS ONLY", 3, 0, "", false},
		{"overflow", "SELECT id FROM orders OFFSET 9223372036854775807 LIMIT 10", 3, 1, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := noCountTrinoPagination(tc.query, tc.size, tc.offset)
			if tc.valid {
				if err != nil || got != tc.want {
					t.Fatalf("got=%q err=%v want=%q", got, err, tc.want)
				}
			} else if !errors.Is(err, ErrNoCountUnsupported) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
