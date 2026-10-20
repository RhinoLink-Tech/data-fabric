package database

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// failingDriver is a database/sql driver whose connections always fail, used to
// simulate a pooled connection that has gone stale.
type failingDriver struct{}

func (failingDriver) Open(string) (sqldriver.Conn, error) {
	return nil, errors.New("connection refused")
}

type failingConnector struct{}

func (failingConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return nil, errors.New("connection refused")
}

func (failingConnector) Driver() sqldriver.Driver { return failingDriver{} }

// deadDB returns a *sql.DB that is already in the pool but can no longer reach
// its database, so PingContext reports it as dead.
func deadDB() *sql.DB {
	return sql.OpenDB(failingConnector{})
}

// A pooled connection that fails its liveness ping must be evicted and replaced
// without panicking. The stale entry is removed and the replacement attempt
// fails, which has to surface as an error.
func TestGetConnectionEvictsDeadPooledConnection(t *testing.T) {
	const unregisteredType = model.DatabaseType("__unregistered_for_test__")

	pool := NewConnectionPool(nil, nil, false)
	pool.pools["ds-1"] = deadDB()

	dataSource := &model.DataSource{
		ID:     "ds-1",
		Type:   unregisteredType,
		Config: model.DataSourceConfig{Host: "127.0.0.1", Port: 3306, Database: "demo"},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("GetConnection panicked while evicting a dead pooled connection: %v", r)
		}
	}()

	if _, err := pool.GetConnection(context.Background(), dataSource); err == nil {
		t.Fatal("GetConnection returned a dead pooled connection instead of an error")
	}

	if _, stillPooled := pool.pools["ds-1"]; stillPooled {
		t.Error("the dead connection is still in the pool after GetConnection")
	}
}

// getConnectUrl and ApplyBatchPagination must not depend on some earlier caller
// having initialized the package-level driver registry.
func TestRegistryDependentHelpersWorkOnColdStart(t *testing.T) {
	registryOnce = sync.Once{}
	globalDriverRegistry = nil

	pool := NewConnectionPool(nil, nil, false)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("helper panicked before the driver registry was initialized: %v", r)
		}
	}()

	dsn, err := pool.getConnectUrl(model.DatabaseTypeMySQL, &model.DataSourceConfig{
		Host:     "127.0.0.1",
		Port:     3306,
		Database: "demo",
	})
	if err != nil {
		t.Fatalf("getConnectUrl failed on a cold start: %v", err)
	}
	if dsn == "" {
		t.Error("getConnectUrl returned an empty DSN on a cold start")
	}

	paginated, err := pool.ApplyBatchPagination(model.DatabaseTypeMySQL, "SELECT 1", 10, 0)
	if err != nil {
		t.Fatalf("ApplyBatchPagination failed on a cold start: %v", err)
	}
	if paginated == "SELECT 1" {
		t.Errorf("ApplyBatchPagination returned the query unchanged: %q", paginated)
	}
}

// Read-only enforcement is the global flag ANDed with a per-data-source
// override that can only opt out, never re-enable (#20).
func TestReadOnlyEnforced(t *testing.T) {
	tests := []struct {
		name   string
		global bool
		props  map[string]interface{}
		want   bool
	}{
		{"global off", false, nil, false},
		{"global on without override", true, nil, true},
		{"bool false opts out", true, map[string]interface{}{"enforce_read_only": false}, false},
		{"bool true stays enforced", true, map[string]interface{}{"enforce_read_only": true}, true},
		{"string false opts out", true, map[string]interface{}{"enforce_read_only": "false"}, false},
		{"string off opts out", true, map[string]interface{}{"enforce_read_only": "off"}, false},
		{"string 0 opts out", true, map[string]interface{}{"enforce_read_only": "0"}, false},
		{"unrecognized string stays enforced", true, map[string]interface{}{"enforce_read_only": "banana"}, true},
		{"unrelated properties ignored", true, map[string]interface{}{"pool_hint": false}, true},
		// A per-data-source override can only weaken enforcement; the global
		// flag is the release-mode lock (#20).
		{"global off cannot be re-enabled per data source", false, map[string]interface{}{"enforce_read_only": true}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := NewConnectionPool(nil, nil, tt.global)
			config := &model.DataSourceConfig{AdditionalProps: tt.props}
			if got := pool.readOnlyEnforced(config); got != tt.want {
				t.Errorf("readOnlyEnforced() = %v, want %v", got, tt.want)
			}
		})
	}
}

// pingableConn is a database/sql connection whose ping always succeeds, so a
// pool built on it is healthy without any network.
type pingableConn struct{}

func (pingableConn) Prepare(string) (sqldriver.Stmt, error) {
	return nil, errors.New("not implemented")
}
func (pingableConn) Close() error                      { return nil }
func (pingableConn) Begin() (sqldriver.Tx, error)      { return nil, errors.New("not implemented") }
func (pingableConn) PingContext(context.Context) error { return nil }

type pingableConnector struct{}

func (pingableConnector) Connect(context.Context) (sqldriver.Conn, error) { return pingableConn{}, nil }
func (pingableConnector) Driver() sqldriver.Driver                        { return failingDriver{} }

