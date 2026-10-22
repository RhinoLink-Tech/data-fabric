# ADR-002: Read-only session enforcement at the connection layer

- Status: Accepted
- Date: 2026-10-18
- Related: #20

## Context

Application-layer SELECT validation (`internal/security`) is a single
defense: data-source credentials may still hold write privileges, and dialect
functions can have side effects inside a valid `SELECT`. Issue #20 asks for
database-side enforcement so the engine itself rejects writes.

All pooled connections are created in one place:
`ConnectionPool.createConnection` → `openDatabase`. Connections are recycled
(`SetConnMaxLifetime`/`SetConnMaxIdleTime`), and query, streaming, metadata,
and health-check traffic all share the pool. Streaming keeps cursors open for
minutes, so per-query transactions would hold transactions open for the full
stream lifetime.

## Decision

Enforce read-only sessions at connection creation, keyed by data source type
in a single strategy table (`internal/database/readonly`):

- MySQL-protocol types: `SET SESSION TRANSACTION READ ONLY` executed on every
  new physical connection through a `driver.Connector` wrapper
  (`mysql.NewConnector` + `sql.OpenDB`), so pool refills and recycling are
  covered.
- TiDB is excluded from this MySQL-protocol list. The pinned TiDB 8.5.8
  contract returns error 1235 for the generic read-only statement and reports
  that it is not implemented; the shared MySQL wire protocol is not sufficient
  evidence of enforcement.
- PostgreSQL-protocol types: `default_transaction_read_only=on` appended to
  the DSN (lib/pq forwards unknown parameters to the server); works for both
  URL-form and keyword-form DSNs.
- All other types stay unsupported: SQL validation and a least-privilege
  account only.

Rejected alternatives:

- `BeginTx(&sql.TxOptions{ReadOnly: true})` per query — go-sql-driver does
  not implement `driver.ConnBeginTx` for read-only, so the option is
  *silently ignored* for the whole MySQL family (false security); it also
  adds round trips and holds a transaction open for the stream lifetime.
- One-shot `db.Exec` after `sql.Open` — only reaches whichever pooled
  connection serves it; recycled connections silently lack the init.
- Modifying each family driver's `Open` — a dozen copy-pasted drivers; the
  pool is the single choke point that already holds the global flag and the
  per-data-source override.

Operational rules:

- `security.enforce_read_only` defaults to `true` and cannot be disabled in
  `release` mode (mirrors `enable_auth` in `ValidateSecurity`).
- Per-data-source opt-out via `additionalProps.enforce_read_only` — it can
  only weaken enforcement, never re-enable it against a global off.
- Fail-closed: an engine rejecting the init statement makes the data source
  unusable; the error text names the opt-out. A silent fallback would fake
  protection.
- `test-connection` verifies the enforced session flag and warns about
  write-capable accounts, so "cannot connect" and "rejects read-only" are
  distinguishable.

Engines on the MySQL/PostgreSQL wire protocols whose read-only behavior is
not documented (Doris, StarRocks, Hologres, Redshift, and the unverified
domestic engines) are excluded until live verification: a wrong guess bricks
the data source. They are added per-engine after verification, not per
protocol.

A `DriverCapabilities` flag was considered and rejected: the strategy table
would then have two sources of truth that can drift.

## Consequences

- Every connection to an enforced engine is read-only, including metadata
  extraction and health checks (both are read-only workloads, verified).
- MySQL DSNs are parsed eagerly at pool creation (`mysql.ParseDSN`), so
  malformed DSNs fail at data-source use time instead of first query.
- Unverified engines get no database-side layer; their users rely on SQL
  validation and least-privilege accounts, which the docs and
  `validate-config` warnings state explicitly.
- The support matrix lives in `readonly.ModeFor` and
  `docs/readonly-access.md`; both must change together.
