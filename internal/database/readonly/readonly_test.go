package readonly

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// fakeConn is a driver.Conn that records statements and serves canned rows.
type fakeConn struct {
	execs   []string
	queries []string
	rows    map[string][][]driver.Value
	execErr error
	closed  bool
}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeConn) Close() error {
	c.closed = true
	return nil
}

func (c *fakeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("not implemented")
}

func (c *fakeConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.execErr != nil {
		return nil, c.execErr
	}
	c.execs = append(c.execs, query)
	return fakeResult{}, nil
}

func (c *fakeConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.queries = append(c.queries, query)
	rows, ok := c.rows[query]
	if !ok {
		return nil, errors.New("unexpected query: " + query)
	}
	return &fakeRows{data: rows}, nil
}

type fakeResult struct{}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (fakeResult) RowsAffected() (int64, error) { return 0, nil }

// fakeRows serves preloaded driver.Value rows.
type fakeRows struct {
	data [][]driver.Value
	next int
}

func (r *fakeRows) Columns() []string {
	if len(r.data) > 0 {
		columns := make([]string, len(r.data[0]))
		for i := range columns {
			columns[i] = "c"
		}
		return columns
	}
	return []string{"c"}
}

func (r *fakeRows) Close() error { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.next >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.next])
	r.next++
	return nil
}

// fakeConnector hands out fakeConn instances and records them. When serve is
// set, every Connect returns that prebuilt connection instead of a fresh one.
type fakeConnector struct {
	conns []*fakeConn
	serve *fakeConn
	err   error
}

func (f *fakeConnector) Driver() driver.Driver { return fakeDriver{} }

func (f *fakeConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.serve != nil {
		return f.serve, nil
	}
	conn := &fakeConn{}
	f.conns = append(f.conns, conn)
	return conn, nil
}

type fakeDriver struct{}

func (fakeDriver) Open(name string) (driver.Conn, error) {
	return nil, errors.New("not implemented")
}

func TestModeFor(t *testing.T) {
	tests := []struct {
		name   string
		dbType model.DatabaseType
		want   Mode
	}{
		{"mysql", model.DatabaseTypeMySQL, ModeConnectorInit},
		{"mariadb", model.DatabaseTypeMariaDB, ModeConnectorInit},
		{"oceanbase_mysql", model.DatabaseTypeOceanBaseMySQL, ModeConnectorInit},
		{"tdsql", model.DatabaseTypeTDSQL, ModeConnectorInit},
		{"gbase_8s", model.DatabaseTypeGBase8s, ModeConnectorInit},
		{"gbase_8t", model.DatabaseTypeGBase8t, ModeConnectorInit},
		{"postgresql", model.DatabaseTypePostgreSQL, ModeDSNParam},
		{"gaussdb_mysql", model.DatabaseTypeGaussDBMySQL, ModeDSNParam},
		{"gaussdb_postgres", model.DatabaseTypeGaussDBPostgres, ModeDSNParam},
		{"kingbasees", model.DatabaseTypeKingbaseES, ModeDSNParam},
		{"opengauss", model.DatabaseTypeOpenGauss, ModeDSNParam},
		{"oscar", model.DatabaseTypeOscar, ModeDSNParam},

		// Excluded on purpose: unverified read-only behavior on shared wire
		// protocols, no session-level mode, placeholder drivers, or
		// engine-side responsibility. See docs/readonly-access.md.
		{"doris", model.DatabaseTypeApacheDoris, ModeUnsupported},
		{"starrocks", model.DatabaseTypeStarRocks, ModeUnsupported},
		{"hologres", model.DatabaseTypeHologres, ModeUnsupported},
		{"redshift", model.DatabaseTypeRedshift, ModeUnsupported},
		{"tidb", model.DatabaseTypeTiDB, ModeUnsupported},
		{"oracle", model.DatabaseTypeOracle, ModeUnsupported},
		{"oceanbase_oracle", model.DatabaseTypeOceanBaseOracle, ModeUnsupported},
		{"sqlserver", model.DatabaseTypeSQLServer, ModeUnsupported},
		{"snowflake", model.DatabaseTypeSnowflake, ModeUnsupported},
		{"hive", model.DatabaseTypeApacheHive, ModeUnsupported},
		{"clickhouse", model.DatabaseTypeClickHouse, ModeUnsupported},
		{"dameng", model.DatabaseTypeDaMeng, ModeUnsupported},
		{"mongodb", model.DatabaseTypeMongoDB, ModeUnsupported},
		{"trino", model.DatabaseTypeTrino, ModeUnsupported},
		{"presto", model.DatabaseTypePresto, ModeUnsupported},
		{"openlookeng", model.DatabaseTypeOpenLooKeng, ModeUnsupported},
		{"unknown", model.DatabaseType("unknown"), ModeUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ModeFor(tt.dbType); got != tt.want {
				t.Errorf("ModeFor(%q) = %v, want %v", tt.dbType, got, tt.want)
			}
			if got := SupportsReadOnlySession(tt.dbType); got != (tt.want != ModeUnsupported) {
				t.Errorf("SupportsReadOnlySession(%q) = %v, want %v", tt.dbType, got, tt.want != ModeUnsupported)
			}
		})
	}
}

