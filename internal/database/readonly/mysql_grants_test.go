package readonly

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

type grantRowsConnector struct {
	values    []driver.Value
	nextError error
}

func (c grantRowsConnector) Connect(context.Context) (driver.Conn, error) {
	return grantRowsConn{c}, nil
}
func (c grantRowsConnector) Driver() driver.Driver { return grantRowsDriver{} }

type grantRowsDriver struct{}

func (grantRowsDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

type grantRowsConn struct{ connector grantRowsConnector }

func (grantRowsConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (grantRowsConn) Close() error                        { return nil }
func (grantRowsConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (c grantRowsConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &grantRows{values: c.connector.values, nextError: c.connector.nextError}, nil
}

type grantRows struct {
	values    []driver.Value
	nextError error
	index     int
}

func (*grantRows) Columns() []string { return []string{"grant"} }
func (*grantRows) Close() error      { return nil }
func (r *grantRows) Next(dest []driver.Value) error {
	if r.index < len(r.values) {
		dest[0] = r.values[r.index]
		r.index++
		return nil
	}
	if r.nextError != nil {
		err := r.nextError
		r.nextError = nil
		return err
	}
	return io.EOF
}

func TestMySQLGrantInspectionReportsIncompleteResults(t *testing.T) {
	const sensitive = "driver-secret-marker"
	for _, tc := range []struct {
		name       string
		values     []driver.Value
		nextError  error
		incomplete bool
	}{
		{"complete", []driver.Value{"GRANT SELECT, INSERT ON db.* TO user", "GRANT INSERT ON other.* TO user"}, nil, false},
		{"scan failure", []driver.Value{"GRANT SELECT, INSERT ON db.* TO user", nil, nil}, nil, true},
		{"iteration failure", []driver.Value{"GRANT SELECT, INSERT ON db.* TO user"}, errors.New(sensitive), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := sql.OpenDB(grantRowsConnector{values: tc.values, nextError: tc.nextError})
			defer db.Close()
			warnings := mysqlPrivilegeWarnings(context.Background(), db)
			writeCount, incompleteCount := 0, 0
			for _, warning := range warnings {
				if strings.Contains(warning, sensitive) {
					t.Fatal("driver details leaked")
				}
				if strings.Contains(warning, `privilege "INSERT"`) {
					writeCount++
				}
				if strings.Contains(warning, "privilege inspection incomplete") {
					incompleteCount++
				}
			}
			if writeCount != 1 || (incompleteCount == 1) != tc.incomplete || incompleteCount > 1 {
				t.Fatalf("unexpected warnings: %v", warnings)
			}
		})
	}
}
