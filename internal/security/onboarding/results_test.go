package onboarding

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestApplyResultRecoveryConcurrencyAndIsolation(t *testing.T) {
	a, b, db := setup(t)
	if err := MigrateCompletions(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p := Principal{"iss", "user", "tenant"}
	id, err := a.Begin(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	source := uuid.NewString()
	var executions atomic.Int32
	apply := func(tx *gorm.DB) (string, error) {
		executions.Add(1)
		return source, tx.Create(&effect{ID: source}).Error
	}
	const n = 12
	var wg sync.WaitGroup
	results := make(chan string, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 > 0 {
				s = b
			}
			x, e := s.ApplyResult(ctx, id, p, apply)
			if e != nil {
				errs <- e
			} else {
				results <- x
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	close(results)
	for e := range errs {
		t.Errorf("concurrent error %v", e)
	}
	for x := range results {
		if x != source {
			t.Fatal("wrong result")
		}
	}
	if executions.Load() != 1 {
		t.Fatalf("executions=%d", executions.Load())
	}
	if _, err = b.ApplyResult(ctx, id, Principal{"iss", "other", "tenant"}, apply); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("identity mismatch: %v", err)
	}
	var count int64
	if err = db.Model(&effect{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("effects %d %v", count, err)
	}
	hash, _ := flowHash(id)
	now, _ := dbMillis(db)
	if err = db.Model(&completion{}).Where("id_hash = ?", hash).Update("expires_at", now-1).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = b.ApplyResult(ctx, id, p, apply); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired recovery: %v", err)
	}
	if deleted, err := b.PurgeCompletions(ctx, 1); err != nil || deleted != 1 {
		t.Fatalf("purge: %d %v", deleted, err)
	}
}
func TestApplyResultFailureRollsBack(t *testing.T) {
	a, _, db := setup(t)
	if err := MigrateCompletions(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p := Principal{"iss", "user", "tenant"}
	id, _ := a.Begin(ctx, p)
	fail := errors.New("failed")
	_, err := a.ApplyResult(ctx, id, p, func(tx *gorm.DB) (string, error) { _ = tx.Create(&effect{ID: "rollback"}).Error; return "", fail })
	if !errors.Is(err, fail) {
		t.Fatal(err)
	}
	var n int64
	db.Model(&effect{}).Count(&n)
	if n != 0 {
		t.Fatal("partial effect")
	}
	source := uuid.NewString()
	got, err := a.ApplyResult(ctx, id, p, func(tx *gorm.DB) (string, error) { return source, tx.Create(&effect{ID: source}).Error })
	if err != nil || got != source {
		t.Fatalf("retry: %v", err)
	}
}
