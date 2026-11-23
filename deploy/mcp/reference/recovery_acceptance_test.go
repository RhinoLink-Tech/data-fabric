package reference

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRecoveryAcceptanceRejectsNonCI(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	err := RecoveryAcceptance(context.Background(), "", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "isolated GitHub Actions") {
		t.Fatalf("expected non-CI execution to be refused; got %v", err)
	}
}
