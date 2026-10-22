# Read-Only Data Access

Data Fabric treats every data source as read-only. This page documents the
defense layers, which engines get database-side enforcement, how to configure
it, and how to provision least-privilege accounts (#20).

## Threat model

The SQL validator (`internal/security`) parses each query with the grammar
selected for the target data source. It requires one query statement and
rejects mutations found anywhere in the AST, `SELECT INTO`, row locks, and a
denylist of side-effect functions. Unknown data-source dialects and parser
errors fail closed. That check runs in the application layer, which means it
cannot protect against:

- credentials that hold write privileges on the data source,
- dialect functions with side effects that parse as valid `SELECT`s,
- future validator gaps or bypasses.

Issue #20 adds a second, database-side layer: where the engine supports it,
sessions are opened read-only so the engine itself rejects writes, independent
of what the application sends.

## Defense layers

| Layer | Scope | Mechanism |
|---|---|---|
| SQL validation | SQL data sources with a configured parser mapping | One query statement; AST mutation, output, locking, and side-effect function checks (`internal/security/sql_validator.go`) |
| Read-only session | MySQL/PostgreSQL-lineage relational engines (see matrix below) | Session opened read-only at connection time; engine rejects writes |
| Least-privilege account | All data sources | Operator provisions an account limited to `SELECT` (cookbook below) |
| Engine-side controls | Federated query engines | Catalog permissions and connectors are owned by the deployed engine |

## Engine support matrix

| Engine | Database-side enforcement | Mechanism |
|---|---|---|
| MySQL, MariaDB | Yes — standard behavior | `SET SESSION TRANSACTION READ ONLY` on every pooled connection |
| OceanBase (MySQL mode), TDSQL, GBase 8s/8t | Enabled; verify on your engine version | Same MySQL-protocol init statement |
| TiDB (tested v8.5.8; other versions unverified) | No — verified server reports the generic read-only statement as unimplemented/no-op | SQL validation + least-privilege TiDB account; do not treat the shared MySQL protocol as proof of session enforcement |
| PostgreSQL | Yes — standard behavior | `default_transaction_read_only=on` connection parameter |
| GaussDB, KingbaseES, openGauss, Oscar | Enabled; verify on your engine version | Same PostgreSQL-protocol parameter |
| Doris, StarRocks, Hologres, Redshift | No — excluded pending live verification | Same wire protocol, but read-only behavior is not documented for these engines; SQL validation + least-privilege account only |
| Oracle, SQL Server, Snowflake, Hive, MongoDB | No — no session-level read-only mode | SQL validation + least-privilege account only |
| ClickHouse | No — the native protocol has no gateway read-only session initializer | SQL validation + least-privilege ClickHouse account |
| DaMeng | No — driver not yet functional | SQL validation only |
| Trino, Presto, OpenLooKeng | Engine-side | The deployed engine owns catalog and connector permissions |

The single source of truth for this matrix is `internal/database/readonly`
(`ModeFor`). Engines are added to the enforced set only after live
verification, because a rejected init statement makes the data source
unusable (fail-closed by design).

## SQL parser mapping

Production query validation uses the bundled SQLGlot runtime and selects a
parser by data-source type. MySQL-family connectors use MySQL grammar;
PostgreSQL-family connectors use PostgreSQL grammar; Trino, Presto, and
OpenLooKeng use their corresponding parser families; Oracle, SQL Server,
Snowflake, Databricks, Redshift, BigQuery, Hive, ClickHouse, Doris, StarRocks,
and Druid use their matching grammars. Compatibility connectors use the
closest available SQLGlot grammar (for example, MaxCompute uses Hive grammar;
file and object storage SQL uses the supported MySQL subset). MongoDB has no
SQL parser mapping and is rejected by this SQL query path.

The parser enforces one statement, requires a query root, scans nested AST
nodes for write operations, and rejects `SELECT INTO`, locking clauses, and
known side-effect functions such as file access, outbound connections, sleep,
and administrative functions. The production path does not rely on SQL
keyword or comment regexes to decide whether a statement is read-only. This
application check complements database-side read-only sessions and least
privilege; it does not replace them.

SQLGlot is required at startup for read-only parsing even when
`sql_translation.enabled` is false. That setting disables translation tools
and cross-dialect conversion only; it does not disable SQL safety validation.

## Configuration

`security.enforce_read_only` (default `true`) enables database-side
enforcement for every supported engine. It **cannot be disabled in `release`
mode**; the server refuses to start, mirroring `security.enable_auth`.

Individual data sources can opt out when an engine rejects the init
statement or the session parameter:

```yaml
# data-source config (additionalProps)
additionalProps:
  enforce_read_only: false
```

The override accepts a boolean or the strings `false`/`off`/`0`. It can only
weaken enforcement: setting it to `true` never re-enables enforcement when
the global flag is off. Opting out is surfaced as a warning by
`POST /api/v1/database/validate-config`.

## Fail-closed behavior

When an engine rejects the read-only init statement, connection creation
fails and the data source stays unusable until the engine accepts the
statement or you set the per-data-source override. This is deliberate: a
silent fallback would create the impression of database-side protection that
does not exist.

`POST /api/v1/database/test-connection` distinguishes the conditions:

- `readOnlyEnforced: true` — the enforced session flag was read back
  successfully (MySQL `@@SESSION.TRANSACTION_READ_ONLY`, PostgreSQL
  `SHOW transaction_read_only`),
- `readOnlyEnforced: false` with a warning — connectivity succeeded but the
  read-only session could not be established or verified,
- no field — the engine type has no enforcement mode, or enforcement is
  disabled.

The same response carries least-privilege warnings (see below).

## Least-privilege cookbook

Use an account that can only read. Enforcement is defense-in-depth, not a
substitute for a restricted account.

**MySQL / MariaDB / TiDB / OceanBase (MySQL mode)**

```sql
CREATE USER 'fabric_ro'@'%' IDENTIFIED BY '<random>';
GRANT SELECT ON analytics.* TO 'fabric_ro'@'%';
-- do not grant PROCESS, FILE, SUPER, or CREATE USER
```

**PostgreSQL / GaussDB / KingbaseES / openGauss / Oscar**

```sql
CREATE ROLE fabric_ro LOGIN PASSWORD '<random>';
GRANT USAGE ON SCHEMA analytics TO fabric_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA analytics TO fabric_ro;
ALTER DEFAULT PRIVILEGES IN SCHEMA analytics GRANT SELECT ON TABLES TO fabric_ro;
ALTER ROLE fabric_ro SET default_transaction_read_only = on;  -- belt and braces
```

**Verification**

`test-connection` warns when the account holds write or administrative
privileges:

- MySQL: `SHOW GRANTS FOR CURRENT_USER()` — warns on `ALL PRIVILEGES`,
  `SUPER`, `FILE`, `INSERT`, `UPDATE`, `DELETE`, `DROP`, `ALTER`, `CREATE`,
  `GRANT OPTION`.
- PostgreSQL: `pg_roles` — warns on `rolsuper`, `rolcreatedb`,
  `rolcreaterole`.

Manual check on MySQL:

```sql
SET SESSION TRANSACTION READ ONLY;
CREATE DATABASE sm;  -- expect: ERROR 1792 (HY000): Cannot execute statement in a READ ONLY transaction
```

## What this does not cover

- Federated engines (Trino/Presto/OpenLooKeng): catalog and connector
  permissions are configured on the engine, not the gateway.
- Engines excluded from the matrix: rely on SQL validation and a
  least-privilege account.
- The metadata database Data Fabric itself uses: configure it with the same
  least-privilege care.
