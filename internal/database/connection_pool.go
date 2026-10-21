package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/readonly"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

var (
	globalDriverRegistry *DriverRegistry
	registryOnce         sync.Once
)

// GetDriverRegistry returns the global driver registry instance
func GetDriverRegistry() *DriverRegistry {
	registryOnce.Do(func() {
		globalDriverRegistry = NewDriverRegistry()
	})
	return globalDriverRegistry
}

// ConnectionPool manages database connections for different data sources
type ConnectionPool struct {
	pools           map[string]*sql.DB
	fingerprints    map[string]string // Config fingerprint per pooled connection (#117)
	mutex           sync.RWMutex
	health          map[string]bool
	healthMu        sync.RWMutex
	tokenManager    *security.TokenManager
	vault           *security.CredentialVault
	driverCaps      map[string]drivers.DriverCapabilities // Cached driver capabilities
	enforceReadOnly bool
}

// NewConnectionPool creates a new ConnectionPool instance. When
// enforceReadOnly is set, connections to engines with a read-only session
// mode are opened read-only (#20).
func NewConnectionPool(tokenManager *security.TokenManager, vault *security.CredentialVault, enforceReadOnly bool) *ConnectionPool {
	return &ConnectionPool{
		pools:           make(map[string]*sql.DB),
		fingerprints:    make(map[string]string),
		health:          make(map[string]bool),
		tokenManager:    tokenManager,
		vault:           vault,
		driverCaps:      make(map[string]drivers.DriverCapabilities),
		enforceReadOnly: enforceReadOnly,
	}
}

// GetConnection gets or creates a database connection for the specified data source
func (cp *ConnectionPool) GetConnection(ctx context.Context, dataSource *model.DataSource) (*sql.DB, error) {
	fingerprint := dataSourceFingerprint(dataSource)

	if db := cp.pooledConnectionIfCurrent(dataSource.ID, fingerprint); db != nil {
		// Check if connection is still alive
		if err := db.PingContext(ctx); err == nil {
			return db, nil
		}

		// Connection is dead. Evict exactly the instance that failed the
		// ping: by the time we take the lock, another goroutine may already
		// have replaced it with a fresh pool, which must survive (#119).
		cp.removeConnectionIfSame(dataSource.ID, db)
	} else {
		// Either no pool exists, or the pooled one was built from a different
		// configuration fingerprint. Evict only a demonstrably stale entry —
		// atomically, so a pool concurrently installed from the current
		// configuration is never dropped right after being built (#119).
		cp.removeStaleConnection(dataSource.ID, fingerprint)
	}

	// Create new connection
	return cp.createConnection(ctx, dataSource, fingerprint)
}

// pooledConnectionIfCurrent returns the pooled connection for a data source
// only when it was built from the current configuration fingerprint (#117).
// It never holds the lock while the caller pings the connection.
func (cp *ConnectionPool) pooledConnectionIfCurrent(dataSourceID, fingerprint string) *sql.DB {
	cp.mutex.RLock()
	defer cp.mutex.RUnlock()

	if db, exists := cp.pools[dataSourceID]; exists && cp.fingerprints[dataSourceID] == fingerprint {
		return db
	}
	return nil
}

