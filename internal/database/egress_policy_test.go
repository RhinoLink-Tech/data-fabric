package database

import (
	"context"
	"errors"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"net"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestFixedIPEgressPolicyDeniesBypass(t *testing.T) {
	p, err := NewFixedIPEgressPolicy([]string{"127.0.0.0/8"}, []int{5432, 3306})
	if err != nil {
		t.Fatal(err)
	}
	source := model.DataSource{ID: "test", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Host: "127.0.0.1", Port: 5432, Username: "synthetic", Password: "synthetic", Database: "demo"}}
	if err := p.Check(&source); err != nil {
		t.Fatal(err)
	}
	source.Config.Password = "p@ss:/?&=中文"
	if err := p.Check(&source); err != nil {
		t.Fatal("special password rejected")
	}
	tests := []struct {
		name   string
		mutate func(*model.DataSource)
	}{
		{"dns name", func(d *model.DataSource) { d.Config.Host = "localhost" }},
		{"ipv6", func(d *model.DataSource) { d.Config.Host = "::1" }},
		{"outside prefix", func(d *model.DataSource) { d.Config.Host = "169.254.169.254" }},
		{"outside port", func(d *model.DataSource) { d.Config.Port = 22 }},
		{"unsupported driver", func(d *model.DataSource) { d.Type = model.DatabaseTypeS3CSV }},
		{"password control", func(d *model.DataSource) { d.Config.Password = "safe\x00password" }},
		{"empty username", func(d *model.DataSource) { d.Config.Username = "" }},
		{"empty database", func(d *model.DataSource) { d.Config.Database = "" }},
		{"database injection", func(d *model.DataSource) { d.Config.Database = "demo?host=169.254.169.254" }},
		{"username injection", func(d *model.DataSource) { d.Config.Username = "user:secret" }},
		{"timezone injection", func(d *model.DataSource) { d.Config.Timezone = "UTC&host=169.254.169.254" }},
		{"tls file", func(d *model.DataSource) { d.Config.SSLRootCert = "/dev/zero" }},
		{"extra driver params", func(d *model.DataSource) {
			d.Config.AdditionalProps = map[string]interface{}{"host": "169.254.169.254"}
		}},
	}
	pool, err := NewRestrictedConnectionPool(nil, nil, nil, true, p)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.CloseAll()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := source
			tt.mutate(&d)
			if !errors.Is(p.Check(&d), ErrEgressDenied) {
				t.Fatal("allowed")
			}
			if _, e := pool.GetConnection(context.Background(), &d); !errors.Is(e, ErrEgressDenied) {
				t.Fatalf("pool: %v", e)
			}
			if _, _, e := pool.GetNativeAPIExecutor(context.Background(), &d); !errors.Is(e, ErrEgressDenied) {
				t.Fatalf("native: %v", e)
			}
			if e := NewHealthChecker(pool).CheckStoredSourceConnectivity(context.Background(), &d); !errors.Is(e, ErrEgressDenied) {
				t.Fatalf("stored probe: %v", e)
			}
			if _, e := NewHealthChecker(pool).CheckDataSourceConnectivity(context.Background(), &d.Config, d.Type); !errors.Is(e, ErrEgressDenied) {
				t.Fatalf("direct probe: %v", e)
			}
		})
	}
	if _, e := NewFixedIPEgressPolicy(nil, []int{5432}); !errors.Is(e, ErrEgressDenied) {
		t.Fatal("empty network accepted")
	}
	if _, e := NewRestrictedConnectionPool(nil, nil, nil, false, nil); !errors.Is(e, ErrEgressDenied) {
		t.Fatal("nil accepted")
	}
}

// Integration test is opt-in against isolated synthetic databases only.
func TestRestrictedPoolAgainstSyntheticDatabases(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		typ       model.DatabaseType
	}{{"postgres", "DF_EGRESS_TEST_POSTGRES", model.DatabaseTypePostgreSQL}, {"mysql", "DF_EGRESS_TEST_MYSQL", model.DatabaseTypeMySQL}} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := os.Getenv(tc.env)
			if endpoint == "" {
				t.Skip("isolated DB not configured")
			}
			host, portStr, err := net.SplitHostPort(endpoint)
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(portStr)
			if err != nil {
				t.Fatal(err)
			}
			p, err := NewFixedIPEgressPolicy([]string{"127.0.0.0/8"}, []int{port})
			if err != nil {
				t.Fatal(err)
			}
			pool, err := NewRestrictedConnectionPool(nil, nil, nil, true, p)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.CloseAll()
			d := &model.DataSource{ID: "synthetic", Type: tc.typ, Config: model.DataSourceConfig{Host: host, Port: port, Username: "synthetic", Password: "synthetic", Database: "synthetic"}}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			db, err := pool.GetConnection(ctx, d)
			if err != nil {
				t.Fatal(err)
			}
			var n int
			if err = db.QueryRowContext(ctx, "SELECT 1").Scan(&n); err != nil || n != 1 {
				t.Fatalf("query %d %v", n, err)
			}
			// Altering a reused source to a blocked IP must never return its pool.
			d.Config.Host = "169.254.169.254"
			if _, err = pool.GetConnection(ctx, d); !errors.Is(err, ErrEgressDenied) {
				t.Fatalf("expected deny: %v", err)
			}
			if e := db.PingContext(ctx); e == nil {
				t.Fatal(fmt.Errorf("cached pool remained usable after denied lease"))
			}
		})
	}
}
