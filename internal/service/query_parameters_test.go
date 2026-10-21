package service

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"encoding/json"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// Ordered parameters must normalize into values a SQL driver can bind (#21).
func TestNormalizeFetchParameters(t *testing.T) {
	tests := []struct {
		name  string
		param interface{}
		want  interface{}
	}{
		{"null binds SQL NULL", nil, nil},
		{"boolean passes through", true, true},
		{"string passes through", "abc", "abc"},
		{"integer json number keeps int64 exactness", json.Number("9223372036854775807"), int64(math.MaxInt64)},
		{"negative integer json number", json.Number("-42"), int64(-42)},
		{"decimal json number becomes float64", json.Number("1.5"), float64(1.5)},
		{"exponent json number becomes float64", json.Number("1e3"), float64(1000)},
		{"int widens to int64", 5, int64(5)},
		{"int64 passes through", int64(1) << 40, int64(1) << 40},
		{"int32 passes through", int32(7), int32(7)},
		{"float64 passes through when finite", 2.5, float64(2.5)},
		{"float32 widens to finite float64", float32(1.5), float64(1.5)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeFetchParameters([]interface{}{tt.param})
			if err != nil {
				t.Fatalf("normalizeFetchParameters() error = %v", err)
			}
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("normalizeFetchParameters() = %#v, want [%#v]", got, tt.want)
			}
		})
	}
}

// Empty and nil lists bind nothing.
func TestNormalizeFetchParametersEmpty(t *testing.T) {
	for _, params := range [][]interface{}{nil, {}} {
		got, err := normalizeFetchParameters(params)
		if err != nil {
			t.Fatalf("normalizeFetchParameters(%#v) error = %v", params, err)
		}
		if got != nil {
			t.Errorf("normalizeFetchParameters(%#v) = %#v, want nil", params, got)
		}
	}
}

// Composite, non-finite, and out-of-range values are rejected. The sentinel
// payload must never surface in the error text: a rejected request cannot
// leak its own contents (#21).
func TestNormalizeFetchParametersRejectsUnsafeValues(t *testing.T) {
	const sentinel = "SECRET_PAYLOAD_9f1e"
	tests := []struct {
		name  string
		param interface{}
		want  string
	}{
		{"map", map[string]interface{}{"k": sentinel}, "query parameter 0 has unsupported type map[string]interface {}: use null, boolean, number, or string"},
		{"slice", []interface{}{sentinel}, "query parameter 0 has unsupported type []interface {}: use null, boolean, number, or string"},
		{"struct", struct{ V string }{V: sentinel}, "query parameter 0 has unsupported type struct { V string }: use null, boolean, number, or string"},
		{"NaN", math.NaN(), "query parameter 0 is not a finite number"},
		{"+Inf", math.Inf(1), "query parameter 0 is not a finite number"},
		{"-Inf", math.Inf(-1), "query parameter 0 is not a finite number"},
		{"non-numeric json number", json.Number(sentinel), "query parameter 0 is not a valid number"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeFetchParameters([]interface{}{tt.param})
			if err == nil {
				t.Fatalf("normalizeFetchParameters() accepted %#v", tt.param)
			}
			if err.Error() != tt.want {
				t.Errorf("error = %q, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), sentinel) {
				t.Errorf("error %q leaks the parameter value", err)
			}
		})
	}
}

// The parameter count is bounded.
func TestNormalizeFetchParametersBoundsCount(t *testing.T) {
	params := make([]interface{}, maxFetchParameters+1)
	_, err := normalizeFetchParameters(params)
	if err == nil || !strings.Contains(err.Error(), "too many query parameters") {
		t.Fatalf("error = %v, want too-many-parameters failure", err)
	}

	exact := make([]interface{}, maxFetchParameters)
	if got, err := normalizeFetchParameters(exact); err != nil || len(got) != maxFetchParameters {
		t.Fatalf("exact limit rejected: got=%d err=%v", len(got), err)
	}
}

