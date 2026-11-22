package domestic

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/sijms/go-ora/v2/configurations"
)

func TestOceanBaseOracleDSNAndDriverParsing(t *testing.T) {
	for _, schema := range []string{"", `sales"team`} {
		d := &OceanBaseOracleDriver{}
		cfg := &model.DataSourceConfig{Host: "::1", Port: 2883, Database: "svc/name", Username: "usr@corp", Password: "p@ss:/?#&", Schema: schema}
		dsn := d.BuildDSN(cfg)
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		password, _ := u.User.Password()
		if u.Hostname() != "::1" || u.Path != "/svc/name" || u.User.Username() != cfg.Username || password != cfg.Password || u.Query().Get("charset") != "AL32UTF8" || u.Query().Get("schema") != schema {
			t.Fatal("encoded connection fields did not round-trip")
		}
		clean, actual, err := oracleConnectionOptions(dsn)
		if err != nil || actual != schema {
			t.Fatalf("valid DSN rejected: %v", err)
		}
		parsed, err := configurations.ParseConfig(clean)
		if err != nil {
			t.Fatalf("go-ora rejected cleaned DSN: %v", err)
		}
		if parsed.UserID != cfg.Username || parsed.Password != cfg.Password || parsed.ServiceName != cfg.Database {
			t.Fatal("go-ora parsed different credentials or service name")
		}
		if strings.Contains(clean, "schema=") {
			t.Fatal("local schema parameter reached go-ora")
		}
	}
	if (&OceanBaseOracleDriver{}).BuildDSN(nil) != "" {
		t.Fatal("nil config must be rejected")
	}
	if _, err := NewOceanBaseOracleDriver(nil); err == nil {
		t.Fatal("nil constructor config must be rejected")
	}
}
func TestOceanBaseOracleConstructorDefaultsAndSchemaOverride(t *testing.T) {
	d, err := NewOceanBaseOracleDriver(&OceanBaseOracleConfig{Host: "constructor", Charset: "UTF8", Schema: "CONSTRUCTOR"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &model.DataSourceConfig{Host: "host", Port: 2883, Database: "svc", Username: "user", Schema: "SOURCE"}
	clean, schema, err := oracleConnectionOptions(d.BuildDSN(cfg))
	if err != nil || schema != "SOURCE" {
		t.Fatalf("source schema should take precedence: %v", err)
	}
	u, err := url.Parse(clean)
	if err != nil || u.Query().Get("charset") != "UTF8" {
		t.Fatalf("constructor charset: %v", err)
	}
	cfg.Schema = ""
	_, schema, err = oracleConnectionOptions(d.BuildDSN(cfg))
	if err != nil || schema != "CONSTRUCTOR" {
		t.Fatalf("constructor schema fallback: %v", err)
	}
}

func TestOceanBaseOracleConfigFailsClosed(t *testing.T) {
	d := &OceanBaseOracleDriver{}
	cfg := &model.DataSourceConfig{Host: "db.invalid", Port: 2883, Database: "db", Username: "user", SSL: true}
	if err := d.ValidateDSN(d.BuildDSN(cfg)); err == nil || !strings.Contains(err.Error(), "TLS") {
		t.Fatalf("TLS must fail closed: %v", err)
	}
	cfg.SSL = false
	cfg.Schema = "bad\nidentifier"
	if err := d.ValidateDSN(d.BuildDSN(cfg)); err == nil {
		t.Fatal("control characters in schema must be rejected")
	}
}

type oracleFixtureDriver struct{}

func (oracleFixtureDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

type oracleFixtureConnector struct{ conn *oracleFixtureConn }

func (c oracleFixtureConnector) Driver() driver.Driver                        { return oracleFixtureDriver{} }
func (c oracleFixtureConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }

type oracleFixtureConn struct {
	query   string
	closed  bool
	execErr error
}

func (c *oracleFixtureConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *oracleFixtureConn) Close() error                        { c.closed = true; return nil }
func (c *oracleFixtureConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (c *oracleFixtureConn) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	c.query = q
	return driver.RowsAffected(0), c.execErr
}
func TestOracleSchemaConnectorInitializesEveryConnectionAndClosesOnError(t *testing.T) {
	for _, fail := range []bool{false, true} {
		conn := &oracleFixtureConn{}
		if fail {
			conn.execErr = io.ErrUnexpectedEOF
		}
		wrapped := oracleSchemaConnector{base: oracleFixtureConnector{conn}, schema: `sales"team`}
		_, err := wrapped.Connect(context.Background())
		if (err != nil) != fail || conn.query != `ALTER SESSION SET CURRENT_SCHEMA = "sales""team"` || conn.closed != fail {
			t.Fatalf("unexpected initialization: fail=%v error=%v query=%q closed=%v", fail, err, conn.query, conn.closed)
		}
	}
}
