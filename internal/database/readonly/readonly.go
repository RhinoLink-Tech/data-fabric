// Package readonly provides database-side read-only session enforcement (#20).
//
// The application-layer SELECT validator (internal/security) is a single
// defense: data-source credentials may still hold write privileges and
// dialect functions can have side effects. This package adds a second,
// database-side layer so the engine itself rejects writes:
//
//   - ModeDSNParam: PostgreSQL-protocol engines receive the
//     default_transaction_read_only GUC as a connection parameter; lib/pq
//     forwards parameters it does not recognize to the server in the
//     startup packet.
//   - ModeConnectorInit: MySQL-protocol engines verified to support the
//     read-only statement get MySQLInitSQL executed on every new physical
//     connection through a driver.Connector wrapper, so connection-pool
//     recycling cannot bypass it.
//
// Engines whose read-only behavior on these wire protocols is not verified
// or is a no-op (TiDB, Doris, StarRocks, Hologres, Redshift, Oracle, SQL
// Server, ...) stay ModeUnsupported: they keep application-layer validation
// only and are documented in docs/readonly-access.md. This mode table is the
// single source of truth for which types get database-side enforcement.
package readonly

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// Mode describes how database-side read-only enforcement is applied (#20).
type Mode int

const (
	// ModeUnsupported means no database-side enforcement: application-layer
	// SQL validation and a least-privilege account are the only defenses.
	ModeUnsupported Mode = iota
	// ModeDSNParam appends a read-only session parameter to the DSN
	// (PostgreSQL protocol).
	ModeDSNParam
	// ModeConnectorInit runs an init statement on every new connection
	// (MySQL protocol).
	ModeConnectorInit
)

const (
	// MySQLInitSQL marks the session read-only for all subsequent
	// transactions; write statements fail with MySQL error 1792.
	MySQLInitSQL = "SET SESSION TRANSACTION READ ONLY"

	// PostgresReadOnlyDSNParam is the GUC that makes every transaction in
	// the session read-only unless explicitly overridden.
	PostgresReadOnlyDSNParam = "default_transaction_read_only"
	PostgresReadOnlyDSNValue = "on"
)

// ErrReadOnlyInitFailed wraps every read-only session initialization failure
// so operators can recognize the fail-closed condition and disable
// enforcement for that data source via additionalProps.enforce_read_only.
var ErrReadOnlyInitFailed = errors.New("read-only session initialization failed")

// sessionModes is the single source of truth for database-side read-only
// enforcement (#20). Included types are MySQL/PostgreSQL-lineage relational
// engines where the session read-only behavior is part of the documented
// protocol. Engines on the same wire protocol but with unverified or no-op
// behavior (TiDB, Doris, StarRocks, Hologres, Redshift) are deliberately
// excluded: a rejected init statement makes the data source unusable, while
// a no-op would create false read-only assurance.
var sessionModes = map[model.DatabaseType]Mode{
	// MySQL protocol (go-sql-driver): per-connection init statement.
	model.DatabaseTypeMySQL:          ModeConnectorInit,
	model.DatabaseTypeMariaDB:        ModeConnectorInit,
	model.DatabaseTypeOceanBaseMySQL: ModeConnectorInit,
	model.DatabaseTypeTDSQL:          ModeConnectorInit,
	model.DatabaseTypeGBase8s:        ModeConnectorInit,
	model.DatabaseTypeGBase8t:        ModeConnectorInit,

	// PostgreSQL protocol (lib/pq): DSN session parameter. gaussdb_mysql is
	// registered with the lib/pq driver in this repository, so it shares the
	// PostgreSQL mechanism.
	model.DatabaseTypePostgreSQL:      ModeDSNParam,
	model.DatabaseTypeGaussDBMySQL:    ModeDSNParam,
	model.DatabaseTypeGaussDBPostgres: ModeDSNParam,
	model.DatabaseTypeKingbaseES:      ModeDSNParam,
	model.DatabaseTypeOpenGauss:       ModeDSNParam,
	model.DatabaseTypeOscar:           ModeDSNParam,
}

// ModeFor returns the database-side read-only enforcement mode for a data
// source type (#20).
func ModeFor(dbType model.DatabaseType) Mode {
	if mode, ok := sessionModes[dbType]; ok {
		return mode
	}
	return ModeUnsupported
}

// SupportsReadOnlySession reports whether dbType has a database-side
// read-only enforcement mode.
func SupportsReadOnlySession(dbType model.DatabaseType) bool {
	return ModeFor(dbType) != ModeUnsupported
}

// ApplyDSN appends the read-only session parameter to the DSN for
// ModeDSNParam types and reports whether it applied (#20). URL-form DSNs
// (postgres://...) get a query parameter; keyword-value DSNs
// (host=... dbname=...) get a plain appended pair — the value contains no
// spaces, so no quoting is needed. The DSN is never re-encoded.
func ApplyDSN(dbType model.DatabaseType, dsn string) (string, bool) {
	if ModeFor(dbType) != ModeDSNParam {
		return dsn, false
	}
	param := PostgresReadOnlyDSNParam + "=" + PostgresReadOnlyDSNValue
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if strings.Contains(dsn, "?") {
			return dsn + "&" + param, true
		}
		return dsn + "?" + param, true
	}
	return dsn + " " + param, true
}

// OpenDB returns a *sql.DB for ModeConnectorInit types whose every new
// physical connection runs MySQLInitSQL before entering the pool (#20).
// handled is false for other modes; callers fall back to driver.Open.
// Initialization failure fails the connection (fail-closed): the pool
// reports the error and the data source stays unusable until the engine
// accepts the statement or enforcement is disabled for that data source.
func OpenDB(dbType model.DatabaseType, dsn string) (db *sql.DB, handled bool, err error) {
	if ModeFor(dbType) != ModeConnectorInit {
		return nil, false, nil
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, true, fmt.Errorf("%w: invalid MySQL DSN: %w", ErrReadOnlyInitFailed, err)
	}
	base, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, true, fmt.Errorf("%w: %w", ErrReadOnlyInitFailed, err)
	}
	return sql.OpenDB(initConnector{base: base, initSQL: MySQLInitSQL}), true, nil
}

// initConnector wraps a driver.Connector and marks every new connection
// read-only before it is returned to database/sql (#20). Connectors are the
// only hook that covers pool refills and connection recycling; a one-time
// db.Exec after Open would only hit whichever connection happened to serve
// it.
type initConnector struct {
	base    driver.Connector
	initSQL string
}

// Connect opens a connection through the wrapped connector and initializes
// the read-only session on it.
func (c initConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	execer, ok := conn.(driver.ExecerContext)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("%w: driver connection does not support ExecerContext", ErrReadOnlyInitFailed)
	}
	if _, err := execer.ExecContext(ctx, c.initSQL, nil); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w (%q): disable for this data source via additionalProps.enforce_read_only=false: %w", ErrReadOnlyInitFailed, c.initSQL, err)
	}
	return conn, nil
}

// Driver returns the wrapped connector's driver.
func (c initConnector) Driver() driver.Driver {
	return c.base.Driver()
}
