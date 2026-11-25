package reference

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
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

func TestPublishedReleaseRejectsUnknownTag(t *testing.T) {
	if _, err := PublishedReleaseByTag("latest"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported release rejection, got %v", err)
	}
}

func TestReleaseSourceArchiveArgs(t *testing.T) {
	if args := releaseSourceArchiveArgs(""); args != nil {
		t.Fatalf("expected no archive arguments, got %v", args)
	}
	want := []string{"--release-source-archive", "/tmp/published.tar.gz"}
	if got := releaseSourceArchiveArgs("/tmp/published.tar.gz"); !reflect.DeepEqual(got, want) {
		t.Fatalf("release archive arguments = %v, want %v", got, want)
	}
}

func TestPublishedReleaseRejectsArchiveBeforeExtraction(t *testing.T) {
	for _, tag := range []string{"v2.1.0-preview.2", "v2.1.0-preview.3"} {
		t.Run(tag, func(t *testing.T) {
			release, err := PublishedReleaseByTag(tag)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			archive := filepath.Join(dir, "untrusted.tar.gz")
			if err := os.WriteFile(archive, []byte("not the published archive"), 0600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "extract")
			err = recoveryExtractPublished(context.Background(), archive, target, release)
			if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
				t.Fatalf("expected checksum rejection, got %v", err)
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("extraction target was created on a checksum mismatch: %v", err)
			}
		})
	}
}
