//go:build !datafabric_core

package database

import (
	"context"
	"errors"
	"testing"
	"time"

	clickhouse "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestRestrictedClickHousePinsAddressAndTLSIdentity(t *testing.T) {
	cfg := model.DataSourceConfig{Host: "192.0.2.7", Port: 9440, Database: "orders", Username: "reader", Password: "synthetic", SSL: true, TLSServerName: "db.example.test"}
	opts, err := restrictedClickHouseOptions(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Protocol != clickhouse.Native || len(opts.Addr) != 1 || opts.Addr[0] != "192.0.2.7:9440" ||
		opts.TLS == nil || opts.TLS.ServerName != "db.example.test" || opts.TLS.InsecureSkipVerify ||
		opts.Auth.Username != "reader" || opts.Auth.Database != "orders" || opts.DialTimeout <= 0 || opts.ReadTimeout <= 0 {
		t.Fatalf("restricted connector options do not preserve pinning or verification")
	}
	for _, change := range []func(*model.DataSourceConfig){
		func(c *model.DataSourceConfig) { c.Host = "db.example.test" },
		func(c *model.DataSourceConfig) { c.Host = "::1" },
		func(c *model.DataSourceConfig) { c.SSL = false },
		func(c *model.DataSourceConfig) { c.TLSServerName = "" },
		func(c *model.DataSourceConfig) { c.TLSCA = "custom" },
		func(c *model.DataSourceConfig) { c.Port = 0 },
	} {
		bad := cfg
		change(&bad)
		if _, err := restrictedClickHouseOptions(bad, nil); !errors.Is(err, ErrEgressDenied) {
			t.Fatal("invalid restricted configuration accepted")
		}
	}
}

func TestClickHouseRestrictedPolicyIsExplicitAndTLSOnly(t *testing.T) {
	disabled, err := NewFixedIPEgressPolicyWithClickHouse([]string{"192.0.2.0/24"}, []int{9440}, "", false, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := NewFixedIPEgressPolicyWithClickHouse([]string{"192.0.2.0/24"}, []int{9440}, "", false, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.AllowsSourceType(model.DatabaseTypeClickHouse) || !enabled.AllowsSourceType(model.DatabaseTypeClickHouse) {
		t.Fatal("ClickHouse gate is not explicit")
	}
	source := &model.DataSource{Type: model.DatabaseTypeClickHouse, Config: model.DataSourceConfig{
		Host: "192.0.2.7", Port: 9440, Database: "orders", Username: "reader", Password: "synthetic", SSL: true, TLSServerName: "db.example.test",
	}}
	if err := enabled.Check(source); err != nil {
		t.Fatalf("allowed source: %v", err)
	}
	if err := disabled.Check(source); !errors.Is(err, ErrEgressDenied) {
		t.Fatal("disabled source admitted")
	}
	for _, change := range []func(*model.DataSource){
		func(s *model.DataSource) { s.Config.Host = "198.51.100.1" },
		func(s *model.DataSource) { s.Config.Port = 9000 },
		func(s *model.DataSource) { s.Config.SSL = false },
		func(s *model.DataSource) { s.Config.TLSServerName = "" },
	} {
		bad := *source
		change(&bad)
		if err := enabled.Check(&bad); !errors.Is(err, ErrEgressDenied) {
			t.Fatal("unsafe source admitted")
		}
	}
}

// ClickHouse supplies a database/sql driver, not the optional native API
// executor extension. A bounded browser pool must therefore preserve the
// controlled SQL path instead of treating every SQL-capable driver as native.
func TestBoundedClickHouseUsesRestrictedSQLPath(t *testing.T) {
	policy, err := NewFixedIPEgressPolicyWithClickHouse([]string{"192.0.2.0/24"}, []int{9440}, "", false, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := NewRestrictedConnectionPool(nil, nil, nil, true, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Stop()
	if err = pool.SetBoundedSourceFreshness(time.Second, func(context.Context, *model.DataSource) error { return nil }); err != nil {
		t.Fatal(err)
	}
	executor, supported, err := pool.GetNativeSQLAPIExecutor(context.Background(), &model.DataSource{ID: "synthetic", Type: model.DatabaseTypeClickHouse})
	if err != nil || supported || executor != nil {
		t.Fatalf("GetNativeSQLAPIExecutor() = %#v, %t, %v; want ordinary SQL fallback", executor, supported, err)
	}
}
