package database

import (
	"context"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"testing"
	"time"
)

func TestBoundedTiDBUsesRestrictedSQLPathOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		policy, err := NewFixedIPEgressPolicyWithTiDB([]string{"192.0.2.0/24"}, []int{4000}, "", false, false, enabled)
		if err != nil {
			t.Fatal(err)
		}
		pool, err := NewRestrictedConnectionPool(nil, nil, nil, true, policy)
		if err != nil {
			t.Fatal(err)
		}
		if err = pool.SetBoundedSourceFreshness(time.Second, func(context.Context, *model.DataSource) error { return nil }); err != nil {
			t.Fatal(err)
		}
		defer pool.Stop()
		source := &model.DataSource{ID: "synthetic", Type: model.DatabaseTypeTiDB}
		executor, supported, err := pool.GetNativeAPIExecutor(context.Background(), source)
		if enabled {
			if err != nil || supported || executor != nil {
				t.Fatalf("enabled TiDB did not select bounded SQL path: %v", err)
			}
		} else if err == nil || supported || executor != nil {
			t.Fatal("disabled TiDB bypassed the restricted policy")
		}
		source.Type = model.DatabaseTypeMariaDB
		if executor, supported, err = pool.GetNativeAPIExecutor(context.Background(), source); err == nil || supported || executor != nil {
			t.Fatal("unapproved native type accepted")
		}
	}
}

func TestTiDBPolicyIsExplicitAndRequiresTLS(t *testing.T) {
	disabled, err := NewFixedIPEgressPolicyWithTiDB([]string{"192.0.2.0/24"}, []int{4000}, "", false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := NewFixedIPEgressPolicyWithTiDB([]string{"192.0.2.0/24"}, []int{4000}, "", false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	source := &model.DataSource{Type: model.DatabaseTypeTiDB, Config: model.DataSourceConfig{Host: "192.0.2.1", Port: 4000, Database: "orders", Username: "reader", Password: "synthetic", SSL: true, TLSServerName: "db.example.test", TLSCA: "system"}}
	if disabled.AllowsSourceType(source.Type) || disabled.Check(source) == nil {
		t.Fatal("TiDB enabled without explicit opt-in")
	}
	if enabled.Check(source) != nil {
		t.Fatal("explicit verified TLS source rejected")
	}
	source.Config.SSL = false
	if enabled.Check(source) == nil {
		t.Fatal("plaintext TiDB source accepted")
	}
	source.Config.SSL = true
	source.Config.Host = "169.254.169.254"
	if enabled.Check(source) == nil {
		t.Fatal("unlisted address accepted")
	}
	source.Config.Host = "192.0.2.1"
	source.Config.Port = 4001
	if enabled.Check(source) == nil {
		t.Fatal("unlisted port accepted")
	}
}
