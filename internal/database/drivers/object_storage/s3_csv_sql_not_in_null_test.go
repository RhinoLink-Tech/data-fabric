package object_storage

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"vitess.io/vitess/go/vt/sqlparser"
)

// notInSemanticsCases pins SQL three-valued logic for IN/NOT IN: a NULL in
// the list (or a NULL source value) makes the comparison UNKNOWN, and WHERE
// keeps only TRUE rows — UNKNOWN must never let a non-matching row through.
var notInSemanticsCases = []struct {
	name   string
	op     sqlparser.ComparisonExprOperator
	actual interface{}
	values []interface{}
	want   bool
}{
	// List without NULL.
	{name: "NOT IN match", op: sqlparser.NotInOp, actual: int64(1), values: []interface{}{int64(1), int64(2)}, want: false},
	{name: "NOT IN nonmatch", op: sqlparser.NotInOp, actual: int64(3), values: []interface{}{int64(1), int64(2)}, want: true},
	{name: "NOT IN null source", op: sqlparser.NotInOp, actual: nil, values: []interface{}{int64(1), int64(2)}, want: false},
	{name: "IN match", op: sqlparser.InOp, actual: int64(1), values: []interface{}{int64(1), int64(2)}, want: true},
	{name: "IN nonmatch", op: sqlparser.InOp, actual: int64(3), values: []interface{}{int64(1), int64(2)}, want: false},
	{name: "IN null source", op: sqlparser.InOp, actual: nil, values: []interface{}{int64(1), int64(2)}, want: false},
	// List with one NULL.
	{name: "NOT IN match with null in list", op: sqlparser.NotInOp, actual: int64(1), values: []interface{}{int64(1), nil}, want: false},
	{name: "NOT IN nonmatch with null in list", op: sqlparser.NotInOp, actual: int64(3), values: []interface{}{int64(1), nil}, want: false},
	{name: "NOT IN null source with null in list", op: sqlparser.NotInOp, actual: nil, values: []interface{}{int64(1), nil}, want: false},
	{name: "IN match with null in list", op: sqlparser.InOp, actual: int64(1), values: []interface{}{int64(1), nil}, want: true},
	{name: "IN nonmatch with null in list", op: sqlparser.InOp, actual: int64(3), values: []interface{}{int64(1), nil}, want: false},
	{name: "IN null source with null in list", op: sqlparser.InOp, actual: nil, values: []interface{}{int64(1), nil}, want: false},
	// List containing only NULL.
	{name: "NOT IN only null", op: sqlparser.NotInOp, actual: int64(3), values: []interface{}{nil}, want: false},
	{name: "NOT IN only null null source", op: sqlparser.NotInOp, actual: nil, values: []interface{}{nil}, want: false},
	{name: "IN only null", op: sqlparser.InOp, actual: int64(3), values: []interface{}{nil}, want: false},
	{name: "IN only null null source", op: sqlparser.InOp, actual: nil, values: []interface{}{nil}, want: false},
}

func TestMatchesCSVConditionInNotInNullSemantics(t *testing.T) {
	for _, test := range notInSemanticsCases {
		t.Run(test.name, func(t *testing.T) {
			condition := s3CSVCondition{column: "id", op: test.op, values: test.values}
			if got := matchesCSVCondition(test.actual, condition); got != test.want {
				t.Fatalf("matchesCSVCondition(%v, %v %v) = %v, want %v", test.actual, test.op, test.values, got, test.want)
			}
		})
	}
}

// assertNotInEndToEnd runs the shared acceptance table over an opened adapter
// so the object SQL adapter and the CSV path are exercised with identical
// queries: literal NULL lists, a bound nil parameter, NULL source rows, and
// an AND-combined predicate.
func assertNotInEndToEnd(t *testing.T, db *sql.DB) {
	t.Helper()
	tests := []struct {
		name  string
		query string
		args  []interface{}
		want  []interface{}
	}{
		{name: "literal null list excludes nonmatches", query: "SELECT id FROM records WHERE id NOT IN (1, NULL)", want: []interface{}{}},
		{name: "plain list keeps nonmatches", query: "SELECT id FROM records WHERE id NOT IN (1)", want: []interface{}{int64(2)}},
		{name: "only null list excludes everything", query: "SELECT id FROM records WHERE id NOT IN (NULL)", want: []interface{}{}},
		{name: "bound nil parameter excludes nonmatches", query: "SELECT id FROM records WHERE id NOT IN (?, ?)", args: []interface{}{int64(1), nil}, want: []interface{}{}},
		{name: "and predicate with null list", query: "SELECT id FROM records WHERE id NOT IN (1, NULL) AND name = 'Bob'", want: []interface{}{}},
		{name: "and predicate with plain list", query: "SELECT id FROM records WHERE id NOT IN (1) AND name = 'Bob'", want: []interface{}{int64(2)}},
		{name: "in still matches before null", query: "SELECT id FROM records WHERE id IN (1, NULL)", want: []interface{}{int64(1)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rows, err := db.QueryContext(context.Background(), test.query, test.args...)
			if err != nil {
				t.Fatalf("query %q: %v", test.query, err)
			}
			got := collectQueriedIDs(t, rows)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("query %q returned %v, want %v", test.query, got, test.want)
			}
		})
	}
}

func collectQueriedIDs(t *testing.T, rows *sql.Rows) []interface{} {
	t.Helper()
	defer rows.Close()
	got := []interface{}{}
	for rows.Next() {
		var id interface{}
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan id: %v", err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate rows: %v", err)
	}
	return got
}

// TestObjectSQLAdapterNotInNullSemantics runs the real shared object SQL
// adapter over synthetic in-memory rows 1, 2, NULL — no external source.
func TestObjectSQLAdapterNotInNullSemantics(t *testing.T) {
	schema := &objectSQLSchema{Columns: []objectSQLColumn{
		{Name: "id", StandardType: "bigint", Nullable: true},
		{Name: "name", StandardType: "varchar", Nullable: true},
	}}
	rows := []map[string]interface{}{
		{"id": int64(1), "name": "Alice"},
		{"id": int64(2), "name": "Bob"},
		{"id": nil, "name": "Carol"},
	}
	db := newObjectSQLDB(&objectSQLAdapter{
		driverName: "test-object",
		objectKey:  "records",
		tableName:  "records",
		getSchema: func(context.Context, string) (*objectSQLSchema, error) {
			return schema, nil
		},
		readRows: func(context.Context, string) (*objectSQLSchema, []map[string]interface{}, error) {
			return schema, rows, nil
		},
	})
	defer db.Close()
	assertNotInEndToEnd(t, db)
}

// TestCSVSQLDatabaseNotInNullSemantics runs the CSV path of the shared
// adapter over an in-memory CSV whose third row has a NULL id.
func TestCSVSQLDatabaseNotInNullSemantics(t *testing.T) {
	const fixture = "id,name\n1,Alice\n2,Bob\n,Carol\n"
	db, err := NewCSVSQLDatabase(CSVSQLDatabaseConfig{
		DriverName: "test-csv", ObjectKey: "records.csv", TableName: "records",
		Delimiter: ',', HasHeader: true, NullValues: []string{"", "NULL"},
	}, func(context.Context, string) error {
		return nil
	}, func(context.Context, string) ([]byte, error) {
		return []byte(fixture), nil
	}, nil)
	if err != nil {
		t.Fatalf("open CSV SQL database: %v", err)
	}
	defer db.Close()
	assertNotInEndToEnd(t, db)
}
