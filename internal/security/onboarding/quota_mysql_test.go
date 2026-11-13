package onboarding

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMySQLBoundedQuotaAcrossReplicas(t *testing.T) {
	dsn := os.Getenv("DF_ONBOARDING_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL DSN not set")
	}
	open := func() *gorm.DB {
		db, e := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if e != nil {
			t.Fatal("cannot open isolated MySQL")
		}
		return db
	}
	db1, db2 := open(), open()
	if e := Migrate(db1); e != nil {
		t.Fatal(e)
	}
	if e := MigrateCompletions(db1); e != nil {
		t.Fatal(e)
	}
	if e := MigrateQuota(db1); e != nil {
		t.Fatal(e)
	}
	a, _ := New(db1, time.Minute)
	b, _ := New(db2, time.Minute)
	p := Principal{"issuer", "quota-user", "tenant"}
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			_, e := s.BeginBounded(context.Background(), p, 3, 3, 5)
			if e == nil {
				success.Add(1)
			} else if !errors.Is(e, ErrUnavailable) {
				t.Errorf("admission: %v", e)
			}
		}(i)
	}
	wg.Wait()
	if success.Load() != 3 {
		t.Fatalf("success=%d, want 3", success.Load())
	}
}

func TestMySQLTenantQuotaAcrossReplicas(t *testing.T) {
	dsn := os.Getenv("DF_ONBOARDING_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL DSN not set")
	}
	open := func() *gorm.DB {
		db, e := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if e != nil {
			t.Fatal("cannot open isolated MySQL")
		}
		return db
	}
	db1, db2 := open(), open()
	if e := Migrate(db1); e != nil {
		t.Fatal(e)
	}
	if e := MigrateCompletions(db1); e != nil {
		t.Fatal(e)
	}
	if e := MigrateQuota(db1); e != nil {
		t.Fatal(e)
	}
	a, _ := New(db1, time.Minute)
	b, _ := New(db2, time.Minute)
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			p := Principal{"tenant-issuer", fmt.Sprintf("tenant-user-%d", i), "tenant-a"}
			_, e := s.BeginBounded(context.Background(), p, 1, 3, 5)
			if e == nil {
				success.Add(1)
			} else if !errors.Is(e, ErrUnavailable) {
				t.Errorf("admission: %v", e)
			}
		}(i)
	}
	wg.Wait()
	if success.Load() != 3 {
		t.Fatalf("tenant admission=%d want 3", success.Load())
	}
	if _, e := a.BeginBounded(context.Background(), Principal{"tenant-issuer", "tenant-b-user", "tenant-b"}, 1, 3, 5); e != nil {
		t.Fatal("other tenant was blocked", e)
	}
}
