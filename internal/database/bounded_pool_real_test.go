package database

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// Real MySQL metadata + PostgreSQL source; opt-in disposable synthetic DBs.
func TestBoundedPoolsConvergeAcrossInstances(t *testing.T) {
	dsn, addr := os.Getenv("DF_BOUNDED_META_DSN"), os.Getenv("DF_BOUNDED_PG_ENDPOINT")
	if dsn == "" || addr == "" {
		t.Skip("isolated synthetic databases required")
	}
	ctx := context.Background()
	metadata, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer metadata.Close()
	if _, err = metadata.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS df_bounded_pool_test (id INT PRIMARY KEY, password_value VARCHAR(64) NOT NULL) ENGINE=InnoDB"); err != nil {
		t.Fatal(err)
	}
	defer metadata.ExecContext(ctx, "DROP TABLE IF EXISTS df_bounded_pool_test")
	if _, err = metadata.ExecContext(ctx, "INSERT INTO df_bounded_pool_test VALUES (1,?) ON DUPLICATE KEY UPDATE password_value=VALUES(password_value)", "synthetic"); err != nil {
		t.Fatal(err)
	}
	host, pstr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(pstr)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewFixedIPEgressPolicy([]string{"127.0.0.0/8"}, []int{port})
	if err != nil {
		t.Fatal(err)
	}
	source := &model.DataSource{ID: "synthetic-source", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Host: host, Port: port, Username: "synthetic", Password: "synthetic", Database: "synthetic"}}
	makePool := func() *ConnectionPool {
		p, e := NewRestrictedConnectionPool(nil, nil, nil, true, policy)
		if e != nil {
			t.Fatal(e)
		}
		e = p.SetBoundedSourceFreshness(300*time.Millisecond, func(ctx context.Context, snap *model.DataSource) error {
			var current string
			if e := metadata.QueryRowContext(ctx, "SELECT password_value FROM df_bounded_pool_test WHERE id=1").Scan(&current); e != nil {
				return e
			}
			if current != snap.Config.Password {
				return fmt.Errorf("stale")
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	first, second := makePool(), makePool()
	defer first.Stop()
	defer second.Stop()
	one, err := first.GetConnection(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	two, err := second.GetConnection(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = metadata.ExecContext(ctx, "UPDATE df_bounded_pool_test SET password_value=? WHERE id=1", "rotated"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*ConnectionPool{first, second} {
		if _, err = p.GetConnection(ctx, source); err == nil {
			t.Fatal("stale snapshot accepted after rotation")
		}
	}
	for _, db := range []*sql.DB{one, two} {
		if err = db.PingContext(ctx); err == nil {
			t.Fatal("invalidated old handle still accepts requests")
		}
	}
	// A caller holding an old pool but making no revalidation must not be able to
	// open it indefinitely. Recreate, then let only the hard timer close it.
	if _, err = metadata.ExecContext(ctx, "UPDATE df_bounded_pool_test SET password_value=? WHERE id=1", "synthetic"); err != nil {
		t.Fatal(err)
	}
	expiryEvents := make(chan struct{}, 1)
	first.SetDataSourceInvalidationHandler(func(string) {
		select {
		case expiryEvents <- struct{}{}:
		default:
		}
	})
	hold, err := first.GetConnection(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if err = hold.PingContext(ctx); err == nil {
		t.Fatal("expired handle can still connect")
	}
	select {
	case <-expiryEvents:
		t.Fatal("pool expiry cancelled unrelated cursor sessions")
	default:
	}
	// A replica can pass its primary check just before rotation. Its old
	// pool is admitted during the documented window, then must expire even
	// if no more requests reach that replica.
	third, err := NewRestrictedConnectionPool(nil, nil, nil, true, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Stop()
	checked, proceed := make(chan struct{}), make(chan struct{})
	err = third.SetBoundedSourceFreshness(500*time.Millisecond, func(ctx context.Context, snap *model.DataSource) error {
		var current string
		if e := metadata.QueryRowContext(ctx, "SELECT password_value FROM df_bounded_pool_test WHERE id=1").Scan(&current); e != nil {
			return e
		}
		if current != snap.Config.Password {
			return fmt.Errorf("stale")
		}
		close(checked)
		select {
		case <-proceed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		db  *sql.DB
		err error
	}
	opened := make(chan result, 1)
	go func() { db, e := third.GetConnection(ctx, source); opened <- result{db, e} }()
	<-checked
	if _, err = metadata.ExecContext(ctx, "UPDATE df_bounded_pool_test SET password_value=? WHERE id=1", "rotated"); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	old := <-opened
	if old.err != nil {
		t.Fatal(old.err)
	}
	time.Sleep(700 * time.Millisecond)
	if err = old.db.PingContext(ctx); err == nil {
		t.Fatal("in-flight stale pool did not expire")
	}

	// Primary unavailable: no new lease or stored-source probe may proceed.
	if err = metadata.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = second.GetConnection(ctx, source); err == nil {
		t.Fatal("unavailable primary accepted")
	}
	if err = NewHealthChecker(second).CheckStoredSourceConnectivity(ctx, source); err == nil {
		t.Fatal("stored source probe skipped primary check")
	}
}

func TestBoundedPoolRejectsSlowInFlightBuild(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, e := listener.Accept()
		if e == nil {
			accepted <- conn
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	policy, err := NewFixedIPEgressPolicy([]string{"127.0.0.0/8"}, []int{port})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := NewRestrictedConnectionPool(nil, nil, nil, true, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Stop()
	if err = pool.SetBoundedSourceFreshness(3*time.Second, func(context.Context, *model.DataSource) error { return nil }); err != nil {
		t.Fatal(err)
	}
	source := &model.DataSource{ID: "slow", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Host: "127.0.0.1", Port: port, Username: "synthetic", Password: "synthetic", Database: "synthetic"}}
	done := make(chan error, 1)
	go func() { _, e := pool.GetConnection(context.Background(), source); done <- e }()
	var conn net.Conn
	select {
	case conn = <-accepted:
		defer conn.Close()
	case e := <-done:
		t.Fatalf("restricted connector failed before handshake: %v", e)
	case <-time.After(8 * time.Second):
		t.Fatal("restricted connector did not start")
	}
	// Leave the actual PostgreSQL handshake unanswered. The absolute TTL
	// must cancel the in-flight build instead of installing a stale pool.
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("expired creation installed")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("expired creation did not return")
	}
	pool.mutex.RLock()
	_, installed := pool.pools[source.ID]
	pool.mutex.RUnlock()
	if installed {
		t.Fatal("expired pool remains installed")
	}
}
