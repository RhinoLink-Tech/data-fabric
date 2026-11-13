package object_storage

import (
	"context"
	"testing"

	"vitess.io/vitess/go/vt/sqlparser"
)

// IN/NOT IN must follow SQL's three-valued logic: a NULL in the list makes a
// non-matching comparison UNKNOWN, so WHERE excludes the row (#527).
func TestMatchesCSVConditionInNotNullSemantics(t *testing.T) {
	in := func(values ...interface{}) s3CSVCondition {
		return s3CSVCondition{column: "id", op: sqlparser.InOp, values: values}
	}
	notIn := func(values ...interface{}) s3CSVCondition {
		return s3CSVCondition{column: "id", op: sqlparser.NotInOp, values: values}
	}
	tests := []struct {
		name      string
		actual    interface{}
		condition s3CSVCondition
		want      bool
	}{
		{"in matches", int64(2), in(int64(1), int64(2)), true},
		{"in misses", int64(3), in(int64(1), int64(2)), false},
		{"in with null still matches", int64(1), in(int64(1), nil), true},
		{"in with null misses", int64(2), in(int64(1), nil), false},
		{"not in misses on match", int64(1), notIn(int64(1), int64(2)), false},
		{"not in returns unmatched value", int64(3), notIn(int64(1), int64(2)), true},
		{"not in with null excludes unmatched value", int64(2), notIn(int64(1), nil), false},
		{"not in with null excludes null", nil, notIn(int64(1), nil), false},
		{"not in null list only excludes everything", int64(2), notIn(nil), false},
		{"in with null and null value", nil, in(int64(1), nil), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesCSVCondition(tt.actual, tt.condition); got != tt.want {
				t.Errorf("matchesCSVCondition(%v) = %v, want %v", tt.actual, got, tt.want)
			}
		})
	}
}

// The filter stage drops the UNKNOWN rows the predicate excludes.
func TestApplyCSVConditionsExcludesUnknownNotInRows(t *testing.T) {
	rows := []map[string]interface{}{
		{"id": int64(1)},
		{"id": int64(2)},
		{"id": nil},
	}
	filtered, err := applyCSVConditions(context.Background(), rows, []s3CSVCondition{
		{column: "id", op: sqlparser.NotInOp, values: []interface{}{int64(1), nil}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 0 {
		t.Fatalf("filtered = %+v, want no rows", filtered)
	}

	filtered, err = applyCSVConditions(context.Background(), rows, []s3CSVCondition{
		{column: "id", op: sqlparser.NotInOp, values: []interface{}{int64(1)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0]["id"] != int64(2) {
		t.Fatalf("filtered = %+v, want only id=2", filtered)
	}
}
