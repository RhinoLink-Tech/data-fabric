package traditional

import (
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/go-sql-driver/mysql"
	"net/url"
	"testing"
)

func TestStructuredDSNDoesNotInterpretCredentialOrDatabaseDelimiters(t *testing.T) {
	c := &model.DataSourceConfig{Host: "127.0.0.1", Port: 5432, Username: "reader@evil:42", Password: "s' /@host?sslmode=disable&x=1\\word", Database: "orders/?sslmode=disable", Timezone: "UTC&sslrootcert=/tmp/evil", SSL: true}
	d := (&PostgreSQLDriver{}).BuildDSN(c)
	u, err := url.Parse(d)
	if err != nil {
		t.Fatal(err)
	}
	pass, _ := u.User.Password()
	if u.Hostname() != c.Host || u.Port() != "5432" || u.User.Username() != c.Username || pass != c.Password || u.Path != "/"+c.Database || u.Query().Get("TimeZone") != c.Timezone || u.Query().Get("sslmode") != "require" || u.Query().Has("sslrootcert") {
		t.Fatal("PostgreSQL DSN did not preserve boundaries")
	}
	c.Port = 3306
	c.Username = "reader"
	c.Timezone = "UTC"
	c.SSL = false
	d = (&MySQLDriver{}).BuildDSN(c)
	m, err := mysql.ParseDSN(d)
	if err != nil {
		t.Fatal(err)
	}
	if m.Addr != "127.0.0.1:3306" || m.User != c.Username || m.Passwd != c.Password || m.DBName != c.Database || m.TLSConfig != "" || m.Params["sslrootcert"] != "" {
		t.Fatal("MySQL DSN did not preserve boundaries")
	}
}
func TestStructuredDSNRejectsUnrepresentableTargetsAndMySQLUsername(t *testing.T) {
	for _, host := range []string{"127.0.0.1)/db?allowAllFiles=true", "x@evil", "127.0.0.1/foo", "", "[::1]x"} {
		c := &model.DataSourceConfig{Host: host, Port: 3306, Username: "user"}
		if (&MySQLDriver{}).BuildDSN(c) != "" || (&PostgreSQLDriver{}).BuildDSN(c) != "" {
			t.Fatalf("unsafe host accepted")
		}
	}
	c := &model.DataSourceConfig{Host: "localhost", Port: 3306, Username: "user:injected"}
	if (&MySQLDriver{}).BuildDSN(c) != "" {
		t.Fatal("ambiguous MySQL username accepted")
	}
}
func TestMySQLUserDelimitersCannotAlterTarget(t *testing.T) {
	for _, user := range []string{"name@elsewhere", "name/with/slash", "name?x=1#z%"} {
		c := &model.DataSourceConfig{Host: "127.0.0.1", Port: 3306, Username: user, Password: "p@ss:/q+", Database: "db/?x=1"}
		d := (&MySQLDriver{}).BuildDSN(c)
		got, err := mysql.ParseDSN(d)
		if err != nil || got.User != user || got.Passwd != c.Password || got.Addr != "127.0.0.1:3306" || got.DBName != c.Database {
			t.Fatal("MySQL boundary was not preserved")
		}
	}
	c := &model.DataSourceConfig{Host: "127.0.0.1", Port: 3306, Username: "user", Timezone: "UTC&tls=skip-verify"}
	if (&MySQLDriver{}).BuildDSN(c) != "" {
		t.Fatal("invalid timezone accepted")
	}
}
