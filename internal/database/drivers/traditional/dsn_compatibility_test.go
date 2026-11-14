package traditional

import (
	"net/url"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
)

func TestScopedIPv6HostsPreserveZoneAndDSNBoundaries(t *testing.T) {
	for _, input := range []string{"fe80::1%eth0", "[fe80::1%eth0]", "[fe80::1%25eth0]"} {
		c := &model.DataSourceConfig{Host: input, Port: 5432, Username: "reader", Password: "synthetic:/?", Database: "orders", SSL: true}
		u, err := url.Parse((&PostgreSQLDriver{}).BuildDSN(c))
		if err != nil || u.Hostname() != "fe80::1%eth0" || u.Port() != "5432" || u.Query().Get("sslmode") != "require" {
			t.Fatalf("postgres scoped host rejected or changed: %q %v", input, err)
		}
		pg, err := pq.NewConfig((&PostgreSQLDriver{}).BuildDSN(c))
		if err != nil || pg.Host != "fe80::1%eth0" {
			t.Fatalf("postgres driver cannot parse scoped host %q: %v", input, err)
		}
		c.Port = 3306
		cfg, err := mysql.ParseDSN((&MySQLDriver{}).BuildDSN(c))
		if err != nil || cfg.Addr != mysqlTarget(input) || cfg.TLSConfig != "true" {
			t.Fatalf("mysql scoped host rejected or changed: %q %v", input, err)
		}
	}
	for _, bad := range []string{"fe80::1%eth0/path", "[fe80::1%eth0?tls=false]", "fe80::1%eth0@remote"} {
		c := &model.DataSourceConfig{Host: bad, Port: 5432}
		if (&PostgreSQLDriver{}).BuildDSN(c) != "" || (&MySQLDriver{}).BuildDSN(c) != "" {
			t.Fatalf("invalid zone accepted: %q", bad)
		}
	}
}

func mysqlTarget(input string) string {
	if input == "[fe80::1%25eth0]" {
		return "[fe80::1%25eth0]:3306"
	}
	return "[fe80::1%eth0]:3306"
}
func TestIDNHostAndZoneBeginningWith25(t *testing.T) {
	for _, host := range []string{"数据库.example", "xn--dxty1ibyb.example"} {
		c := &model.DataSourceConfig{Host: host, Port: 5432, Username: "reader", Password: "synthetic", Database: "orders"}
		u, err := url.Parse((&PostgreSQLDriver{}).BuildDSN(c))
		if err != nil || u.Hostname() != "xn--dxty1ibyb.example" {
			t.Fatalf("postgres IDN did not resolve to expected ASCII host: %v", err)
		}
		pg, err := pq.NewConfig((&PostgreSQLDriver{}).BuildDSN(c))
		if err != nil || pg.Host != "xn--dxty1ibyb.example" {
			t.Fatalf("postgres IDN driver parsing: %v", err)
		}
		c.Port = 3306
		cfg, err := mysql.ParseDSN((&MySQLDriver{}).BuildDSN(c))
		if err != nil || cfg.Addr != "xn--dxty1ibyb.example:3306" {
			t.Fatalf("mysql IDN: %+v %v", cfg, err)
		}
	}
	c := &model.DataSourceConfig{Host: "fe80::1%25prod", Port: 5432}
	u, err := url.Parse((&PostgreSQLDriver{}).BuildDSN(c))
	if err != nil || u.Hostname() != "fe80::1%25prod" {
		t.Fatalf("unbracketed postgres zone: %v", err)
	}
	c.Host = "[fe80::1%25prod]"
	c.Port = 3306
	cfg, err := mysql.ParseDSN((&MySQLDriver{}).BuildDSN(c))
	if err != nil || cfg.Addr != "[fe80::1%25prod]:3306" {
		t.Fatalf("mysql zone: %v", err)
	}
	for _, bad := range []string{"数据@evil.example", "数据库.example/other", "数据库.example?tls=false"} {
		c.Host = bad
		if (&MySQLDriver{}).BuildDSN(c) != "" || (&PostgreSQLDriver{}).BuildDSN(c) != "" {
			t.Fatalf("unsafe IDN accepted")
		}
	}
}
