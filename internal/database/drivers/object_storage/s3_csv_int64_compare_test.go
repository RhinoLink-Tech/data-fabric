package object_storage

import (
	"context"
	"fmt"
	"math"
	"testing"

	"vitess.io/vitess/go/vt/sqlparser"
)

// Adjacent int64 values above 2^53 are not distinguishable as float64, so
// the comparator must keep integer operands exact.
func TestCompareCSVValuesKeepsAdjacentInt64Exact(t *testing.T) {
	const big int64 = 9007199254740993 // 2^53 + 1
	if got := compareCSVValues(big, big-1); got != 1 {
		t.Fatalf("compareCSVValues(%d, %d) = %d, want 1", big, big-1, got)
	}
	if got := compareCSVValues(big-1, big); got != -1 {
		t.Fatalf("compareCSVValues(%d, %d) = %d, want -1", big-1, big, got)
	}
	if got := compareCSVValues(big, big); got != 0 {
		t.Fatalf("compareCSVValues(%d, %d) = %d, want 0", big, big, got)
	}
}

func TestCompareCSVValuesMixedNumericOperandsAreExact(t *testing.T) {
	tests := []struct {
		name        string
		left, right interface{}
		want        int
	}{
		{"int64 above float64 neighbour", int64(9007199254740993), float64(9007199254740992), 1},
		{"int64 below float64 neighbour", int64(9007199254740993), float64(9007199254740994), -1},
		{"int64 vs fractional float", int64(1), 1.5, -1},
		{"negative int64 vs fractional float", int64(-1), -1.5, 1},
		{"int64 vs +Inf", int64(1), math.Inf(1), -1},
		{"uint64 max vs int64 max", uint64(math.MaxUint64), int64(math.MaxInt64), 1},
		{"uint64 above int64 max", uint64(1) << 63, int64(math.MaxInt64), 1},
		{"integer string vs int64", "9007199254740993", int64(9007199254740992), 1},
		{"int8 min vs int16", int8(math.MinInt8), int16(-128), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareCSVValues(tt.left, tt.right); got != tt.want {
				t.Fatalf("compareCSVValues(%v, %v) = %d, want %d", tt.left, tt.right, got, tt.want)
			}
		})
	}
}

// Drives the SQL literal, WHERE/IN and ORDER BY path with exact int64 values.
func TestS3CSVSQLAdjacentInt64FilterAndOrder(t *testing.T) {
	const high int64 = 9007199254740993
	const low int64 = 9007199254740992
	base := func() []map[string]interface{} {
		return []map[string]interface{}{{"id": high}, {"id": low}}
	}
	ids := func(rows []map[string]interface{}) []interface{} {
		out := make([]interface{}, 0, len(rows))
		for _, row := range rows {
			out = append(out, row["id"])
		}
		return out
	}
	run := func(t *testing.T, query string) []map[string]interface{} {
		t.Helper()
		stmt, err := sqlparser.NewTestParser().Parse(query)
		if err != nil {
			t.Fatal(err)
		}
		sel := stmt.(*sqlparser.Select)
		idx := 0
		plan, err := buildS3CSVPlan(sel, nil, &idx)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := applyCSVConditions(context.Background(), base(), plan.conditions)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = applyCSVOrder(context.Background(), rows, plan.orderBy)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	tests := []struct {
		name  string
		query string
		want  []interface{}
	}{
		{"equality literal", "SELECT id FROM records WHERE id = 9007199254740993", []interface{}{high}},
		{"inequality literal", "SELECT id FROM records WHERE id != 9007199254740993", []interface{}{low}},
		{"greater than literal", "SELECT id FROM records WHERE id > 9007199254740992", []interface{}{high}},
		{"in list", "SELECT id FROM records WHERE id IN (9007199254740992)", []interface{}{low}},
		{"not in list", "SELECT id FROM records WHERE id NOT IN (9007199254740993)", []interface{}{low}},
		{"order ascending", "SELECT id FROM records ORDER BY id", []interface{}{low, high}},
		{"order descending", "SELECT id FROM records ORDER BY id DESC", []interface{}{high, low}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ids(run(t, tt.query))
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Fatalf("%s: got %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}
