package onboarding

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUpdateFlowIsBoundToPrincipalOperationAndSource(t *testing.T) {
	s, _, db := setup(t)
	if err := MigrateCompletions(db); err != nil {
		t.Fatal(err)
	}
	if err := MigrateQuota(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	actor := Principal{"iss", "user", "tenant"}
	target := uuid.NewString()
	other := uuid.NewString()
	id, err := s.BeginBoundedUpdate(ctx, actor, target, 3, 4, 5)
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	apply := func(*gorm.DB) (string, error) { called++; return target, nil }
	if _, err = s.ApplyResult(ctx, id, actor, apply); !errors.Is(err, ErrUnavailable) {
		t.Fatal("create accepted update flow", err)
	}
	if _, _, err = s.Inspect(ctx, id, actor); !errors.Is(err, ErrUnavailable) {
		t.Fatal("create status accepted update flow", err)
	}
	if _, err = s.ApplyUpdateResult(ctx, id, actor, other, apply); !errors.Is(err, ErrUnavailable) {
		t.Fatal("wrong target accepted", err)
	}
	if _, err = s.ApplyUpdateResult(ctx, id, Principal{"iss", "other", "tenant"}, target, apply); !errors.Is(err, ErrUnavailable) {
		t.Fatal("wrong principal accepted", err)
	}
	if called != 0 {
		t.Fatal("unauthorized callback executed")
	}
	if _, err = s.ApplyUpdateResult(ctx, id, actor, target, func(*gorm.DB) (string, error) { return other, nil }); !errors.Is(err, ErrInvalid) {
		t.Fatal("wrong result target accepted", err)
	}
	got, err := s.ApplyUpdateResult(ctx, id, actor, target, apply)
	if err != nil || got != target {
		t.Fatal(got, err)
	}
	got, err = s.ApplyUpdateResult(ctx, id, actor, target, apply)
	if err != nil || got != target || called != 1 {
		t.Fatal("retry repeated or lost result", got, called, err)
	}
	if _, err = s.ApplyResult(ctx, id, actor, apply); !errors.Is(err, ErrUnavailable) {
		t.Fatal("create recovery accepted update", err)
	}
	if _, _, err = s.InspectUpdateTx(ctx, db, id, actor, other); !errors.Is(err, ErrUnavailable) {
		t.Fatal("status accepted wrong target", err)
	}
	state, got, err := s.InspectUpdateTx(ctx, db, id, actor, target)
	if err != nil || state != "completed" || got != target {
		t.Fatal(state, got, err)
	}
}

func TestMySQLUpdateIntentConcurrency(t *testing.T) {
	dsn := os.Getenv("DF_ONBOARDING_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL DSN not set")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("open isolated database")
	}
	for _, m := range []func(*gorm.DB) error{Migrate, MigrateCompletions, MigrateQuota} {
		if err = m(db); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(db, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	actor := Principal{"iss", "concurrent-update", "tenant"}
	target := uuid.NewString()
	id, err := s.BeginBoundedUpdate(ctx, actor, target, 4, 8, 20)
	if err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, e := s.ApplyUpdateResult(ctx, id, actor, target, func(*gorm.DB) (string, error) { count.Add(1); return target, nil })
			if e != nil || got != target {
				failures <- e
			}
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Errorf("failed update recovery: %v", e)
	}
	if count.Load() != 1 {
		t.Fatalf("executed %d updates", count.Load())
	}
}
