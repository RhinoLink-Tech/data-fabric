package database

import (
	"context"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"testing"
	"time"
)

func TestBoundedOpenGaussUsesRestrictedSQLPathOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		policy, err := NewFixedIPEgressPolicyWithSourceOptions([]string{"192.0.2.0/24"}, []int{5432}, "", false, enabled)
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
		source := &model.DataSource{ID: "synthetic", Type: model.DatabaseTypeOpenGauss}
		executor, supported, err := pool.GetNativeAPIExecutor(context.Background(), source)
		if enabled {
			if err != nil || supported || executor != nil {
				t.Fatalf("enabled OpenGauss did not select bounded SQL path: %v", err)
			}
		} else if err == nil || supported || executor != nil {
			t.Fatal("disabled OpenGauss bypassed the restricted policy")
		}
		source.Type = model.DatabaseTypeTiDB
		if executor, supported, err = pool.GetNativeAPIExecutor(context.Background(), source); err == nil || supported || executor != nil {
			t.Fatal("unapproved native type accepted")
		}
	}
}

func TestOpenGaussPolicyIsExplicitAndRequiresTLS(t *testing.T) {
	disabled, err := NewFixedIPEgressPolicyWithSourceOptions([]string{"192.0.2.0/24"}, []int{5432}, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := NewFixedIPEgressPolicyWithSourceOptions([]string{"192.0.2.0/24"}, []int{5432}, "", false, true)
	if err != nil {
		t.Fatal(err)
	}
	source := &model.DataSource{Type: model.DatabaseTypeOpenGauss, Config: model.DataSourceConfig{Host: "192.0.2.1", Port: 5432, Database: "orders", Username: "reader", Password: "synthetic", SSL: true, TLSServerName: "db.example.test", TLSCA: "system"}}
	if disabled.AllowsSourceType(source.Type) || disabled.Check(source) == nil {
		t.Fatal("openGauss enabled without explicit opt-in")
	}
	if enabled.Check(source) != nil {
		t.Fatal("explicit verified TLS source rejected")
	}
	source.Config.SSL = false
	if enabled.Check(source) == nil {
		t.Fatal("plaintext openGauss source accepted")
	}
	source.Config.SSL = true
	source.Config.Host = "169.254.169.254"
	if enabled.Check(source) == nil {
		t.Fatal("unlisted address accepted")
	}
	source.Config.Host = "192.0.2.1"
	source.Config.Port = 5433
	if enabled.Check(source) == nil {
		t.Fatal("unlisted port accepted")
	}
}
