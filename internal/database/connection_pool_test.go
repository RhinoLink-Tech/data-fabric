package database

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
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

	pool := NewConnectionPool(NewDriverRegistry(), nil, nil, false)
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
func TestRegistryDependentHelpersUseInjectedRegistry(t *testing.T) {
	pool := NewConnectionPool(NewDriverRegistry(), nil, nil, false)

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

func TestConnectionPoolReturnsErrorsForUnregisteredDriver(t *testing.T) {
	const unregisteredType = model.DatabaseType("__unregistered_injected_registry__")
	registry := &DriverRegistry{drivers: make(map[model.DatabaseType]func() drivers.Driver)}
	pool := NewConnectionPool(registry, nil, nil, false)

	if _, err := pool.getConnectUrl(unregisteredType, &model.DataSourceConfig{}); err == nil || !strings.Contains(err.Error(), "unsupported database type") {
		t.Fatalf("getConnectUrl error = %v, want unsupported database type", err)
	}
	if _, err := pool.ApplyBatchPagination(unregisteredType, "SELECT 1", 10, 0); err == nil || !strings.Contains(err.Error(), "unsupported database type") {
		t.Fatalf("ApplyBatchPagination error = %v, want unsupported database type", err)
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
			pool := NewConnectionPool(NewDriverRegistry(), nil, nil, tt.global)
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

func (stubPoolDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{}
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
	registry := NewDriverRegistry()
	registry.RegisterDriver(stubPoolTestType, stubPoolDriver{})
	pool := NewConnectionPool(registry, nil, nil, false)
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
	registry := NewDriverRegistry()
	registry.RegisterDriver(stubPoolTestType, stubPoolDriver{})
	pool := NewConnectionPool(registry, nil, nil, false)

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
	registry := NewDriverRegistry()
	registry.RegisterDriver(stubPoolTestType, stubPoolDriver{})
	pool := NewConnectionPool(registry, nil, nil, false)

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
	registry := NewDriverRegistry()
	registry.RegisterDriver(stubPoolTestType, stubPoolDriver{})
	pool := NewConnectionPool(registry, nil, nil, false)

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

func TestInvalidateDataSourceNotifiesActiveResourceOwnerWithoutIdlePool(t *testing.T) {
	pool := NewConnectionPool(nil, nil, nil, false)
	var invalidated []string
	pool.SetDataSourceInvalidationHandler(func(dataSourceID string) {
		invalidated = append(invalidated, dataSourceID)
	})

	if err := pool.InvalidateDataSource("source-without-idle-pool"); err != nil {
		t.Fatalf("InvalidateDataSource failed: %v", err)
	}
	if len(invalidated) != 1 || invalidated[0] != "source-without-idle-pool" {
		t.Fatalf("invalidation notifications = %v, want the source ID once", invalidated)
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

func (d recoveryPoolDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{}
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
	registry := NewDriverRegistry()
	registry.RegisterDriver(recoveryPoolTestType, recoveryPoolDriver{state: state})
	pool := NewConnectionPool(registry, nil, nil, false)
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

// The idle timeout is the data source's own idle_timeout, never the connect
// timeout it also carries; before #116 the pool reused config.Timeout, so a
// 30-second dial timeout evicted every idle connection every 30 seconds.
func TestIdleConnectionTimeout(t *testing.T) {
	tests := []struct {
		name   string
		config *model.DataSourceConfig
		want   time.Duration
	}{
		{"nil config uses the default", nil, defaultIdleTimeout},
		{"zero falls back to the default", &model.DataSourceConfig{}, defaultIdleTimeout},
		{"negative falls back to the default", &model.DataSourceConfig{IdleTimeout: -5}, defaultIdleTimeout},
		{"seconds are honored", &model.DataSourceConfig{IdleTimeout: 300}, 5 * time.Minute},
		{"distinct from the connect timeout", &model.DataSourceConfig{Timeout: 30, IdleTimeout: 600}, 10 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := idleConnectionTimeout(tt.config); got != tt.want {
				t.Errorf("idleConnectionTimeout(%+v) = %s, want %s", tt.config, got, tt.want)
			}
		})
	}
}

func TestConcurrentConnectionCloseIsIdempotent(t *testing.T) {
	state := &recoveryPoolState{}
	db := sql.OpenDB(recoveryPoolConnector{state: state, generation: 1})
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("prepare fake connection: %v", err)
	}

	pool := NewConnectionPool(NewDriverRegistry(), nil, nil, false)
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

func TestRotateTokenUsesConfiguredGoogleOAuthProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm() error = %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"provider-issued-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer server.Close()

	credentials := `{"type":"authorized_user","client_id":"client-id","client_secret":"client-secret","refresh_token":"refresh-token","token_uri":"` + server.URL + `"}`
	pool := NewConnectionPool(NewDriverRegistry(), security.NewTokenManager(nil), nil, false)
	token, expiresAt, err := pool.rotateToken(context.Background(), &model.DataSource{
		ID:   "bigquery-source",
		Type: model.DatabaseTypeBigQuery,
		Config: model.DataSourceConfig{
			AdditionalProps: map[string]interface{}{"credentials_json": credentials},
		},
	})
	if err != nil {
		t.Fatalf("rotateToken() error = %v", err)
	}
	if token != "provider-issued-token" {
		t.Fatalf("rotateToken() = %q, want provider-issued-token", token)
	}
	if !expiresAt.After(time.Now()) {
		t.Fatalf("rotateToken() expiry = %v, want a future expiry", expiresAt)
	}
}

func TestRotateTokenFailsClearlyForUnsupportedDataSource(t *testing.T) {
	pool := NewConnectionPool(NewDriverRegistry(), security.NewTokenManager(nil), nil, false)
	_, _, err := pool.rotateToken(context.Background(), &model.DataSource{Type: model.DatabaseTypeMySQL})
	if !errors.Is(err, security.ErrTokenRotationUnsupported) {
		t.Fatalf("rotateToken() error = %v, want ErrTokenRotationUnsupported", err)
	}
}

func TestDataSourceFingerprintIgnoresRotatedAccessToken(t *testing.T) {
	source := &model.DataSource{
		ID: "source-1", Type: model.DatabaseTypeBigQuery,
		Config: model.DataSourceConfig{AdditionalProps: map[string]interface{}{
			"project":                 "analytics",
			"access_token":            "token-one",
			"access_token_expires_at": "2026-11-01T06:00:00Z",
		}},
	}
	first := dataSourceFingerprint(source)
	source.Config.AdditionalProps["access_token"] = "token-two"
	source.Config.AdditionalProps["access_token_expires_at"] = "2026-11-01T07:00:00Z"
	if got := dataSourceFingerprint(source); got != first {
		t.Fatalf("rotating an access token changed the connection fingerprint: got %s, want %s", got, first)
	}
	source.Config.AdditionalProps["project"] = "warehouse"
	if got := dataSourceFingerprint(source); got == first {
		t.Fatal("changing connection configuration did not change the fingerprint")
	}
}

type blockingPoolConnector struct {
	entered chan struct{}
	resume  chan struct{}
}

func (c blockingPoolConnector) Connect(ctx context.Context) (sqldriver.Conn, error) {
	close(c.entered)
	select {
	case <-c.resume:
		return pingableConn{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (c blockingPoolConnector) Driver() sqldriver.Driver { return failingDriver{} }

type blockingPoolDriver struct {
	drivers.Driver
	entered chan struct{}
	resume  chan struct{}
}

func (d blockingPoolDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{}
}
func (d blockingPoolDriver) BuildDSN(*model.DataSourceConfig) string { return "stub://" }
func (d blockingPoolDriver) Open(string) (*sql.DB, error) {
	return sql.OpenDB(blockingPoolConnector{d.entered, d.resume}), nil
}
func TestInvalidateFencesConnectionBeingCreated(t *testing.T) {
	entered, resume := make(chan struct{}), make(chan struct{})
	reg := NewDriverRegistry()
	reg.RegisterDriver(stubPoolTestType, blockingPoolDriver{entered: entered, resume: resume})
	pool := NewConnectionPool(reg, nil, nil, false)
	result := make(chan error, 1)
	go func() { _, err := pool.GetConnection(context.Background(), stubPoolDataSource("ds-1")); result <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("connect did not begin")
	}
	if err := pool.InvalidateDataSource("ds-1"); err != nil {
		t.Fatal(err)
	}
	close(resume)
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("in-flight stale connection escaped invalidation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("connection setup did not finish")
	}
	pool.mutex.RLock()
	_, found := pool.pools["ds-1"]
	pool.mutex.RUnlock()
	if found {
		t.Fatal("stale pool installed")
	}
}

func TestFreshnessCheckFailsClosedForStaleAndUnavailableSnapshots(t *testing.T) {
	reg := NewDriverRegistry()
	reg.RegisterDriver(stubPoolTestType, stubPoolDriver{})
	pool := NewConnectionPool(reg, nil, nil, false)
	expected := "initial"
	unavailable := false
	if err := pool.SetSourceFreshnessCheck(func(_ context.Context, ds *model.DataSource) error {
		if unavailable || ds.Config.Password != expected {
			return errors.New("stale")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	source := stubPoolDataSource("ds-1")
	source.Config.Password = expected
	if _, err := pool.GetConnection(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	expected = "rotated"
	if _, err := pool.GetConnection(context.Background(), source); err == nil {
		t.Fatal("old snapshot accepted")
	}
	pool.mutex.RLock()
	_, found := pool.pools["ds-1"]
	pool.mutex.RUnlock()
	if found {
		t.Fatal("stale pool retained")
	}
	source.Config.Password = expected
	if _, err := pool.GetConnection(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	unavailable = true
	if _, err := pool.GetConnection(context.Background(), source); err == nil {
		t.Fatal("unavailable metadata accepted")
	}
}

func TestInvalidateDuringFreshnessCheckFencesConnection(t *testing.T) {
	reg := NewDriverRegistry()
	reg.RegisterDriver(stubPoolTestType, stubPoolDriver{})
	pool := NewConnectionPool(reg, nil, nil, false)
	entered, resume := make(chan struct{}), make(chan struct{})
	if err := pool.SetSourceFreshnessCheck(func(context.Context, *model.DataSource) error { close(entered); <-resume; return nil }); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := pool.GetConnection(context.Background(), stubPoolDataSource("ds-1")); result <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("freshness check not entered")
	}
	if err := pool.InvalidateDataSource("ds-1"); err != nil {
		t.Fatal(err)
	}
	close(resume)
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("stale setup escaped invalidation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request blocked")
	}
}

func TestStopPermanentlyFencesInflightBuilds(t *testing.T) {
	entered, resume := make(chan struct{}), make(chan struct{})
	reg := NewDriverRegistry()
	reg.RegisterDriver(stubPoolTestType, blockingPoolDriver{entered: entered, resume: resume})
	pool := NewConnectionPool(reg, nil, nil, false)
	result := make(chan error, 1)
	go func() { _, err := pool.GetConnection(context.Background(), stubPoolDataSource("ds-1")); result <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("connect did not begin")
	}
	if err := pool.Stop(); err != nil {
		t.Fatal(err)
	}
	close(resume)
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("in-flight connection escaped stop")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("connection blocked")
	}
	if _, err := pool.GetConnection(context.Background(), stubPoolDataSource("ds-1")); err == nil {
		t.Fatal("new connection accepted after stop")
	}
}

type delayedCloseConnector struct{ entered, release chan struct{} }

func (c delayedCloseConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return delayedCloseConnection{c.entered, c.release}, nil
}
func (c delayedCloseConnector) Driver() sqldriver.Driver { return failingDriver{} }

type delayedCloseConnection struct{ entered, release chan struct{} }

func (c delayedCloseConnection) Prepare(string) (sqldriver.Stmt, error) {
	return nil, errors.New("unused")
}
func (c delayedCloseConnection) Begin() (sqldriver.Tx, error) { return nil, errors.New("unused") }
func (c delayedCloseConnection) Close() error                 { close(c.entered); <-c.release; return nil }
func (c delayedCloseConnection) Ping(context.Context) error   { return nil }

func TestExpiredPoolPreservesHealthyStatusAcrossConcurrentReplacement(t *testing.T) {
	cp := NewConnectionPool(nil, nil, nil, false)
	entered, release := make(chan struct{}), make(chan struct{})
	old := sql.OpenDB(delayedCloseConnector{entered, release})
	if err := old.Ping(); err != nil {
		t.Fatal(err)
	}
	cp.pools["s"] = old
	cp.setHealth("s", true)
	done := make(chan struct{})
	go func() { cp.expireBoundedPool("s", old); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("old pool did not close")
	}
	// Expiry removed the old pool, but healthy describes the underlying source.
	if !cp.getHealth("s") {
		t.Fatal("ordinary expiry marked the source unhealthy")
	}
	replacement := sql.OpenDB(pingableConnector{})
	cp.mutex.Lock()
	cp.pools["s"] = replacement
	cp.mutex.Unlock()
	cp.setHealth("s", true)
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("expiry blocked")
	}
	if !cp.getHealth("s") {
		t.Fatal("old expiry overwrote replacement health")
	}
	if err := replacement.Ping(); err != nil {
		t.Fatal("old expiry closed replacement")
	}
	_ = cp.Stop()
}

type cancelPingConnector struct{ entered chan struct{} }

func (c cancelPingConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return cancelPingConn{c.entered}, nil
}
func (c cancelPingConnector) Driver() sqldriver.Driver { return failingDriver{} }

type cancelPingConn struct{ entered chan struct{} }

func (c cancelPingConn) Prepare(string) (sqldriver.Stmt, error) { return nil, errors.New("unused") }
func (c cancelPingConn) Close() error                           { return nil }
func (c cancelPingConn) Begin() (sqldriver.Tx, error)           { return nil, errors.New("unused") }
func (c cancelPingConn) Ping(ctx context.Context) error {
	close(c.entered)
	<-ctx.Done()
	return ctx.Err()
}
func TestCanceledPoolPingDoesNotEvictSharedPool(t *testing.T) {
	pool := NewConnectionPool(nil, nil, nil, false)
	source := stubPoolDataSource("ping-cancel")
	entered := make(chan struct{})
	shared := sql.OpenDB(cancelPingConnector{entered})
	defer shared.Close()
	pool.pools[source.ID] = shared
	pool.fingerprints[source.ID] = dataSourceFingerprint(source)
	var invalidations atomic.Int32
	pool.SetDataSourceInvalidationHandler(func(string) { invalidations.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, e := pool.GetConnection(ctx, source); result <- e }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("ping did not start")
	}
	cancel()
	if e := <-result; !errors.Is(e, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", e)
	}
	pool.mutex.RLock()
	installed := pool.pools[source.ID]
	pool.mutex.RUnlock()
	if installed != shared || invalidations.Load() != 0 {
		t.Fatal("canceled ping evicted a shared pool")
	}
}
func TestCanceledFreshnessDoesNotInvalidateSharedPool(t *testing.T) {
	for _, reason := range []error{context.Canceled, context.DeadlineExceeded} {
		pool := NewConnectionPool(nil, nil, nil, false)
		source := stubPoolDataSource("check-cancel")
		shared := sql.OpenDB(pingableConnector{})
		defer shared.Close()
		pool.pools[source.ID] = shared
		pool.fingerprints[source.ID] = dataSourceFingerprint(source)
		var invalidations atomic.Int32
		pool.SetDataSourceInvalidationHandler(func(string) { invalidations.Add(1) })
		if e := pool.SetSourceFreshnessCheck(func(context.Context, *model.DataSource) error { return reason }); e != nil {
			t.Fatal(e)
		}
		if _, e := pool.GetConnection(context.Background(), source); !errors.Is(e, reason) {
			t.Fatal("cancellation not preserved", e)
		}
		pool.mutex.RLock()
		installed := pool.pools[source.ID]
		pool.mutex.RUnlock()
		if installed != shared || invalidations.Load() != 0 {
			t.Fatal("canceled freshness check evicted a shared pool")
		}
	}
}
