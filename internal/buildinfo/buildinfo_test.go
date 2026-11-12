package buildinfo

import "testing"

func TestUnversionedBuildIdentity(t *testing.T) {
	if Version != "development" || SourceRevision != "unknown" {
		t.Fatalf("unversioned identity = %q/%q", Version, SourceRevision)
	}
}