// dataSourceFingerprint identifies the connection-relevant configuration of a
// data source: type, endpoint, credentials and pool tuning (#117). Two
// sources with the same fingerprint can share a pool; any change means the
// pooled connections were built from stale settings and must be replaced.
func dataSourceFingerprint(dataSource *model.DataSource) string {
	payload, err := json.Marshal(struct {
		Type   model.DatabaseType     `json:"type"`
		Config model.DataSourceConfig `json:"config"`
	}{Type: dataSource.Type, Config: dataSource.Config})
	if err != nil {
		// json.Marshal of this struct cannot fail in practice; fall back to a
		// unique value so an unexpected marshal failure degrades to pool
		// churn, never to reuse of a stale pool.
		return fmt.Sprintf("unfingerprintable-%p", dataSource)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// createConnection builds a new connection pool for the data source. All
// network I/O — opening the database, pinging it — happens outside the global
// pool lock so a slow or unreachable source cannot serialize pool creation
// for unrelated data sources (#119). Concurrent creators for the same source
// are reconciled at install time: the first pool wins, later duplicates are
// closed so nothing leaks.
func (cp *ConnectionPool) createConnection(ctx context.Context, dataSource *model.DataSource, fingerprint string) (*sql.DB, error) {
	// Get connection string
	dsn, err := cp.getConnectUrl(dataSource.Type, &dataSource.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection URL: %w", err)
	}

	// Inject the read-only session parameter where the dialect uses DSN
	// parameters for it (#20).
	readOnly := cp.readOnlyEnforced(&dataSource.Config)
	if readOnly {
		if roDSN, ok := readonly.ApplyDSN(dataSource.Type, dsn); ok {
			dsn = roDSN
		}
	}

	// Config-aware drivers can construct non-network database/sql adapters from
	// structured source settings. This keeps secrets and object-store options out
	// of the DSN while preserving the common query service path.
	db, err := cp.openConfiguredDatabase(ctx, dataSource.Type, dsn, &dataSource.Config, readOnly)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Configure connection pool
	if err := cp.configureConnectionPool(db, &dataSource.Config); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to configure connection pool: %w", err)
	}

	// Test the connection
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Install the pool under the write lock. A concurrent creator may have
	// installed the same source first; the winner is kept and the duplicate
	// closed (#119).
	cp.mutex.Lock()
	if existing, exists := cp.pools[dataSource.ID]; exists {
		if cp.fingerprints[dataSource.ID] == fingerprint {
			cp.mutex.Unlock()
			db.Close()
			cp.setHealth(dataSource.ID, true)
			return existing, nil
		}
		// The source changed again mid-flight; the installed pool is stale.
		delete(cp.pools, dataSource.ID)
		delete(cp.fingerprints, dataSource.ID)
	}
	cp.pools[dataSource.ID] = db
	cp.fingerprints[dataSource.ID] = fingerprint
	cp.mutex.Unlock()

	cp.setHealth(dataSource.ID, true)

	return db, nil
}

func (cp *ConnectionPool) openConfiguredDatabase(ctx context.Context, dbType model.DatabaseType, dsn string, config *model.DataSourceConfig, enforceReadOnly bool) (*sql.DB, error) {
	driver, err := GetDriverRegistry().GetDriver(dbType)
	if err != nil {
		return nil, err
	}
	if opener, ok := driver.(interface {
		OpenWithConfig(context.Context, *model.DataSourceConfig) (*sql.DB, error)
	}); ok {
		return opener.OpenWithConfig(ctx, config)
	}
	return cp.openDatabase(dbType, dsn, enforceReadOnly)
}

func (d *ConnectionPool) ApplyBatchPagination(dbType model.DatabaseType, sql string, batchSize, offset int64) (string, error) {
	driver, err := GetDriverRegistry().GetDriver(dbType)
	if err != nil {
		return "", err
	}
	return driver.ApplyBatchPagination(sql, batchSize, offset)
}

// openDatabase opens a database connection based on database type. When
// enforceReadOnly is set and the dialect initializes read-only sessions per
// connection, the pool's driver.Open is replaced by the read-only connector
// path so every pooled connection is covered (#20).
func (cp *ConnectionPool) openDatabase(dbType model.DatabaseType, dsn string, enforceReadOnly bool) (*sql.DB, error) {
	driver, err := GetDriverRegistry().GetDriver(dbType)
	if err != nil {
		return nil, err
	}
	if enforceReadOnly {
		if db, handled, err := readonly.OpenDB(dbType, dsn); handled {
			return db, err
		}
	}
	return driver.Open(dsn)
}

// readOnlyEnforced resolves the global security.enforce_read_only flag with
// the per-data-source override additionalProps.enforce_read_only (#20). The
// override accepts booleans and the strings "false"/"off"/"0" (JSON gives
// either shape).
func (cp *ConnectionPool) readOnlyEnforced(config *model.DataSourceConfig) bool {
	if !cp.enforceReadOnly {
		return false
	}
	if override, ok := config.AdditionalProps["enforce_read_only"]; ok {
		switch value := override.(type) {
		case bool:
			return value
		case string:
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "false", "off", "0":
				return false
			}
		}
	}
	return true
}

