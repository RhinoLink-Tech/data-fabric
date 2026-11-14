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
		if err != nil || cfg.Addr != "[fe80::1%eth0]:3306" || cfg.TLSConfig != "true" {
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
