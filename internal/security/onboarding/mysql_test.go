package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// Supply only an isolated, disposable database: this test creates tables.
func TestMySQLSharedConsumption(t *testing.T) {
	dsn := os.Getenv("DF_ONBOARDING_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL DSN not set")
	}
	open := func() *gorm.DB {
		db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal("open disposable MySQL database")
		}
		return db
	}
	db1, db2 := open(), open()
	if err := Migrate(db1); err != nil {
		t.Fatal(err)
	}
	if err := db1.AutoMigrate(&effect{}); err != nil {
		t.Fatal(err)
	}
	a, _ := New(db1, time.Minute)
	b, _ := New(db2, time.Minute)
	p := Principal{"iss", "mysql-user", "tenant"}
	ctx := context.Background()
	id, err := a.Begin(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	var done atomic.Int32
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			err := s.ConsumeAndApply(ctx, id, p, func(tx *gorm.DB) error { done.Add(1); return tx.Create(&effect{ID: id}).Error })
			if err != nil && !errors.Is(err, ErrUnavailable) {
				failures <- err
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("database consume error: %v", err)
	}
	if done.Load() != 1 {
		t.Fatalf("callbacks=%d", done.Load())
	}
}

func TestMySQLExpiryWhileWaitingForLock(t *testing.T) {
	dsn := os.Getenv("DF_ONBOARDING_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL DSN not set")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("open disposable MySQL database")
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	store, _ := New(db, time.Minute)
	p := Principal{"issuer", "user", "tenant"}
	start := time.Now()
	var now atomic.Int64
	now.Store(start.UnixMilli())
	store.now = func() time.Time { return time.UnixMilli(now.Load()) }
	id, err := store.Begin(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(id))
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	var row record
	if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id_hash = ?", hex.EncodeToString(hash[:])).First(&row).Error; err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	ready := make(chan struct{}, 1)
	store.now = func() time.Time {
		select {
		case ready <- struct{}{}:
		default:
		}
		return time.UnixMilli(now.Load())
	}
	finished := make(chan error, 1)
	var called atomic.Bool
	go func() {
		finished <- store.ConsumeAndApply(context.Background(), id, p, func(*gorm.DB) error { called.Store(true); return nil })
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		tx.Rollback()
		t.Fatal("consumer did not start")
	}
	now.Store(start.Add(2 * time.Minute).UnixMilli())
	if err = tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("expired while waiting: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("consumer did not finish")
	}
	if called.Load() {
		t.Fatal("expired callback ran")
	}
}
