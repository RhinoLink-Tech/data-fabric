package singleton

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type delayedConnector struct {
	entered, release chan struct{}
	once             sync.Once
	lost             atomic.Bool
}

func (c *delayedConnector) Connect(context.Context) (driver.Conn, error) { return &delayedConn{c}, nil }
func (c *delayedConnector) Driver() driver.Driver                        { return noOpenDriver{} }

type noOpenDriver struct{}

func (noOpenDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

type delayedConn struct{ c *delayedConnector }

func (c *delayedConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *delayedConn) Close() error                        { return nil }
func (c *delayedConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (c *delayedConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	c.c.once.Do(func() { close(c.c.entered) })
	select {
	case <-c.c.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	value := int64(1)
	if c.c.lost.Load() {
		value = 0
	}
	return &oneRow{value: value}, nil
}

type oneRow struct {
	value int64
	used  bool
}

func (r *oneRow) Columns() []string { return []string{"held"} }
func (r *oneRow) Close() error      { return nil }
func (r *oneRow) Next(v []driver.Value) error {
	if r.used {
		return io.EOF
	}
	r.used = true
	v[0] = r.value
	return nil
}

func TestBusyFloodAndSlowCheckDoNotLoseLease(t *testing.T) {
	driverConn := &delayedConnector{entered: make(chan struct{}), release: make(chan struct{})}
	db := sql.OpenDB(driverConn)
	defer db.Close()
	conn, e := db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	var losses atomic.Int32
	lease := &Lease{conn: conn, key: "synthetic", onLoss: func() { losses.Add(1) }, done: make(chan struct{}), checkSlot: make(chan struct{}, 1)}
	first := make(chan error, 1)
	go func() { first <- lease.Check(context.Background()) }()
	select {
	case <-driverConn.entered:
	case <-time.After(time.Second):
		t.Fatal("slow check did not start")
	}
	watchCtx, cancel := context.WithCancel(context.Background())
	watchDone := make(chan error, 1)
	go func() { watchDone <- lease.Watch(watchCtx, time.Millisecond, 10*time.Millisecond) }()
	var busy, other atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 300; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if errors.Is(lease.Check(context.Background()), ErrBusy) {
				busy.Add(1)
			} else {
				other.Add(1)
			}
		}()
	}
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	if busy.Load() != 300 || other.Load() != 0 || losses.Load() != 0 {
		t.Fatal("contention caused false lease loss", busy.Load(), other.Load(), losses.Load())
	}
	close(driverConn.release)
	if e = <-first; e != nil {
		t.Fatal("valid slow check failed", e)
	}
	cancel()
	<-watchDone
	if losses.Load() != 0 {
		t.Fatal("watcher stopped a valid lease")
	}
	driverConn.lost.Store(true)
	if e = lease.Check(context.Background()); !errors.Is(e, ErrUnavailable) || losses.Load() != 1 {
		t.Fatal("real lock loss not detected", e, losses.Load())
	}
}