// stubPoolDriver registers a driver whose Open returns an always-healthy
// in-memory *sql.DB, so pool lifecycle tests run without a network.
type stubPoolDriver struct {
	drivers.Driver
}

func (stubPoolDriver) BuildDSN(*model.DataSourceConfig) string { return "stub://" }
func (stubPoolDriver) Open(string) (*sql.DB, error)            { return sql.OpenDB(pingableConnector{}), nil }

const stubPoolTestType = model.DatabaseType("__stub_pool_test__")

const recoveryPoolTestType = model.DatabaseType("__recovery_pool_test__")

func stubPoolDataSource(id string) *model.DataSource {
	return &model.DataSource{
		ID:     id,
		Type:   stubPoolTestType,
		Config: model.DataSourceConfig{Host: "stub", Port: 1, Database: "stub", Username: "stub"},
	}
}

// A pool built from a different configuration fingerprint must not be handed
// out again: it is replaced by a pool built from the current configuration
// (#117).
func TestGetConnectionEvictsPoolWithStaleFingerprint(t *testing.T) {
	GetDriverRegistry().RegisterDriver(stubPoolTestType, stubPoolDriver{})
	pool := NewConnectionPool(nil, nil, false)
	stale := deadDB()
	pool.pools["ds-1"] = stale
	pool.fingerprints["ds-1"] = "built-from-old-config"

	fresh, err := pool.GetConnection(context.Background(), stubPoolDataSource("ds-1"))
	if err != nil {
		t.Fatalf("GetConnection failed to rebuild from the current configuration: %v", err)
	}
	if fresh == stale {
		t.Fatal("GetConnection handed out the pool built from the stale configuration")
	}

	pool.mutex.RLock()
	installed, fingerprint := pool.pools["ds-1"], pool.fingerprints["ds-1"]
	pool.mutex.RUnlock()
	if installed != fresh {
		t.Error("the pool built from the current configuration was not installed")
	}
	if want := dataSourceFingerprint(stubPoolDataSource("ds-1")); fingerprint != want {
		t.Errorf("installed fingerprint = %q, want the current configuration fingerprint %q", fingerprint, want)
	}
}

// An unchanged configuration reuses the installed pool instead of churning it.
func TestGetConnectionReusesPoolWithMatchingFingerprint(t *testing.T) {
	GetDriverRegistry().RegisterDriver(stubPoolTestType, stubPoolDriver{})
	pool := NewConnectionPool(nil, nil, false)

	first, err := pool.GetConnection(context.Background(), stubPoolDataSource("ds-1"))
	if err != nil {
		t.Fatalf("first GetConnection failed: %v", err)
	}
	second, err := pool.GetConnection(context.Background(), stubPoolDataSource("ds-1"))
	if err != nil {
		t.Fatalf("second GetConnection failed: %v", err)
	}
	if first != second {
		t.Error("GetConnection rebuilt the pool although the configuration was unchanged")
	}
}

// Concurrent connection setup for the same data source must resolve to a
// single installed pool, with the losing duplicates closed (#119).
func TestConcurrentGetConnectionInstallsSinglePool(t *testing.T) {
	GetDriverRegistry().RegisterDriver(stubPoolTestType, stubPoolDriver{})
	pool := NewConnectionPool(nil, nil, false)

	const goroutines = 16
	results := make([]*sql.DB, goroutines)
	errs := make([]error, goroutines)
	var start, done atomic.Int32
	var ready sync.WaitGroup
	ready.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer ready.Done()
			start.Add(1)
			// Give every goroutine a chance to be inside createConnection
			// at the same time before any of them finishes.
			for start.Load() < goroutines {
			}
			results[i], errs[i] = pool.GetConnection(context.Background(), stubPoolDataSource("ds-1"))
			done.Add(1)
		}(i)
	}
	ready.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d GetConnection failed: %v", i, err)
		}
	}
	for i := 1; i < goroutines; i++ {
		if results[i] != results[0] {
			t.Fatalf("goroutine %d got a different pool instance; concurrent creation installed more than one", i)
		}
	}

	pool.mutex.RLock()
	pooled, pooledCount := pool.pools["ds-1"], len(pool.pools)
	pool.mutex.RUnlock()
	if pooledCount != 1 {
		t.Errorf("pool map holds %d entries after concurrent creation, want 1", pooledCount)
	}
	if pooled != results[0] {
		t.Error("the installed pool is not the one handed to callers")
	}
}

// InvalidateDataSource drops the pooled entry, reports health as false, and is
// idempotent for sources with no pool (#117).
func TestInvalidateDataSourceClosesAndIsIdempotent(t *testing.T) {
	GetDriverRegistry().RegisterDriver(stubPoolTestType, stubPoolDriver{})
	pool := NewConnectionPool(nil, nil, false)

	if _, err := pool.GetConnection(context.Background(), stubPoolDataSource("ds-1")); err != nil {
		t.Fatalf("GetConnection failed: %v", err)
	}

	if err := pool.InvalidateDataSource("ds-1"); err != nil {
		t.Fatalf("InvalidateDataSource failed: %v", err)
	}
	if _, stillPooled := pool.pools["ds-1"]; stillPooled {
		t.Error("the pool is still installed after invalidation")
	}
	if pool.IsHealthy("ds-1") {
		t.Error("an invalidated data source is still reported healthy")
	}

	if err := pool.InvalidateDataSource("ds-1"); err != nil {
		t.Errorf("second InvalidateDataSource for a pool-less source returned an error: %v", err)
	}
}