func TestApplyDSN(t *testing.T) {
	tests := []struct {
		name    string
		dbType  model.DatabaseType
		dsn     string
		wantDSN string
		wantOK  bool
	}{
		{
			name:    "url form with existing query",
			dbType:  model.DatabaseTypePostgreSQL,
			dsn:     "postgres://user:pass@localhost:5432/db?sslmode=disable&TimeZone=UTC",
			wantDSN: "postgres://user:pass@localhost:5432/db?sslmode=disable&TimeZone=UTC&default_transaction_read_only=on",
			wantOK:  true,
		},
		{
			name:    "url form without query",
			dbType:  model.DatabaseTypePostgreSQL,
			dsn:     "postgres://user:pass@localhost:5432/db",
			wantDSN: "postgres://user:pass@localhost:5432/db?default_transaction_read_only=on",
			wantOK:  true,
		},
		{
			name:    "keyword form",
			dbType:  model.DatabaseTypeKingbaseES,
			dsn:     "host=localhost port=5432 dbname=db user=user password=pass sslmode=disable search_path=public",
			wantDSN: "host=localhost port=5432 dbname=db user=user password=pass sslmode=disable search_path=public default_transaction_read_only=on",
			wantOK:  true,
		},
		{
			name:    "keyword form minimal",
			dbType:  model.DatabaseTypeOpenGauss,
			dsn:     "host=localhost port=5432 dbname=db",
			wantDSN: "host=localhost port=5432 dbname=db default_transaction_read_only=on",
			wantOK:  true,
		},
		{
			name:    "mysql is not DSN injected",
			dbType:  model.DatabaseTypeMySQL,
			dsn:     "user:pass@tcp(localhost:3306)/db",
			wantDSN: "user:pass@tcp(localhost:3306)/db",
			wantOK:  false,
		},
		{
			name:    "unsupported type",
			dbType:  model.DatabaseTypeOracle,
			dsn:     "user/pass@localhost:1521/db",
			wantDSN: "user/pass@localhost:1521/db",
			wantOK:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ApplyDSN(tt.dbType, tt.dsn)
			if ok != tt.wantOK {
				t.Errorf("ApplyDSN(%q) ok = %v, want %v", tt.dbType, ok, tt.wantOK)
			}
			if got != tt.wantDSN {
				t.Errorf("ApplyDSN(%q) = %q, want %q", tt.dbType, got, tt.wantDSN)
			}
		})
	}
}

