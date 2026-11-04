package domestic

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

// --- canned SQL scaffolding (no network): routes statements by query text ---

type cannedConnector struct {
	lookup func(query string) driver.Rows
}

func (c cannedConnector) Connect(context.Context) (driver.Conn, error) {
	return &cannedConn{lookup: c.lookup}, nil
}
func (cannedConnector) Driver() driver.Driver { return cannedDriver{} }

type cannedDriver struct{}

func (cannedDriver) Open(string) (driver.Conn, error) { return nil, errors.New("open via connector only") }

type cannedConn struct {
	lookup func(string) driver.Rows
}

func (c *cannedConn) Prepare(query string) (driver.Stmt, error) {
	return &cannedStmt{lookup: c.lookup, query: query}, nil
}
func (c *cannedConn) Close() error              { return nil }
func (c *cannedConn) Begin() (driver.Tx, error) { return nil, errors.New("transactions unused") }

type cannedStmt struct {
	query  string
	lookup func(string) driver.Rows
}

func (s *cannedStmt) Close() error              { return nil }
func (s *cannedStmt) NumInput() int             { return -1 }
func (s *cannedStmt) Query([]driver.Value) (driver.Rows, error) {
	return s.lookup(s.query), nil
}
func (s *cannedStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("exec unused")
}

type cannedRows struct {
	cols []string
	vals [][]driver.Value
	i    int
}

func (r *cannedRows) Columns() []string { return r.cols }
func (r *cannedRows) Close() error      { return nil }
func (r *cannedRows) Next(dest []driver.Value) error {
	if r.i >= len(r.vals) {
		return io.EOF
	}
	row := r.vals[r.i]
	r.i++
	for j := range dest {
		dest[j] = row[j]
	}
	return nil
}

// --- H2: GetDatabaseInfo must not mask a replica-count failure as 0 ---

func TestGaussDBGetDatabaseInfoPropagatesReplicaCountError(t *testing.T) {
	d := &GaussDBDriver{}
	lookup := func(q string) driver.Rows {
		if strings.Contains(q, "version()") {
			return &cannedRows{cols: []string{"version"}, vals: [][]driver.Value{{"9.2"}}}
		}
		// pg_stat_replication query yields no rows -> sql.ErrNoRows on Scan.
		return &cannedRows{}
	}
	db := sql.OpenDB(cannedConnector{lookup: lookup})

	_, err := d.GetDatabaseInfo(context.Background(), db)
	if err == nil {
		t.Fatal("GetDatabaseInfo() = nil error, want the replica-count failure surfaced instead of a silent ReplicaCount=0")
	}
}

func TestGaussDBGetDatabaseInfoCountsReplicas(t *testing.T) {
	d := &GaussDBDriver{}
	lookup := func(q string) driver.Rows {
		if strings.Contains(q, "version()") {
			return &cannedRows{cols: []string{"version"}, vals: [][]driver.Value{{"9.2"}}}
		}
		return &cannedRows{cols: []string{"count"}, vals: [][]driver.Value{{int64(2)}}}
	}
	db := sql.OpenDB(cannedConnector{lookup: lookup})

	info, err := d.GetDatabaseInfo(context.Background(), db)
	if err != nil {
		t.Fatalf("GetDatabaseInfo() error = %v", err)
	}
	if info.ReplicaCount != 2 {
		t.Errorf("ReplicaCount = %d, want 2", info.ReplicaCount)
	}
}

// --- H1: SwitchToStandby must hand back the standby handle and never close
// the caller's pool ---

func TestGaussDBSwitchToStandbyWithoutStandbyNodes(t *testing.T) {
	d := &GaussDBDriver{config: &GaussDBConfig{StandbyNodes: []string{}}}
	db := sql.OpenDB(cannedConnector{lookup: func(string) driver.Rows { return &cannedRows{} }})

	standby, err := d.SwitchToStandby(context.Background(), db)
	if err == nil {
		t.Fatal("SwitchToStandby() = nil error with no standby nodes configured")
	}
	if standby != nil {
		t.Errorf("SwitchToStandby() returned handle %p, want nil on error", standby)
	}
	// The caller owns db; it must not have been closed.
	if err := db.PingContext(context.Background()); err != nil {
		t.Errorf("caller's *sql.DB was closed by SwitchToStandby: %v", err)
	}
}

func TestGaussDBSwitchToStandbyNilConfigDoesNotPanic(t *testing.T) {
	d := &GaussDBDriver{} // zero value as handed back by the registry
	db := sql.OpenDB(cannedConnector{lookup: func(string) driver.Rows { return &cannedRows{} }})

	if _, err := d.SwitchToStandby(context.Background(), db); err == nil {
		t.Fatal("SwitchToStandby() = nil error with nil config")
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Errorf("caller's *sql.DB was closed by SwitchToStandby: %v", err)
	}
}