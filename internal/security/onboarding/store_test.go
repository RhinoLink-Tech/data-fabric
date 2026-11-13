package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type effect struct {
	ID string `gorm:"primaryKey"`
}

func setup(t *testing.T) (*Store, *Store, *gorm.DB) {
	t.Helper()
	// WAL and busy timeout make independent SQLite connections serialize writers.
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", filepath.Join(t.TempDir(), "flows.db"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&effect{}); err != nil {
		t.Fatal(err)
	}
	a, _ := New(db, time.Minute)
	b, _ := New(db, time.Minute)
	return a, b, db
}
func TestSharedFlowIdentityAndReplay(t *testing.T) {
	a, b, db := setup(t)
	ctx := context.Background()
	principal := Principal{"https://issuer", "alice", "tenant"}
	id, err := a.Begin(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []Principal{{"https://other", "alice", "tenant"}, {"https://issuer", "bob", "tenant"}, {"https://issuer", "alice", "other"}, {"https://issuer", "alice ", "tenant"}} {
		if err = b.ConsumeAndApply(ctx, id, wrong, func(*gorm.DB) error { t.Fatal("unauthorized callback"); return nil }); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("expected denial: %v", err)
		}
	}
	if err = b.ConsumeAndApply(ctx, id, principal, func(tx *gorm.DB) error { return tx.Create(&effect{ID: "ok"}).Error }); err != nil {
		t.Fatal(err)
	}
	if err = a.ConsumeAndApply(ctx, id, principal, func(*gorm.DB) error { return nil }); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("replay: %v", err)
	}
	var n int64
	db.Model(&effect{}).Count(&n)
	if n != 1 {
		t.Fatalf("effects: %d", n)
	}
}
func TestRollbackKeepsFlowAndRevertsMutation(t *testing.T) {
	a, _, db := setup(t)
	ctx := context.Background()
	p := Principal{"iss", "user", "tenant"}
	id, _ := a.Begin(ctx, p)
	fail := errors.New("fail")
	err := a.ConsumeAndApply(ctx, id, p, func(tx *gorm.DB) error {
		if e := tx.Create(&effect{ID: "rolled-back"}).Error; e != nil {
			return e
		}
		return fail
	})
	if !errors.Is(err, fail) {
		t.Fatalf("callback error: %v", err)
	}
	var n int64
	db.Model(&effect{}).Count(&n)
	if n != 0 {
		t.Fatal("mutation survived rollback")
	}
	if err = a.ConsumeAndApply(ctx, id, p, func(tx *gorm.DB) error { return tx.Create(&effect{ID: "committed"}).Error }); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentConsumeAndExpiry(t *testing.T) {
	a, b, db := setup(t)
	ctx := context.Background()
	p := Principal{"iss", "user", "tenant"}
	id, _ := a.Begin(ctx, p)
	var won atomic.Int32
	var wg sync.WaitGroup
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			err := s.ConsumeAndApply(ctx, id, p, func(tx *gorm.DB) error { won.Add(1); return tx.Create(&effect{ID: "one"}).Error })
			if err != nil && !errors.Is(err, ErrUnavailable) {
				failures <- err
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("unexpected db error: %v", err)
	}
	if won.Load() != 1 {
		t.Fatalf("callbacks=%d", won.Load())
	}
	expired, _ := a.Begin(ctx, p)
	now, err := dbMillis(db)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(expired))
	if err := db.Model(&record{}).Where("id_hash = ?", hex.EncodeToString(hash[:])).Update("expires_at", now-1).Error; err != nil {
		t.Fatal(err)
	}
	if err := b.ConsumeAndApply(ctx, expired, p, func(*gorm.DB) error { return nil }); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired: %v", err)
	}
	if n, err := b.PurgeExpired(ctx); err != nil || n != 1 {
		t.Fatalf("purge %d %v", n, err)
	}
}
func TestInvalidAndPrincipalBoundaries(t *testing.T) {
	a, _, _ := setup(t)
	ctx := context.Background()
	if _, err := a.Begin(ctx, Principal{"iss", "user", " "}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	a1, _ := (Principal{"ab", "c", "tenant"}).digest()
	a2, _ := (Principal{"a", "bc", "tenant"}).digest()
	if a1 == a2 {
		t.Fatal("ambiguous identity")
	}
	if err := a.ConsumeAndApply(ctx, "invalid", Principal{"iss", "user", "tenant"}, func(*gorm.DB) error { return nil }); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