func TestInitConnectorRunsInitPerConnection(t *testing.T) {
	base := &fakeConnector{}
	conn := initConnector{base: base, initSQL: "SET SESSION TRANSACTION READ ONLY"}

	for i := 0; i < 2; i++ { // simulate pool refill/recycling
		got, err := conn.Connect(context.Background())
		if err != nil {
			t.Fatalf("Connect() error = %v", err)
		}
		if got == nil {
			t.Fatal("Connect() returned nil connection")
		}
	}
	if len(base.conns) != 2 {
		t.Fatalf("base connector saw %d connections, want 2", len(base.conns))
	}
	for i, c := range base.conns {
		if len(c.execs) != 1 || c.execs[0] != "SET SESSION TRANSACTION READ ONLY" {
			t.Errorf("connection %d execs = %v, want exactly [SET SESSION TRANSACTION READ ONLY]", i, c.execs)
		}
	}
}

func TestInitConnectorFailsClosed(t *testing.T) {
	t.Run("init statement rejected", func(t *testing.T) {
		base := &fakeConnector{}
		base.conns = append(base.conns, &fakeConn{execErr: errors.New("engine rejected")})
		// fakeConnector.Connect creates fresh conns, so use the recorded one directly.
		connector := initConnector{base: recordedConnector{conn: base.conns[0]}, initSQL: "SET SESSION TRANSACTION READ ONLY"}

		got, err := connector.Connect(context.Background())
		if err == nil {
			t.Fatal("Connect() expected error when init statement fails")
		}
		if got != nil {
			t.Fatal("Connect() must return nil conn on failure")
		}
		if !errors.Is(err, ErrReadOnlyInitFailed) {
			t.Errorf("error %v does not wrap ErrReadOnlyInitFailed", err)
		}
		if !strings.Contains(err.Error(), "enforce_read_only=false") {
			t.Errorf("error %q missing opt-out hint", err)
		}
		if !base.conns[0].closed {
			t.Error("failed connection was not closed")
		}
	})

	t.Run("connection without ExecerContext", func(t *testing.T) {
		connector := initConnector{base: plainConnector{}, initSQL: "SET SESSION TRANSACTION READ ONLY"}
		_, err := connector.Connect(context.Background())
		if err == nil {
			t.Fatal("Connect() expected error without ExecerContext")
		}
		if !errors.Is(err, ErrReadOnlyInitFailed) {
			t.Errorf("error %v does not wrap ErrReadOnlyInitFailed", err)
		}
	})
}

// recordedConnector always returns one prebuilt connection.
type recordedConnector struct {
	conn *fakeConn
}

func (r recordedConnector) Driver() driver.Driver { return fakeDriver{} }

func (r recordedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return r.conn, nil
}

// plainConnector returns a connection implementing only driver.Conn.
type plainConnector struct{}

func (plainConnector) Driver() driver.Driver { return fakeDriver{} }

func (plainConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return bareConn{}, nil
}

type bareConn struct{}

func (bareConn) Prepare(query string) (driver.Stmt, error) { return nil, errors.New("not implemented") }
func (bareConn) Close() error                              { return nil }
func (bareConn) Begin() (driver.Tx, error)                 { return nil, errors.New("not implemented") }

func TestOpenDB(t *testing.T) {
	t.Run("mysql", func(t *testing.T) {
		db, handled, err := OpenDB(model.DatabaseTypeMySQL, "user:pass@tcp(localhost:3306)/db")
		if err != nil {
			t.Fatalf("OpenDB() error = %v", err)
		}
		if !handled {
			t.Fatal("OpenDB(mysql) handled = false, want true")
		}
		if db == nil {
			t.Fatal("OpenDB(mysql) returned nil db")
		}
		db.Close() // no network happened: connectors are lazy
	})

	t.Run("invalid dsn", func(t *testing.T) {
		_, handled, err := OpenDB(model.DatabaseTypeMySQL, "not a valid dsn ://")
		if !handled {
			t.Fatal("OpenDB(mysql, invalid) handled = false, want true")
		}
		if err == nil {
			t.Fatal("OpenDB(mysql, invalid) expected error")
		}
		if !errors.Is(err, ErrReadOnlyInitFailed) {
			t.Errorf("error %v does not wrap ErrReadOnlyInitFailed", err)
		}
	})

	t.Run("non mysql type falls back", func(t *testing.T) {
		db, handled, err := OpenDB(model.DatabaseTypePostgreSQL, "postgres://user:pass@localhost:5432/db")
		if db != nil || handled || err != nil {
			t.Errorf("OpenDB(postgresql) = (%v, %v, %v), want (nil, false, nil)", db, handled, err)
		}
	})
}