// getConnectUrl builds the connection string for the specified database type and configuration
func (cp *ConnectionPool) getConnectUrl(dbType model.DatabaseType, config *model.DataSourceConfig) (string, error) {
	driver, err := GetDriverRegistry().GetDriver(dbType)
	if err != nil {
		return "", err
	}
	return driver.BuildDSN(config), nil
}

// defaultIdleTimeout is how long a pooled data-source connection may sit
// unused before database/sql discards it, when the data source does not set
// idle_timeout. It matches the documented default of the field (#116).
const defaultIdleTimeout = 10 * time.Minute

// idleConnectionTimeout derives the idle timeout for a data source's pool.
// It is the data source's own idle_timeout knob, deliberately independent of
// the connect timeout: mixing the two closed idle connections after seconds
// (or kept them forever after a long dial timeout) depending on which value
// the operator tuned (#116).
func idleConnectionTimeout(config *model.DataSourceConfig) time.Duration {
	if config == nil {
		return defaultIdleTimeout
	}
	timeout := time.Duration(config.IdleTimeout) * time.Second
	if timeout <= 0 {
		return defaultIdleTimeout
	}
	return timeout
}

// configureConnectionPool configures the connection pool settings
func (cp *ConnectionPool) configureConnectionPool(db *sql.DB, config *model.DataSourceConfig) error {
	// Set connection pool size
	maxOpenConns := config.MaxPoolSize
	if maxOpenConns <= 0 {
		maxOpenConns = 10 // Default
	}
	db.SetMaxOpenConns(maxOpenConns)

	// Set maximum idle connections
	maxIdleConns := maxOpenConns / 2
	if maxIdleConns < 2 {
		maxIdleConns = 2
	}
	db.SetMaxIdleConns(maxIdleConns)

	// Set connection lifetime
	maxLifetime := time.Duration(config.MaxLifetime) * time.Second
	if maxLifetime <= 0 {
		maxLifetime = 30 * time.Minute // Default
	}
	db.SetConnMaxLifetime(maxLifetime)

	// Close idle connections after the dedicated idle timeout (#116)
	db.SetConnMaxIdleTime(idleConnectionTimeout(config))

	return nil
}

// removeConnection removes a connection from the pool
func (cp *ConnectionPool) removeConnection(dataSourceID string) {
	db, existed := cp.takeConnection(dataSourceID)
	if existed {
		db.Close()
	}
}

// removeConnectionIfSame evicts the pooled entry only when it is still the
// exact instance the caller found dead. Checking and dropping happen under
// one lock, so a fresh pool installed meanwhile is never closed out from
// under its users (#119).
func (cp *ConnectionPool) removeConnectionIfSame(dataSourceID string, dead *sql.DB) {
	cp.mutex.Lock()
	current, exists := cp.pools[dataSourceID]
	if exists && current == dead {
		delete(cp.pools, dataSourceID)
		delete(cp.fingerprints, dataSourceID)
	}
	cp.mutex.Unlock()

	if exists && current == dead {
		dead.Close()
		cp.setHealth(dataSourceID, false)
	}
}

// removeStaleConnection evicts the pooled entry only when its configuration
// fingerprint differs from the current one. The comparison and the drop are
// atomic, so a concurrently created pool built from the current configuration
// survives (#117, #119).
func (cp *ConnectionPool) removeStaleConnection(dataSourceID, fingerprint string) {
	cp.mutex.Lock()
	db, exists := cp.pools[dataSourceID]
	stale := exists && cp.fingerprints[dataSourceID] != fingerprint
	if stale {
		delete(cp.pools, dataSourceID)
		delete(cp.fingerprints, dataSourceID)
	}
	cp.mutex.Unlock()

	if stale {
		db.Close()
		cp.setHealth(dataSourceID, false)
	}
}

