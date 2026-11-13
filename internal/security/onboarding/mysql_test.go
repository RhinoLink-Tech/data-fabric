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
	store, _ := New(db, 200*time.Millisecond)
	p := Principal{"issuer", "user", "tenant"}
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
	if err = db.Callback().Delete().Before("gorm:delete").Register("onboarding_test_ready", func(*gorm.DB) {
		select {
		case ready <- struct{}{}:
		default:
		}
	}); err != nil {
		tx.Rollback()
		t.Fatal(err)
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
	time.Sleep(350 * time.Millisecond)
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

func TestMySQLClockIndependentOfSessionTimeZone(t *testing.T) {
	dsn := os.Getenv("DF_ONBOARDING_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL DSN not set")
	}
	open := func(zone string) *gorm.DB {
		db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal("open disposable MySQL database")
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
		if err = db.Exec("SET time_zone = ?", zone).Error; err != nil {
			t.Fatal(err)
		}
		return db
	}
	utc, east := open("+00:00"), open("+08:00")
	first, err := dbMillis(utc)
	if err != nil {
		t.Fatal(err)
	}
	second, err := dbMillis(east)
	if err != nil {
		t.Fatal(err)
	}
	if delta := second - first; delta < -1000 || delta > 1000 {
		t.Fatalf("database clock differs across session time zones by %dms", delta)
	}
}

// Run with a disposable MySQL whose timezone tables include America/New_York.
// The same wall-clock time occurs twice on 2026-11-01; server epoch must advance.
func TestMySQLClockNamedTimeZoneDSTRepeat(t *testing.T) {
	dsn := os.Getenv("DF_ONBOARDING_TEST_MYSQL_DSN")
	if dsn == "" || os.Getenv("DF_ONBOARDING_TEST_MYSQL_TZ") != "1" {
		t.Skip("isolated MySQL with named timezone tables not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("open disposable MySQL database")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = db.Exec("SET time_zone = ?", "America/New_York").Error; err != nil {
		t.Fatal("named timezone unavailable", err)
	}
	var epochs []int64
	var wall []string
	for _, h := range []int{5, 6} {
		expected := time.Date(2026, 11, 1, h, 30, 0, 0, time.UTC).Unix()
		if err = db.Exec("SET timestamp = ?", float64(expected)+0.875).Error; err != nil {
			t.Fatal(err)
		}
		got, err := dbMillis(db)
		if err != nil {
			t.Fatal(err)
		}
		if got != expected*1000+875 {
			t.Fatalf("epoch %d: got %d", expected, got)
		}
		var local string
		if err = db.Raw("SELECT DATE_FORMAT(CURRENT_TIMESTAMP(3), '%Y-%m-%d %H:%i:%s')").Scan(&local).Error; err != nil {
			t.Fatal(err)
		}
		epochs = append(epochs, got)
		wall = append(wall, local)
	}
	if wall[0] != "2026-11-01 01:30:00" || wall[1] != wall[0] {
		t.Fatalf("expected repeated wall time; got %v", wall)
	}
	if epochs[1]-epochs[0] != int64(time.Hour/time.Millisecond) {
		t.Fatalf("expected one hour: %v", epochs)
	}
}
