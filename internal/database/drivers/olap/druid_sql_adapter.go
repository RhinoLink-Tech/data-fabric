package olap

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var errDruidReadOnly = errors.New("druid supports read-only SELECT and EXPLAIN queries only")

type druidConnector struct{ client *DruidRESTClient }

func (c *druidConnector) Connect(context.Context) (driver.Conn, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("Druid REST client is required")
	}
	return &druidConn{client: c.client}, nil
}

func (c *druidConnector) Driver() driver.Driver { return druidDatabaseDriver{connector: c} }

type druidDatabaseDriver struct{ connector *druidConnector }

func (d druidDatabaseDriver) Open(string) (driver.Conn, error) {
	return d.connector.Connect(context.Background())
}

type druidConn struct{ client *DruidRESTClient }

func (c *druidConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Druid does not support prepared statements through this adapter")
}
func (c *druidConn) Close() error              { return nil }
func (c *druidConn) Begin() (driver.Tx, error) { return nil, errDruidReadOnly }
func (c *druidConn) Ping(ctx context.Context) error {
	status, err := c.client.GetClusterStatus(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(status.Version) == "" {
		return errors.New("Druid status did not include a server version")
	}
	return nil
}

func (c *druidConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if !isDruidReadOnlyQuery(query) {
		return nil, errDruidReadOnly
	}
	parameters, err := druidParameters(args)
	if err != nil {
		return nil, err
	}
	result, err := c.client.ExecuteSQLQueryWithParameters(ctx, query, parameters)
	if err != nil {
		return nil, err
	}
	rows := make([][]driver.Value, len(result.Values))
	for rowIndex, row := range result.Values {
		if len(row) != len(result.Columns) {
			return nil, fmt.Errorf("Druid result row %d has %d values, want %d", rowIndex, len(row), len(result.Columns))
		}
		values := make([]driver.Value, len(result.Columns))
		for columnIndex := range result.Columns {
			values[columnIndex] = toDruidDriverValue(row[columnIndex])
		}
		rows[rowIndex] = values
	}
	return &druidRows{ctx: ctx, columns: result.Columns, types: result.ColumnTypes, rows: rows}, nil
}

func (c *druidConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, errDruidReadOnly
}

type druidRows struct {
	ctx            context.Context
	columns, types []string
	rows           [][]driver.Value
	next           int
}

func (r *druidRows) Columns() []string { return append([]string(nil), r.columns...) }
func (r *druidRows) Close() error      { return nil }
func (r *druidRows) Next(dest []driver.Value) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if r.next >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.next])
	r.next++
	return nil
}
func (r *druidRows) ColumnTypeDatabaseTypeName(index int) string {
	if index < 0 || index >= len(r.types) || strings.TrimSpace(r.types[index]) == "" {
		return "VARCHAR"
	}
	return r.types[index]
}
func (r *druidRows) ColumnTypeNullable(int) (nullable, ok bool) { return true, true }

func isDruidReadOnlyQuery(query string) bool {
	statement := strings.TrimSpace(stripDruidLeadingComments(query))
	statement = strings.TrimSpace(strings.TrimSuffix(statement, ";"))
	if statement == "" || strings.Contains(statement, ";") {
		return false
	}
	upper := strings.ToUpper(statement)
	return strings.HasPrefix(upper, "SELECT ") || upper == "SELECT" ||
		strings.HasPrefix(upper, "EXPLAIN ") || upper == "EXPLAIN"
}

func stripDruidLeadingComments(query string) string {
	remaining := strings.TrimSpace(query)
	for {
		switch {
		case strings.HasPrefix(remaining, "--"):
			if newline := strings.IndexByte(remaining, '\n'); newline >= 0 {
				remaining = strings.TrimSpace(remaining[newline+1:])
				continue
			}
			return ""
		case strings.HasPrefix(remaining, "/*"):
			end := strings.Index(remaining[2:], "*/")
			if end < 0 {
				return ""
			}
			remaining = strings.TrimSpace(remaining[end+4:])
			continue
		default:
			return remaining
		}
	}
}

func druidParameters(args []driver.NamedValue) ([]DruidQueryParameter, error) {
	parameters := make([]DruidQueryParameter, 0, len(args))
	for _, arg := range args {
		parameter, err := druidParameter(arg.Value)
		if err != nil {
			return nil, fmt.Errorf("Druid parameter %d: %w", arg.Ordinal, err)
		}
		parameters = append(parameters, parameter)
	}
	return parameters, nil
}

func druidParameter(value interface{}) (DruidQueryParameter, error) {
	switch typed := value.(type) {
	case nil:
		return DruidQueryParameter{Type: "VARCHAR", Value: nil}, nil
	case bool:
		return DruidQueryParameter{Type: "BOOLEAN", Value: typed}, nil
	case int64:
		return DruidQueryParameter{Type: "BIGINT", Value: typed}, nil
	case int:
		return DruidQueryParameter{Type: "BIGINT", Value: int64(typed)}, nil
	case int32:
		return DruidQueryParameter{Type: "BIGINT", Value: int64(typed)}, nil
	case float64:
		return DruidQueryParameter{Type: "DOUBLE", Value: typed}, nil
	case float32:
		return DruidQueryParameter{Type: "DOUBLE", Value: float64(typed)}, nil
	case string:
		return DruidQueryParameter{Type: "VARCHAR", Value: typed}, nil
	case []byte:
		return DruidQueryParameter{Type: "VARCHAR", Value: string(typed)}, nil
	case time.Time:
		return DruidQueryParameter{Type: "TIMESTAMP", Value: typed.UTC().Format(time.RFC3339Nano)}, nil
	default:
		return DruidQueryParameter{}, fmt.Errorf("unsupported parameter type %T", value)
	}
}

func toDruidDriverValue(value interface{}) driver.Value {
	switch typed := value.(type) {
	case nil:
		return nil
	case string, bool, int64, float64, time.Time:
		return typed
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer
		}
		if number, err := typed.Float64(); err == nil {
			return number
		}
		return typed.String()
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
}

func druidValueFromJSON(raw json.RawMessage) (interface{}, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func druidSQLTypeName(raw json.RawMessage) string {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return "VARCHAR"
	}
	return value
}