// takeConnection atomically removes a pooled connection (and its fingerprint)
// from the map and returns it; closing happens outside the lock so teardown
// I/O never blocks pool users (#119).
func (cp *ConnectionPool) takeConnection(dataSourceID string) (*sql.DB, bool) {
	cp.mutex.Lock()
	defer cp.mutex.Unlock()

	db, exists := cp.pools[dataSourceID]
	if exists {
		delete(cp.pools, dataSourceID)
		delete(cp.fingerprints, dataSourceID)
		cp.setHealth(dataSourceID, false)
	}
	return db, exists
}

// InvalidateDataSource closes and removes the pooled connections for a data
// source after its configuration changed or it was removed (#117). It is
// idempotent: a source with no pooled connections is not an error. Close
// failures are surfaced so callers can log them instead of leaking them
// silently; the pool entry is dropped either way so stale connections are
// never handed out again.
func (cp *ConnectionPool) InvalidateDataSource(dataSourceID string) error {
	db, existed := cp.takeConnection(dataSourceID)
	if !existed {
		return nil
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("failed to close pooled connections for data source %s: %w", dataSourceID, err)
	}
	return nil
}

// CloseConnection closes a specific connection and removes it from the pool
func (cp *ConnectionPool) CloseConnection(dataSourceID string) error {
	db, existed := cp.takeConnection(dataSourceID)
	if !existed {
		return fmt.Errorf("connection not found for data source: %s", dataSourceID)
	}
	return db.Close()
}

// CloseAll closes all connections in the pool
func (cp *ConnectionPool) CloseAll() error {
	cp.mutex.Lock()
	closing := cp.pools
	cp.pools = make(map[string]*sql.DB)
	cp.fingerprints = make(map[string]string)
	cp.mutex.Unlock()

	var lastErr error
	for id, db := range closing {
		if err := db.Close(); err != nil {
			lastErr = err
		}
		cp.setHealth(id, false)
	}

	return lastErr
}

// GetStats returns statistics for all connections in the pool
func (cp *ConnectionPool) GetStats() map[string]ConnectionStats {
	cp.mutex.RLock()
	defer cp.mutex.RUnlock()

	stats := make(map[string]ConnectionStats)
	for id, db := range cp.pools {
		dbStats := db.Stats()
		stats[id] = ConnectionStats{
			OpenConnections:   dbStats.OpenConnections,
			InUse:             dbStats.InUse,
			Idle:              dbStats.Idle,
			WaitCount:         dbStats.WaitCount,
			WaitDuration:      dbStats.WaitDuration,
			MaxIdleClosed:     dbStats.MaxIdleClosed,
			MaxLifetimeClosed: dbStats.MaxLifetimeClosed,
			Healthy:           cp.getHealth(id),
		}
	}

	return stats
}

// ConnectionStats contains connection pool statistics
type ConnectionStats struct {
	OpenConnections   int           `json:"openConnections"`
	InUse             int           `json:"inUse"`
	Idle              int           `json:"idle"`
	WaitCount         int64         `json:"waitCount"`
	WaitDuration      time.Duration `json:"waitDuration"`
	MaxIdleClosed     int64         `json:"maxIdleClosed"`
	MaxLifetimeClosed int64         `json:"maxLifetimeClosed"`
	Healthy           bool          `json:"healthy"`
}

// HealthCheck performs health checks on all connections
func (cp *ConnectionPool) HealthCheck(ctx context.Context) map[string]bool {
	cp.mutex.RLock()
	defer cp.mutex.RUnlock()

	results := make(map[string]bool)

	for id, db := range cp.pools {
		if err := db.PingContext(ctx); err != nil {
			cp.setHealth(id, false)
			results[id] = false
		} else {
			cp.setHealth(id, true)
			results[id] = true
		}
	}

	return results
}

// IsHealthy checks if a specific data source connection is healthy
func (cp *ConnectionPool) IsHealthy(dataSourceID string) bool {
	cp.healthMu.RLock()
	defer cp.healthMu.RUnlock()
	return cp.health[dataSourceID]
}

