package service

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// The fetch wire representation (#123): NULL, binary, text, and exact
// numerals must stay distinguishable after encoding.
func TestEncodeFetchValue(t *testing.T) {
	nonUTF8 := []byte{0xff, 0xfe, 0x00, 0x01}

	tests := []struct {
		name       string
		value      interface{}
		columnType model.StandardizedType
		want       interface{}
	}{
		{"SQL NULL stays nil", nil, model.TypeString, nil},
		{"text bytes become string", []byte("hello"), model.TypeString, "hello"},
		{"decimal bytes keep exact text", []byte("12345678901234567890.123456789"), model.TypeDecimal, "12345678901234567890.123456789"},
		{"binary bytes become base64", nonUTF8, model.TypeBinary, base64.StdEncoding.EncodeToString(nonUTF8)},
		{"unknown type treats bytes as text", []byte("plain"), model.TypeUnknown, "plain"},
		{"int64 passes through exactly", int64(1) << 62, model.TypeBigInt, int64(1) << 62},
		{"bool passes through", true, model.TypeBoolean, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := encodeFetchValue(tt.value, tt.columnType); got != tt.want {
				t.Errorf("encodeFetchValue(%v, %s) = %#v, want %#v", tt.value, tt.columnType, got, tt.want)
			}
		})
	}
}

// A row wider than the column list (defensive) still encodes: unknown
// columns treat bytes as text.
func TestEncodeFetchRowWithMissingColumnTypes(t *testing.T) {
	row := []interface{}{[]byte("text"), []byte{0xff}, nil}
	encodeFetchRow(row, []model.ColumnInfo{{Type: model.TypeString}})

	if row[0] != "text" || row[1] != string([]byte{0xff}) || row[2] != nil {
		t.Errorf("encodeFetchRow() = %#v, want [text <raw bytes> nil]", row)
	}
}

// typedRows serves one result set whose values exercise every wire-
// representation rule, with column type metadata the batch path reads.
type typedRows struct {
	columns []string
	types   []string
	values  [][]sqldriver.Value
	pos     int
}

func (r *typedRows) Columns() []string { return r.columns }
func (*typedRows) Close() error        { return nil }
func (r *typedRows) Next(dest []sqldriver.Value) error {
	if r.pos >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.pos])
	r.pos++
	return nil
}

func (r *typedRows) ColumnTypeDatabaseTypeName(index int) string { return r.types[index] }
func (r *typedRows) ColumnTypeNullable(index int) (bool, bool)   { return true, true }

type typedConn struct{ lookup func(query string) *typedRows }

func (c typedConn) Prepare(string) (sqldriver.Stmt, error) {
	return nil, errors.New("prepared statements are not supported")
}
func (typedConn) Close() error                 { return nil }
func (typedConn) Begin() (sqldriver.Tx, error) { return nil, errors.New("transactions unused") }
func (c typedConn) QueryContext(_ context.Context, query string, _ []sqldriver.NamedValue) (sqldriver.Rows, error) {
	return c.lookup(query), nil
}

type typedConnector struct{ lookup func(query string) *typedRows }

func (c typedConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return typedConn{lookup: c.lookup}, nil
}
func (typedConnector) Driver() sqldriver.Driver { return typedDriver{} }

type typedDriver struct{}

func (typedDriver) Open(string) (sqldriver.Conn, error) {
	return nil, errors.New("typedDriver must be used through its connector")
}

// The batch path must return distinguishable values for the whole fixture:
// SQL NULL, the literal text "NULL", non-UTF-8 binary, a large exact DECIMAL,
// and an out-of-float-range integer (#123, #24).
func TestExecuteBatchQueryEncodesWireRepresentation(t *testing.T) {
	binaryValue := []byte{0xff, 0xfe, 0x00, 0x01} // not valid UTF-8
	decimalText := "12345678901234567890.123456789"
	bigInt := int64(1) << 62

	// Each query gets a fresh rows instance: the batch read consumes one and
	// the count query runs a second statement.
	newFixture := func() *typedRows {
		return &typedRows{
			columns: []string{"note", "avatar", "price", "count"},
			types:   []string{"VARCHAR", "BLOB", "DECIMAL", "BIGINT"},
			values: [][]sqldriver.Value{
				{nil, binaryValue, []byte(decimalText), bigInt},
				{[]byte("NULL"), nil, nil, nil},
			},
		}
	}
	lookup := func(query string) *typedRows {
		if strings.HasPrefix(query, "SELECT COUNT(*)") {
			return &typedRows{
				columns: []string{"count"},
				types:   []string{"BIGINT"},
				values:  [][]sqldriver.Value{{int64(2)}},
			}
		}
		return newFixture()
	}
	db := sql.OpenDB(typedConnector{lookup: lookup})

	qs := &queryService{connPool: database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)}
	columns, entries, _, err := qs.executeBatchQuery(context.Background(), model.DatabaseTypeMySQL, db,
		"SELECT note, avatar, price, count FROM mix", 2, 0, nil)
	if err != nil {
		t.Fatalf("executeBatchQuery() error = %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("entries = %d rows, want 2", len(entries))
	}

	first := *entries[0]
	if first[0] != nil {
		t.Errorf("SQL NULL encoded as %#v, want nil (JSON null)", first[0])
	}
	if got, want := first[1], base64.StdEncoding.EncodeToString(binaryValue); got != want {
		t.Errorf("binary encoded as %#v, want base64 %q", got, want)
	}
	if got := first[2]; got != decimalText {
		t.Errorf("decimal encoded as %#v, want exact text %q", got, decimalText)
	}
	if got := first[3]; got != bigInt {
		t.Errorf("bigint encoded as %#v, want exact int64 %#v", got, bigInt)
	}

	second := *entries[1]
	if second[0] != "NULL" {
		t.Errorf("literal text NULL encoded as %#v, want the string \"NULL\" — text and null must differ", second[0])
	}
	if second[1] != nil || second[2] != nil || second[3] != nil {
		t.Errorf("nullables encoded as %#v, want nil in every position", second)
	}

	if len(columns) != 4 || columns[1].Type != model.TypeBinary {
		t.Errorf("columns = %+v, want 4 columns with a binary-typed avatar", columns)
	}

	// The final JSON must carry the same distinctions: null vs "NULL", the
	// base64 body, and the exact decimal digits.
	encoded, err := json.Marshal(&model.FetchQueryResponse{Entries: entries})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	wire := string(encoded)
	for _, want := range []string{
		"[null,", // SQL NULL as JSON null
		`"NULL"`,
		base64.StdEncoding.EncodeToString(binaryValue),
		decimalText,
	} {
		if !strings.Contains(wire, want) {
			t.Errorf("wire JSON %s missing %q", wire, want)
		}
	}
}
