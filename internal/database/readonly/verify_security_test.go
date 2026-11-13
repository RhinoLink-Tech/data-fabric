package readonly

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

type failingPrivilegeConnector struct{}

func (failingPrivilegeConnector) Connect(context.Context) (driver.Conn, error) {
	return failingPrivilegeConn{}, nil
}
func (failingPrivilegeConnector) Driver() driver.Driver { return failingPrivilegeDriver{} }

type failingPrivilegeDriver struct{}

func (failingPrivilegeDriver) Open(string) (driver.Conn, error) { return failingPrivilegeConn{}, nil }

type failingPrivilegeConn struct{}

func (failingPrivilegeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not supported")
}
func (failingPrivilegeConn) Close() error              { return nil }
func (failingPrivilegeConn) Begin() (driver.Tx, error) { return nil, errors.New("not supported") }
func (failingPrivilegeConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, errors.New("driver error: a%2fb with encoded credentials")
}

func TestPrivilegeProbeWarningDoesNotExposeDriverError(t *testing.T) {
	db := sql.OpenDB(failingPrivilegeConnector{})
	defer db.Close()
	for _, probe := range []func(context.Context, *sql.DB) []string{postgresPrivilegeWarnings, mysqlPrivilegeWarnings} {
		warnings := probe(context.Background(), db)
		if len(warnings) != 1 || strings.Contains(warnings[0], "a%2fb") || !strings.Contains(warnings[0], "could not inspect account privileges") {
			t.Fatalf("unsafe privilege warning: %v", warnings)
		}
	}
}
