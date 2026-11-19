package warehouses

import (
	"errors"
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
