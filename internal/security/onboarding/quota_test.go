package onboarding

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoundedOnboardingQuotaAndIdentity(t *testing.T) {
	a, b, db := setup(t)
	if e := MigrateCompletions(db); e != nil {
		t.Fatal(e)
	}
	if e := MigrateQuota(db); e != nil {
		t.Fatal(e)
	}
	p := Principal{"https://issuer", "alice", "tenant"}
	ctx := context.Background()
	var done atomic.Int32
	var wg sync.WaitGroup
	var id string
	var mu sync.Mutex
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			v, e := s.BeginBounded(ctx, p, 2, 2, 3)
			if e == nil {
				done.Add(1)
				mu.Lock()
				id = v
				mu.Unlock()
			} else if !errors.Is(e, ErrUnavailable) {
				t.Errorf("admission failed: %v", e)
			}
		}(i)
	}
	wg.Wait()
	if done.Load() != 2 {
		t.Fatalf("allowed %d, want 2", done.Load())
	}
	other := Principal{"https://issuer", "bob", "tenant"}
	if _, e := a.BeginBounded(ctx, other, 2, 3, 3); e != nil {
		t.Fatal(e)
	}
	if _, e := a.BeginBounded(ctx, Principal{"iss", "third", "tenant"}, 2, 3, 3); !errors.Is(e, ErrUnavailable) {
		t.Fatal("global quota not enforced")
	}
	if _, _, e := a.Inspect(ctx, id, other); !errors.Is(e, ErrUnavailable) {
		t.Fatal("cross-principal status allowed")
	}
	state, source, e := a.Inspect(ctx, id, p)
	if e != nil || state != "pending" || source != "" {
		t.Fatal("pending missing")
	}
	expected := "34ee7a59-e839-4de8-b278-afc920c54136"
	if _, e = a.ApplyResult(ctx, id, p, func(*gorm.DB) (string, error) { return expected, nil }); e != nil {
		t.Fatal(e)
	}
	state, source, e = a.Inspect(ctx, id, p)
	if e != nil || state != "completed" || source != expected {
		t.Fatal("completed missing")
	}
	if _, e = a.BeginBounded(ctx, p, 2, 2, 3); !errors.Is(e, ErrUnavailable) {
		t.Fatal("completed flow escaped quota")
	}
	db.Model(&record{}).Where("principal_hash <> ?", "").Update("expires_at", time.Now().Add(-time.Hour).UnixMilli())
	db.Model(&completion{}).Where("principal_hash <> ?", "").Update("expires_at", time.Now().Add(-time.Hour).UnixMilli())
	if _, e = a.BeginBounded(ctx, p, 2, 2, 3); e != nil {
		t.Fatal("expired storage not reclaimed", e)
	}
}
func TestQuotaFailClosedForLegacyRowsAndFlowExists(t *testing.T) {
	a, _, db := setup(t)
	if e := MigrateCompletions(db); e != nil {
		t.Fatal(e)
	}
	if e := MigrateQuota(db); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	p := Principal{"issuer", "user", "tenant"}
	id, e := a.BeginBounded(ctx, p, 2, 3, 4)
	if e != nil {
		t.Fatal(e)
	}
	if exists, e := a.Exists(ctx, id); e != nil || !exists {
		t.Fatal("existing flow not found")
	}
	if exists, e := a.Exists(ctx, "invalid"); e != nil || exists {
		t.Fatal("invalid flow exists")
	}
	hash, e := flowHash(id)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Model(&record{}).Where("id_hash = ?", hash).Update("tenant_hash", "").Error; e != nil {
		t.Fatal(e)
	}
	if _, e = a.BeginBounded(ctx, p, 2, 3, 4); !errors.Is(e, ErrUnavailable) {
		t.Fatal("legacy unmapped flow accepted")
	}
}
