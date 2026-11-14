package singleton

import (
	"context"
	"database/sql"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// Uses only a disposable isolated MySQL instance. The test kills one dedicated
// connection it has created; never supply a production database.
func TestMySQLExclusionAndLostSession(t *testing.T) {
	dsn := os.Getenv("DF_SINGLETON_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL DSN not supplied")
	}
	db, e := sql.Open("mysql", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db2, e := sql.Open("mysql", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db2.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	key := fmt.Sprintf("df-test-%d", time.Now().UnixNano())
	var losses atomic.Int32
	lease, e := Acquire(ctx, db, key, func() { losses.Add(1) })
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Close()
	if e = lease.Check(ctx); e != nil {
		t.Fatal(e)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if e = lease.Check(cancelled); e == nil {
		t.Fatal("cancelled caller accepted")
	}
	if e = lease.Check(ctx); e != nil || losses.Load() != 0 {
		t.Fatal("caller cancellation killed lease", e)
	}
	if _, e = Acquire(ctx, db2, key, func() {}); e == nil {
		t.Fatal("second process acquired live lock")
	}
	var holder int64
	if e = db2.QueryRowContext(ctx, "SELECT IS_USED_LOCK(?)", key).Scan(&holder); e != nil {
		t.Fatal(e)
	}
	if _, e = db2.ExecContext(ctx, fmt.Sprintf("KILL CONNECTION %d", holder)); e != nil {
		t.Fatal(e)
	}
	if e = lease.Check(ctx); e == nil {
		t.Fatal("lost lock accepted")
	}
	if e = lease.Check(ctx); e == nil || losses.Load() != 1 {
		t.Fatal("loss callback count or fail closed", e, losses.Load())
	}
	replacement, e := Acquire(ctx, db2, key, func() {})
	if e != nil {
		t.Fatal("replacement not admitted", e)
	}
	defer replacement.Close()
	if e = replacement.Check(ctx); e != nil {
		t.Fatal(e)
	}
}
