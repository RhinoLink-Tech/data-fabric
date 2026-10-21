package auditlog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testStore(t *testing.T, secret string, retentionDays int) *Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("second audit migration should be idempotent: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	store, err := NewStore(db, secret, retentionDays)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestFingerprintIsKeyedAndNormalizesWhitespace(t *testing.T) {
	first := testStore(t, "stable deployment secret", 30)
	second := testStore(t, "another deployment secret", 30)
	one := first.Fingerprint("SELECT  id\nFROM orders")
	if one == "" || one != first.Fingerprint("SELECT id FROM orders") {
		t.Fatal("same normalized SQL should have a stable fingerprint")
	}
	if one == second.Fingerprint("SELECT id FROM orders") {
		t.Fatal("fingerprint should be keyed per deployment")
	}
}

func TestStoreListsFiltersAndPrunesEvents(t *testing.T) {
	store := testStore(t, "stable deployment secret", 1)
	now := time.Now().UTC()
	for _, event := range []Event{
		{ActorID: "alice", TenantID: "team-a", Transport: "rest", Operation: "POST /api/v1/fetch", Decision: DecisionAllow, Outcome: "success", OccurredAt: now.Add(-time.Minute)},
		{ActorID: "bob", TenantID: "team-b", Transport: "mcp-http", Operation: "tools/call execute_sql_query", Decision: DecisionDeny, Outcome: "denied", OccurredAt: now.Add(-2 * time.Minute)},
		{ActorID: "alice", TenantID: "team-a", Transport: "rest", Operation: "POST /api/v1/fetch", Decision: DecisionError, Outcome: "failed", OccurredAt: now.Add(-48 * time.Hour)},
	} {
		if err := store.Record(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.List(context.Background(), Filter{TenantID: "team-a", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Events) != 2 {
		t.Fatalf("filtered page = %+v", page)
	}
	if page.Events[0].Decision != DecisionAllow {
		t.Fatalf("events are not newest first: %+v", page.Events)
	}
	restarted, err := NewStore(store.db, "stable deployment secret", 1)
	if err != nil {
		t.Fatal(err)
	}
	reopenedPage, err := restarted.List(context.Background(), Filter{TenantID: "team-a", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if reopenedPage.Total != page.Total {
		t.Fatalf("events after reopening store = %d, want %d", reopenedPage.Total, page.Total)
	}
	deleted, err := store.PruneExpired(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("PruneExpired() deleted %d events, want 1", deleted)
	}
}

func TestEventJSONContainsNoRawQueryOrBindValues(t *testing.T) {
	event := Event{QueryFingerprint: strings.Repeat("a", 64), ActorID: "alice", Transport: "rest", Operation: "POST /api/v1/fetch", Decision: DecisionAllow, Outcome: "success"}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "SELECT") || strings.Contains(string(encoded), "password") {
		t.Fatalf("audit event unexpectedly contains raw query or secret: %s", encoded)
	}
}
