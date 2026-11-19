package warehouses

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestMaxComputeDoesNotCreatePlaceholderQueryConnection(t *testing.T) {
	driver := NewMaxComputeDriver()
	for _, dsn := range []string{"maxcompute://user:synthetic-secret@example.invalid:80?project=p", "invalid"} {
		db, err := driver.Open(dsn)
		if db != nil || !errors.Is(err, ErrMaxComputeQueryUnavailable) {
			t.Fatalf("Open must fail before submitting a task, db=%v err=%v", db, err)
		}
		if strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatal("error exposed credentials")
		}
	}
	if err := driver.TestConnection(nil); !errors.Is(err, ErrMaxComputeQueryUnavailable) {
		t.Fatalf("TestConnection() = %v, want unavailable", err)
	}
	caps := driver.GetCapabilities()
	if caps.SupportsSQL || caps.SupportsSchemaDiscovery {
		t.Fatalf("unimplemented query capabilities advertised: %+v", caps)
	}
}

func TestMaxComputeBatchPaginationRejectsOffsets(t *testing.T) {
	driver := NewMaxComputeDriver()
	got, err := driver.ApplyBatchPagination("SELECT id FROM orders", 100, 0)
	if err != nil || got != "SELECT id FROM orders LIMIT 100" {
		t.Fatalf("first batch = %q, %v", got, err)
	}
	if got, err := driver.ApplyBatchPagination("SELECT id FROM orders", 100, 100); !errors.Is(err, ErrMaxComputeOffsetUnsupported) {
		t.Fatalf("second batch = %q, %v; want offset unsupported", got, err)
	}
	for _, bounds := range [][2]int64{{0, 0}, {-1, 0}, {100, -1}, {1, math.MaxInt64}} {
		if got, err := driver.ApplyBatchPagination("SELECT id FROM orders", bounds[0], bounds[1]); err == nil {
			t.Fatalf("ApplyBatchPagination(%d, %d) = %q, want error", bounds[0], bounds[1], got)
		}
	}
}