type recoveryPoolState struct {
	failedGeneration atomic.Int32
	openCount        atomic.Int32
	closeCount       atomic.Int32
}

type recoveryPoolDriver struct {
	drivers.Driver
	state *recoveryPoolState
}

func (d recoveryPoolDriver) BuildDSN(*model.DataSourceConfig) string { return "recovery://" }

func (d recoveryPoolDriver) Open(string) (*sql.DB, error) {
	generation := d.state.openCount.Add(1)
	return sql.OpenDB(recoveryPoolConnector{state: d.state, generation: generation}), nil
}

type recoveryPoolConnector struct {
	state      *recoveryPoolState
	generation int32
}

func (c recoveryPoolConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return recoveryPoolConn{state: c.state, generation: c.generation}, nil
}

func (c recoveryPoolConnector) Driver() sqldriver.Driver { return failingDriver{} }

type recoveryPoolConn struct {
	state      *recoveryPoolState
	generation int32
}

func (recoveryPoolConn) Prepare(string) (sqldriver.Stmt, error) {
	return nil, errors.New("not implemented")
}

func (c recoveryPoolConn) Close() error {
	c.state.closeCount.Add(1)
	return nil
}

func (recoveryPoolConn) Begin() (sqldriver.Tx, error) {
	return nil, errors.New("not implemented")
}

func (c recoveryPoolConn) Ping(context.Context) error {
	if c.state.failedGeneration.Load() == c.generation {
		return errors.New("injected ping failure")
	}
	return nil
}

func TestGetConnectionRebuildsAfterPingFailure(t *testing.T) {
	state := &recoveryPoolState{}
	GetDriverRegistry().RegisterDriver(recoveryPoolTestType, recoveryPoolDriver{state: state})
	pool := NewConnectionPool(nil, nil, false)
	defer pool.CloseAll()

	dataSource := stubPoolDataSource("ds-recovery")
	dataSource.Type = recoveryPoolTestType
	first, err := pool.GetConnection(context.Background(), dataSource)
	if err != nil {
		t.Fatalf("initial GetConnection failed: %v", err)
	}
	if got := state.openCount.Load(); got != 1 {
		t.Fatalf("opened %d pools on initial connection, want 1", got)
	}

	state.failedGeneration.Store(1)
	result := make(chan struct {
		db  *sql.DB
		err error
	}, 1)
	go func() {
		db, err := pool.GetConnection(context.Background(), dataSource)
		result <- struct {
			db  *sql.DB
			err error
		}{db: db, err: err}
	}()

	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("GetConnection failed to replace an unhealthy pool: %v", got.err)
		}
		if got.db == first {
			t.Fatal("GetConnection returned the same pool after its health check failed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("GetConnection did not release the pool lock after a failed health check")
	}

	if got := state.openCount.Load(); got != 2 {
		t.Errorf("opened %d pools after recovery, want 2 total", got)
	}
	if !pool.IsHealthy(dataSource.ID) {
		t.Error("rebuilt connection is not marked healthy")
	}
	if got := state.closeCount.Load(); got == 0 {
		t.Error("failed connection was not closed during rebuild")
	}
}

func TestConcurrentConnectionCloseIsIdempotent(t *testing.T) {
	state := &recoveryPoolState{}
	db := sql.OpenDB(recoveryPoolConnector{state: state, generation: 1})
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("prepare fake connection: %v", err)
	}

	pool := NewConnectionPool(nil, nil, false)
	const dataSourceID = "ds-concurrent-close"
	pool.pools[dataSourceID] = db
	pool.fingerprints[dataSourceID] = "test"
	pool.setHealth(dataSourceID, true)

	var workers sync.WaitGroup
	workers.Add(3)
	go func() {
		defer workers.Done()
		if err := pool.CloseConnection(dataSourceID); err != nil && !strings.Contains(err.Error(), "connection not found") {
			t.Errorf("CloseConnection returned unexpected error: %v", err)
		}
	}()
	go func() {
		defer workers.Done()
		if err := pool.InvalidateDataSource(dataSourceID); err != nil {
			t.Errorf("InvalidateDataSource returned error: %v", err)
		}
	}()
	go func() {
		defer workers.Done()
		if err := pool.CloseAll(); err != nil {
			t.Errorf("CloseAll returned error: %v", err)
		}
	}()
	workers.Wait()

	if got := state.closeCount.Load(); got != 1 {
		t.Errorf("underlying connection closed %d times, want exactly 1", got)
	}
	if stats := pool.GetStats(); len(stats) != 0 {
		t.Errorf("pool still contains connections after concurrent close: %#v", stats)
	}
	if pool.IsHealthy(dataSourceID) {
		t.Error("closed data source is still reported healthy")
	}
}
