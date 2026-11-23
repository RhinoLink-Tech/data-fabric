//go:build !datafabric_core

package database

import (
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestDaMengConnectionPoolPaginationPreservesLineComment(t *testing.T) {
	pool := NewConnectionPool(nil, nil, nil, false)
	for _, offset := range []int64{0, 100} {
		got, err := pool.ApplyBatchPagination(model.DatabaseTypeDaMeng, "SELECT id FROM orders -- tail\n;", 100, offset)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "-- tail\n") || !strings.Contains(got, "FETCH FIRST 100 ROWS ONLY") {
			t.Fatalf("pagination was swallowed by a comment: %q", got)
		}
	}
}