// JSON decoded with UseNumber (the controller and MCP paths) must keep large
// integers exact through normalization instead of degrading to float64 (#21).
func TestNormalizeFetchParametersKeepsWireIntegersExact(t *testing.T) {
	var raw []interface{}
	decoder := json.NewDecoder(strings.NewReader(`[9223372036854775807, 1.5, null, "txt", true]`))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		t.Fatalf("decode error = %v", err)
	}

	got, err := normalizeFetchParameters(raw)
	if err != nil {
		t.Fatalf("normalizeFetchParameters() error = %v", err)
	}
	want := []interface{}{int64(math.MaxInt64), float64(1.5), nil, "txt", true}
	if len(got) != len(want) {
		t.Fatalf("normalized %d values, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parameter %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

// recordedCall captures one statement the driver was asked to run, with the
// values the database/sql layer bound to it.
type recordedCall struct {
	query string
	args  []sqldriver.Value
}

type recordingConn struct{ calls *[]recordedCall }

func (recordingConn) Prepare(string) (sqldriver.Stmt, error) {
	return nil, errUnsupportedPrepare{}
}
func (recordingConn) Close() error                 { return nil }
func (recordingConn) Begin() (sqldriver.Tx, error) { return nil, errUnsupportedPrepare{} }
func (c recordingConn) QueryContext(_ context.Context, query string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	values := make([]sqldriver.Value, len(args))
	for i, a := range args {
		values[i] = a.Value
	}
	*c.calls = append(*c.calls, recordedCall{query: query, args: values})
	if strings.HasPrefix(query, "SELECT COUNT(*)") {
		return &recordedRows{values: [][]sqldriver.Value{{int64(2)}}}, nil
	}
	return &recordedRows{values: [][]sqldriver.Value{{[]byte("a")}, {[]byte("b")}}}, nil
}

type errUnsupportedPrepare struct{}

func (errUnsupportedPrepare) Error() string { return "prepared statements are not supported" }

// recordedRows serves its fixture once, one row per Next call.
type recordedRows struct {
	values [][]sqldriver.Value
	pos    int
}

func (*recordedRows) Columns() []string { return []string{"note"} }
func (*recordedRows) Close() error      { return nil }
func (r *recordedRows) Next(dest []sqldriver.Value) error {
	if r.pos >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.pos])
	r.pos++
	return nil
}

// recordingConnector hands out connections that share one call log.
type recordingConnector struct{ calls *[]recordedCall }

func (c recordingConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return recordingConn{calls: c.calls}, nil
}
func (recordingConnector) Driver() sqldriver.Driver { return recordingDriver{} }

type recordingDriver struct{}

func (recordingDriver) Open(string) (sqldriver.Conn, error) {
	return nil, errUnsupportedPrepare{}
}

// The batch statement and the row count must both receive the same bound
// values, and neither statement may embed the values in its SQL text: the
// placeholder survives and the payload never appears in the query (#21).
func TestExecuteBatchQueryBindsParametersForDataAndCount(t *testing.T) {
	var calls []recordedCall
	db := sql.OpenDB(recordingConnector{calls: &calls})
	defer db.Close()

	qs := &queryService{connPool: database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)}
	args := []interface{}{int64(7), "abc", nil}
	// batchSize 2 matches the fixture size, so the page counts as full and
	// the count statement runs as a second database call.
	_, entries, totalCount, err := qs.executeBatchQuery(context.Background(), model.DatabaseTypeMySQL, db,
		"SELECT note FROM mix WHERE id > ? AND name = ? AND deleted_at IS ?", 2, 0, args)
	if err != nil {
		t.Fatalf("executeBatchQuery() error = %v", err)
	}
	if len(entries) != 2 || totalCount != 2 {
		t.Fatalf("entries=%d totalCount=%d, want 2 entries and count 2", len(entries), totalCount)
	}

	if len(calls) != 2 {
		t.Fatalf("driver saw %d statements, want 2 (data + count)", len(calls))
	}
	dataCall, countCall := calls[0], calls[1]
	if !strings.Contains(dataCall.query, "LIMIT") {
		t.Errorf("data statement %q is not paginated", dataCall.query)
	}
	if !strings.HasPrefix(countCall.query, "SELECT COUNT(*)") {
		t.Errorf("count statement %q does not wrap the query", countCall.query)
	}

	wantArgs := []sqldriver.Value{int64(7), "abc", nil}
	for _, call := range []recordedCall{dataCall, countCall} {
		if len(call.args) != len(wantArgs) {
			t.Fatalf("statement %q bound %d args, want %d", call.query, len(call.args), len(wantArgs))
		}
		for i, want := range wantArgs {
			if call.args[i] != want {
				t.Errorf("statement %q bound arg %d = %#v, want %#v", call.query, i, call.args[i], want)
			}
		}
		if strings.Count(call.query, "?") != 3 {
			t.Errorf("statement %q should keep 3 placeholders, got %d", call.query, strings.Count(call.query, "?"))
		}
		if strings.Contains(call.query, "abc") {
			t.Errorf("statement %q interpolates the parameter value into SQL text", call.query)
		}
	}
}
