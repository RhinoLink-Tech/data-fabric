package replicarouting

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestOwnerRoutingMarker(t *testing.T) {
	keys := fmt.Sprintf(`{"old":"%s","new":"%s"}`,
		base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 32))),
		base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32))))
	ring, err := ParseKeyRing("site", keys)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := ring.NewOwner("old", REST, "replica-a")
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := ring.NewOwner("new", REST, "replica-a")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 6, 1, 0, 0, 0, time.UTC)
	marker, err := owner.Mark("query-id", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if slot, err := ring.Verify(marker, REST, "query-id", now); err != nil || slot != "replica-a" {
		t.Fatalf("ingress got slot %q with error %v", slot, err)
	}
	if err := owner.Verify(marker, "query-id", now); err != nil {
		t.Fatal(err)
	}
	if err := replacement.Verify(marker, "query-id", now); err == nil {
		t.Fatal("replacement accepted previous boot")
	}
	for _, tc := range []struct {
		name   string
		marker string
		pool   string
		id     string
		at     time.Time
	}{
		{"altered slot", strings.Replace(marker, "replica-a", "replica-b", 1), REST, "query-id", now},
		{"changed key ID", strings.Replace(marker, ".old.", ".new.", 1), REST, "query-id", now},
		{"changed query ID", marker, REST, "query-other", now},
		{"different transport", marker, MCP, "query-id", now},
		{"expired", marker, REST, "query-id", now.Add(time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if slot, err := ring.Verify(tc.marker, tc.pool, tc.id, tc.at); err == nil || slot != "" {
				t.Fatalf("ingress got slot %q with error %v", slot, err)
			}
		})
	}
}
