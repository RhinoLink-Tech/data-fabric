package database

import (
	"context"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"testing"
	"time"
)

func TestBoundedMariaDBUsesRestrictedSQLPathOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		policy, err := NewFixedIPEgressPolicyWithCAAndMariaDB([]string{"192.0.2.0/24"}, []int{3306}, "", enabled)
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
		source := &model.DataSource{ID: "synthetic", Type: model.DatabaseTypeMariaDB}
		executor, supported, err := pool.GetNativeAPIExecutor(context.Background(), source)
		if enabled {
			if err != nil || supported || executor != nil {
				t.Fatalf("enabled MariaDB did not select bounded SQL path: %v", err)
			}
		} else if err == nil || supported || executor != nil {
			t.Fatal("disabled MariaDB bypassed the restricted policy")
		}
		source.Type = model.DatabaseTypeTiDB
		if executor, supported, err = pool.GetNativeAPIExecutor(context.Background(), source); err == nil || supported || executor != nil {
			t.Fatal("unapproved native type accepted")
		}
	}
}
