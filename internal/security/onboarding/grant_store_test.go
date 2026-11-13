package onboarding

import (
	"context"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestGrantStoreExplicitUniqueAndRevocable(t *testing.T) {
	db, e := gorm.Open(sqlite.Open("file:grant-test?mode=memory&cache=shared"), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	if e = MigrateGrants(db); e != nil {
		t.Fatal(e)
	}
	s, e := NewGrantStore(db)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e = s.Provision(ctx, "https://issuer.example", "existing-alice", "existing-alice", "tenant-a"); e != nil {
		t.Fatal(e)
	}
	u, ten, ok, e := s.Resolve(ctx, "https://issuer.example", "existing-alice")
	if e != nil || !ok || u != "existing-alice" || ten != "tenant-a" {
		t.Fatal("approved mapping not resolved")
	}
	if e = s.Provision(ctx, "https://other.example", "existing-alice", "existing-alice", "tenant-b"); e == nil {
		t.Fatal("duplicate owner across issuers allowed")
	}
	if _, _, ok, _ = s.Resolve(ctx, "https://other.example", "existing-alice"); ok {
		t.Fatal("unknown issuer accepted")
	}
	if e = s.Provision(ctx, "https://issuer.example", "bob", "another-bob", "tenant-a"); e == nil {
		t.Fatal("different canonical user accepted")
	}
	if e = s.Provision(ctx, "https://issuer.example", "auth0|AbCd", "auth0|AbCd", "tenant-a"); e != nil {
		t.Fatal("valid provider subject rejected")
	}
	if e = s.Provision(ctx, "https://other.example", "auth0|abcd", "auth0|abcd", "tenant-b"); e == nil {
		t.Fatal("case-insensitive owner collision accepted")
	}
	longSubject := strings.Repeat("a", 255)
	if e = s.Provision(ctx, "https://issuer.example", longSubject, longSubject, "tenant-a"); e != nil {
		t.Fatal("valid maximum subject rejected", e)
	}
	if e = s.Revoke(ctx, "https://issuer.example", "existing-alice"); e != nil {
		t.Fatal(e)
	}
	if _, _, ok, _ = s.Resolve(ctx, "https://issuer.example", "existing-alice"); ok {
		t.Fatal("revocation not enforced")
	}
}