func TestVerifySessionPostgres(t *testing.T) {
	conn := &fakeConn{rows: map[string][][]driver.Value{
		"SHOW transaction_read_only": {{"on"}},
		"SELECT rolsuper, rolcreatedb, rolcreaterole FROM pg_roles WHERE rolname = current_user": {
			{false, false, false},
		},
	}}
	var gotDSN string
	open := func(dsn string) (*sql.DB, error) {
		gotDSN = dsn
		return sql.OpenDB(&fakeConnector{serve: conn}), nil
	}

	enforced, warnings, err := VerifySession(context.Background(), model.DatabaseTypePostgreSQL, open,
		"postgres://user:pass@localhost:5432/db?sslmode=disable")
	if err != nil {
		t.Fatalf("VerifySession() error = %v", err)
	}
	if !enforced {
		t.Error("VerifySession(postgresql) enforced = false, want true")
	}
	if len(warnings) != 0 {
		t.Errorf("VerifySession() warnings = %v, want none", warnings)
	}
	if !strings.Contains(gotDSN, "default_transaction_read_only=on") {
		t.Errorf("open() dsn %q missing read-only parameter", gotDSN)
	}
}

func TestVerifySessionOnDBMySQL(t *testing.T) {
	conn := &fakeConn{rows: map[string][][]driver.Value{
		"SELECT @@SESSION.TRANSACTION_READ_ONLY": {{int64(1)}},
		"SHOW GRANTS FOR CURRENT_USER()": {
			{"GRANT SELECT ON db.* TO 'ro'@'%'"},
			{"GRANT INSERT ON other.* TO 'ro'@'%'"},
		},
	}}
	db := sql.OpenDB(&fakeConnector{serve: conn})
	defer db.Close()

	enforced, warnings, err := verifySessionOnDB(context.Background(), db, model.DatabaseTypeMySQL)
	if err != nil {
		t.Fatalf("verifySessionOnDB() error = %v", err)
	}
	if !enforced {
		t.Error("verifySessionOnDB(mysql) enforced = false, want true")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"INSERT"`) {
		t.Errorf("verifySessionOnDB() warnings = %v, want one INSERT warning", warnings)
	}
}

func TestVerifySessionOnDBPostgresRoleWarnings(t *testing.T) {
	conn := &fakeConn{rows: map[string][][]driver.Value{
		"SHOW transaction_read_only": {{"on"}},
		"SELECT rolsuper, rolcreatedb, rolcreaterole FROM pg_roles WHERE rolname = current_user": {
			{true, false, true},
		},
	}}
	db := sql.OpenDB(&fakeConnector{serve: conn})
	defer db.Close()

	enforced, warnings, err := verifySessionOnDB(context.Background(), db, model.DatabaseTypeOpenGauss)
	if err != nil {
		t.Fatalf("verifySessionOnDB() error = %v", err)
	}
	if !enforced {
		t.Error("verifySessionOnDB(opengauss) enforced = false, want true")
	}
	if len(warnings) != 2 {
		t.Fatalf("verifySessionOnDB() warnings = %v, want 2 (rolsuper, rolcreaterole)", warnings)
	}
	if !strings.Contains(warnings[0], "rolsuper") || !strings.Contains(warnings[1], "rolcreaterole") {
		t.Errorf("verifySessionOnDB() warnings = %v, want rolsuper and rolcreaterole", warnings)
	}
}

func TestVerifySessionUnsupportedType(t *testing.T) {
	enforced, warnings, err := VerifySession(context.Background(), model.DatabaseTypeOracle,
		func(dsn string) (*sql.DB, error) { return nil, errors.New("must not open") }, "dsn")
	if enforced || warnings != nil || err != nil {
		t.Errorf("VerifySession(oracle) = (%v, %v, %v), want (false, nil, nil)", enforced, warnings, err)
	}
}