// setHealth sets the health status for a connection
func (cp *ConnectionPool) setHealth(dataSourceID string, healthy bool) {
	cp.healthMu.Lock()
	defer cp.healthMu.Unlock()
	cp.health[dataSourceID] = healthy
}

// getHealth gets the health status for a connection
func (cp *ConnectionPool) getHealth(dataSourceID string) bool {
	cp.healthMu.RLock()
	defer cp.healthMu.RUnlock()
	return cp.health[dataSourceID]
}

// =============================================================================
// Token Rotation Support
// =============================================================================

// SetupTokenRotation registers token rotation for a data source if needed
func (cp *ConnectionPool) SetupTokenRotation(ctx context.Context, dataSource *model.DataSource) error {
	driver, err := GetDriverRegistry().GetDriver(dataSource.Type)
	if err != nil {
		return err
	}

	caps := driver.GetCapabilities()
	if !caps.RequiresTokenRotation {
		return nil // No rotation needed
	}

	// Cache driver capabilities
	cp.driverCaps[dataSource.ID] = caps

	// Register token rotation with TokenManager
	if cp.tokenManager != nil {
		// Define rotation function
		onRotate := func(ctx context.Context) (string, time.Time, error) {
			return cp.rotateToken(ctx, dataSource)
		}

		// Set expiration time (default 1 hour, should be configured per data source)
		expiresAt := time.Now().Add(1 * time.Hour)

		cp.tokenManager.RegisterToken(dataSource.ID, expiresAt, onRotate)
	}

	return nil
}

// rotateToken performs token rotation for a data source
func (cp *ConnectionPool) rotateToken(ctx context.Context, dataSource *model.DataSource) (string, time.Time, error) {
	_, err := GetDriverRegistry().GetDriver(dataSource.Type)
	if err != nil {
		return "", time.Time{}, err
	}

	// Get credentials from vault
	// TODO: Implement credential retrieval from database via CredentialVault

	// Rotate token based on data source type
	switch dataSource.Type {
	case model.DatabaseTypeBigQuery:
		// GCP OAuth token rotation
		// TODO: Call GCP security to refresh OAuth token
		return "new-token", time.Now().Add(1 * time.Hour), nil

	// TODO: Add support for other data source types when they are implemented
	// case model.DatabaseTypeS3, model.DatabaseTypeMinIO:
	// case model.DatabaseTypeHDFS, model.DatabaseTypeOzone:

	default:
		return "", time.Time{}, fmt.Errorf("token rotation not supported for %s", dataSource.Type)
	}
}

// RefreshToken forces an immediate token refresh for a data source
func (cp *ConnectionPool) RefreshToken(ctx context.Context, dataSourceID string) error {
	if cp.tokenManager == nil {
		return fmt.Errorf("token manager not configured")
	}

	// Trigger token rotation
	_, _, err := cp.tokenManager.RotateTokenWithResult(ctx, dataSourceID)
	return err
}

// GetCurrentToken retrieves the current token for a data source
func (cp *ConnectionPool) GetCurrentToken(ctx context.Context, dataSourceID string) (string, error) {
	if cp.tokenManager == nil {
		return "", fmt.Errorf("token manager not configured")
	}

	return cp.tokenManager.GetToken(dataSourceID)
}

// RemoveTokenRotation removes token rotation for a data source
func (cp *ConnectionPool) RemoveTokenRotation(dataSourceID string) {
	if cp.tokenManager != nil {
		// TODO: Implement token removal in TokenManager
		_ = dataSourceID
	}
}

// GetDataSourceCapabilities returns cached driver capabilities for a data source
func (cp *ConnectionPool) GetDataSourceCapabilities(dataSourceID string) (drivers.DriverCapabilities, bool) {
	cp.mutex.RLock()
	defer cp.mutex.RUnlock()

	caps, exists := cp.driverCaps[dataSourceID]
	return caps, exists
}

// RequiresTokenRotation checks if a data source requires token rotation
func (cp *ConnectionPool) RequiresTokenRotation(dataSourceID string) bool {
	caps, exists := cp.GetDataSourceCapabilities(dataSourceID)
	return exists && caps.RequiresTokenRotation
}
