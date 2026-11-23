package reference

import (
	"context"
	"io"
	"os"
	"path/filepath"
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

func TestPreview2RejectsArchiveBeforeExtraction(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "untrusted.tar.gz")
	if err := os.WriteFile(archive, []byte("not the published archive"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "extract")
	err := recoveryExtractPreview2(context.Background(), archive, target)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum rejection, got %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("extraction target was created on a checksum mismatch: %v", err)
	}
}
