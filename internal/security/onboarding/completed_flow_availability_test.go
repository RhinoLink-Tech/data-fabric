package onboarding

import (
	"context"
	"errors"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"testing"
	"time"
)

func TestCompletedSetupsDoNotBlockAnotherUpdate(t *testing.T) {
	s, _, db := setup(t)
	if err := MigrateCompletions(db); err != nil {
		t.Fatal(err)
	}
	if err := MigrateQuota(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p := Principal{"https://issuer", "alice", "tenant"}
	const source = "34ee7a59-e839-4de8-b278-afc920c54136"
	for i := 0; i < 4; i++ {
		id, err := s.BeginBounded(ctx, p, 4, 64, 4096)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ApplyResult(ctx, id, p, func(*gorm.DB) (string, error) { return source, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.BeginBoundedUpdate(ctx, p, source, 4, 64, 4096); err != nil {
		t.Fatal("four completed setups must not block an update", err)
	}
}

func TestPendingAndCompletionLimitsRemainBounded(t *testing.T) {
	s, _, db := setup(t)
	if err := MigrateCompletions(db); err != nil {
		t.Fatal(err)
	}
	if err := MigrateQuota(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p := Principal{"https://issuer", "alice", "tenant"}
	const source = "34ee7a59-e839-4de8-b278-afc920c54136"
	var last string
	for i := 0; i < 12; i++ {
		id, err := s.BeginBounded(ctx, p, 4, 64, 4096)
		if err != nil {
			t.Fatalf("completion %d: %v", i, err)
		}
		if _, err = s.ApplyResult(ctx, id, p, func(*gorm.DB) (string, error) { return source, nil }); err != nil {
			t.Fatal(err)
		}
		last = id
	}
	var pending []string
	for i := 0; i < 4; i++ {
		id, err := s.BeginBounded(ctx, p, 4, 64, 4096)
		if err != nil {
			t.Fatalf("pending %d: %v", i, err)
		}
		pending = append(pending, id)
	}
	if _, err := s.BeginBoundedUpdate(ctx, p, source, 4, 64, 4096); !errors.Is(err, ErrUnavailable) {
		t.Fatal("pending limit must deny")
	}
	if _, err := s.ApplyResult(ctx, pending[0], p, func(*gorm.DB) (string, error) { return source, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginBoundedUpdate(ctx, p, source, 4, 64, 4096); !errors.Is(err, ErrUnavailable) {
		t.Fatal("completion storage must remain bounded")
	}
	state, got, err := s.Inspect(ctx, last, p)
	if err != nil || state != "completed" || got != source {
		t.Fatal("completion receipt was not retained")
	}
}

// Run only against a disposable MySQL database.
func TestMySQLCompletedSetupsAllowUpdate(t *testing.T) {
	dsn := os.Getenv("DF_ONBOARDING_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("disposable MySQL DSN not set")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("open disposable MySQL")
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = MigrateCompletions(db); err != nil {
		t.Fatal(err)
	}
	if err = MigrateQuota(db); err != nil {
		t.Fatal(err)
	}
	s, err := New(db, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p := Principal{"https://issuer", "mysql-user", "tenant"}
	const source = "34ee7a59-e839-4de8-b278-afc920c54136"
	var last string
	for i := 0; i < 4; i++ {
		id, err := s.BeginBounded(ctx, p, 4, 64, 4096)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ApplyResult(ctx, id, p, func(*gorm.DB) (string, error) { return source, nil }); err != nil {
			t.Fatal(err)
		}
		last = id
	}
	if _, err = s.BeginBoundedUpdate(ctx, p, source, 4, 64, 4096); err != nil {
		t.Fatal("completed operations blocked update", err)
	}
	state, got, err := s.Inspect(ctx, last, p)
	if err != nil || state != "completed" || got != source {
		t.Fatal("completion recovery was not retained")
	}
	// Four receipts and one pending update already occupy five slots.
	for i := 0; i < 11; i++ {
		id, err := s.BeginBounded(ctx, p, 4, 64, 4096)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ApplyResult(ctx, id, p, func(*gorm.DB) (string, error) { return source, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.BeginBounded(ctx, p, 4, 64, 4096); !errors.Is(err, ErrUnavailable) {
		t.Fatal("principal storage limit not enforced")
	}
	other := Principal{"https://issuer", "another-user", "tenant"}
	if _, err := s.BeginBounded(ctx, other, 4, 64, 4096); err != nil {
		t.Fatal("second principal was starved", err)
	}
}

func TestCompletedReceiptsPreserveOtherPrincipalCapacity(t *testing.T) {
	s, _, db := setup(t)
	if err := MigrateCompletions(db); err != nil {
		t.Fatal(err)
	}
	if err := MigrateQuota(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a := Principal{"https://issuer", "alice", "shared-tenant"}
	b := Principal{"https://issuer", "bob", "shared-tenant"}
	const source = "34ee7a59-e839-4de8-b278-afc920c54136"
	var first string
	for i := 0; i < 16; i++ {
		id, err := s.BeginBounded(ctx, a, 4, 64, 4096)
		if err != nil {
			t.Fatalf("completion %d: %v", i, err)
		}
		if i == 0 {
			first = id
		}
		if _, err = s.ApplyResult(ctx, id, a, func(*gorm.DB) (string, error) { return source, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.BeginBounded(ctx, a, 4, 64, 4096); !errors.Is(err, ErrUnavailable) {
		t.Fatal("principal storage limit not enforced")
	}
	if _, err := s.BeginBounded(ctx, b, 4, 64, 4096); err != nil {
		t.Fatal("second principal starved", err)
	}
	state, got, err := s.Inspect(ctx, first, a)
	if err != nil || state != "completed" || got != source {
		t.Fatal("completion retry not preserved")
	}
}
