package table_formats

import (
	"context"
	"strings"
	"testing"
)

func TestDeltaVacuumReturnsUnsupportedForBothModes(t *testing.T) {
	driver := &DeltaDriver{}

	for _, dryRun := range []bool{true, false} {
		name := "execute"
		if dryRun {
			name = "dry run"
		}
		t.Run(name, func(t *testing.T) {
			result, err := driver.Vacuum(context.Background(), "table", 168, dryRun)
			if err == nil || !strings.Contains(err.Error(), "not supported") {
				t.Fatalf("Vacuum error = %v, want explicit unsupported error", err)
			}
			if result != nil {
				t.Fatalf("Vacuum result = %#v, want nil when operation is unsupported", result)
			}
		})
	}
}
