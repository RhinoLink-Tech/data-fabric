// Package singleton provides a process-lifetime MySQL advisory lock for an
// isolated deployment. It is not a complete fencing system and is not wired to
// the server. A deployment must close pools and stop serving on loss, check
// before operations, bound in-flight work and exclude all unrestricted writers.
package singleton

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"time"
)

var ErrUnavailable = errors.New("singleton lease unavailable")

// ErrBusy means no lock query was attempted; it never implies lease loss.
var ErrBusy = errors.New("singleton lease check busy")

type Lease struct {
	conn      *sql.Conn
	key       string
	mu        sync.Mutex
	closed    bool
	failed    bool
	onLoss    func()
	done      chan struct{}
	checkSlot chan struct{}
	once      sync.Once
}

func Acquire(ctx context.Context, db *sql.DB, key string, onLoss func()) (*Lease, error) {
	if db == nil || key == "" || len(key) > 64 || onLoss == nil {
		return nil, ErrUnavailable
	}
	conn, e := db.Conn(ctx)
	if e != nil {
		return nil, ErrUnavailable
	}
	var got sql.NullInt64
	e = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", key).Scan(&got)
	if e != nil || !got.Valid || got.Int64 != 1 {
		if e != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
		return nil, ErrUnavailable
	}
	return &Lease{conn: conn, key: key, onLoss: onLoss, done: make(chan struct{}), checkSlot: make(chan struct{}, 1)}, nil
}
func (l *Lease) fail() {
	l.mu.Lock()
	if l.closed || l.failed {
		l.mu.Unlock()
		return
	}
	l.failed = true
	callback := l.onLoss
	l.mu.Unlock()
	l.once.Do(func() { close(l.done) })
	callback()
}

// Check fails closed if the specific dedicated MySQL session no longer owns
// the advisory lock. A successful Check is not a distributed fencing token.
func (l *Lease) Check(ctx context.Context) error {
	if l == nil {
		return ErrUnavailable
	}
	l.mu.Lock()
	bad := l.closed || l.failed
	l.mu.Unlock()
	if bad {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Never queue on the dedicated connection: queue time must not consume a
	// lock-query timeout or turn contention into a false lease loss.
	select {
	case l.checkSlot <- struct{}{}:
		defer func() { <-l.checkSlot }()
	default:
		return ErrBusy
	}
	// Recheck after admission in case the lease was lost while being acquired.
	l.mu.Lock()
	bad = l.closed || l.failed
	l.mu.Unlock()
	if bad {
		return ErrUnavailable
	}
	// A cancelled client request must not close the dedicated lock session and
	// disable the server. Independently bound the lock check instead.
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	var held sql.NullInt64
	e := l.conn.QueryRowContext(checkCtx, "SELECT IS_USED_LOCK(?) = CONNECTION_ID()", l.key).Scan(&held)
	if e != nil || !held.Valid || held.Int64 != 1 {
		l.fail()
		return ErrUnavailable
	}
	l.mu.Lock()
	bad = l.closed || l.failed
	l.mu.Unlock()
	if bad {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// Watch calls onLoss once after the first failed bounded heartbeat. Loss of
// the monitor context does not release or invalidate the lease; caller must
// invoke Close during orderly shutdown.
func (l *Lease) Watch(ctx context.Context, interval, timeout time.Duration) error {
	if l == nil || interval <= 0 || timeout <= 0 {
		return ErrUnavailable
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-l.done:
			return ErrUnavailable
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(ctx, timeout)
			e := l.Check(checkCtx)
			cancel()
			if errors.Is(e, ErrBusy) || errors.Is(e, context.DeadlineExceeded) {
				continue
			}
			if e != nil {
				return e
			}
		}
	}
}
func (l *Lease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()
	l.once.Do(func() { close(l.done) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var released sql.NullInt64
	e := l.conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", l.key).Scan(&released)
	if e != nil || !released.Valid || released.Int64 != 1 {
		_ = l.conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	return l.conn.Close()
}
