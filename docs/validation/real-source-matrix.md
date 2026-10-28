# Real Source Validation Matrix

**Validated set:** MySQL 8.4.11, PostgreSQL 18.6 (Docker)<br>
**Latest run:** 2026-10-21 — all checks passed ([run log](results/2026-10-21-mysql84-postgres18.md))<br>
**Tracking:** #35 (driver contract tests), D14

## Purpose

The unit and fake-driver test suites verify the gateway's logic against
stubs. This matrix verifies the *behavior at the boundary*: how the
production stack behaves against real database engines — actual protocol
handshakes, actual error text, actual driver type reporting, actual
session semantics. The harness
([scripts/validation/real_source_validation.go](../../scripts/validation/real_source_validation.go))
runs the real in-process gateway stack — SQL validation, the read-only
connection pool, and the pagination and streaming services behind the
unified query service — against live engine instances, and records one
verdict with evidence per operation.

Databases in the matrix beyond the validated set are pending: the
domestic and warehouse engines wait on the support matrix freeze before
their contract is pinned (see [data-source-support.md](../data-source-support.md)).

## Running

```bash
# Ephemeral containers (mysql:8.4, postgres:18), per-run passwords,
# automatic teardown:
./scripts/validation/run_real_source_validation.sh             # both engines
./scripts/validation/run_real_source_validation.sh mysql       # engine subset
```

Prerequisites: `docker`, `go`, `openssl`. The wrapper creates both
databases (`data_fabric_validation`), a write-capable reader account
(`df_reader`, SELECT + INSERT — see the read-only check below), waits for
each engine to be stably reachable, runs the harness, and tears the
containers down. Container passwords are generated per run with `openssl
rand` and never leave the process environment.

Against external databases, export the environment yourself and run the
harness directly:

| Variable | Meaning | Default |
| --- | --- | --- |
| `DF_VALIDATE_ENGINES` | engines to validate (comma or space separated) | `mysql,postgres` |
| `DF_VALIDATE_<ENGINE>_HOST` | engine host | required |
| `DF_VALIDATE_<ENGINE>_PORT` | engine port | 3306 / 5432 |
| `DF_VALIDATE_<ENGINE>_DATABASE` | fixture database (must exist; admin owns it) | `data_fabric_validation` |
| `DF_VALIDATE_<ENGINE>_ADMIN_USER` | DDL account for fixtures | `root` / `postgres` |
| `DF_VALIDATE_<ENGINE>_ADMIN_PASSWORD` | DDL account password | required |
| `DF_VALIDATE_<ENGINE>_READER_USER` | account the gateway connects as | `df_reader` |
| `DF_VALIDATE_<ENGINE>_READER_PASSWORD` | reader password | required |
| `DF_VALIDATE_POSTGRES_SSLMODE` | admin connection sslmode | `disable` |

The reader account must hold **INSERT** on the fixture database: the
read-only check deliberately uses a write-capable credential to prove the
gateway's session-level enforcement (#20) blocks writes the account could
otherwise perform.

Exit codes: `0` every check passed, `1` at least one validation failure,
`2` environment incomplete (missing variables, unreachable engine,
fixture setup failure).

## What each run does

Fixtures are created under a run-unique `dfval_<tag>_` prefix and dropped
afterwards: a 50,000-row main table (id/pad), a ten-column types table
covering the dialect's core scalars in one row, and a sacrificial table
for the write probe. MySQL seeds the main table with a recursive CTE
cross-join; PostgreSQL with `generate_series`.

The harness registers the reader as a data source in an in-memory
repository and wires two unified services over one read-only pool:
the default routing (auto mode, streaming preferred) and
`execution_mode: pagination`, so both documented deployments are
exercised. SQL translation is disabled (runtime manager off).

## Operation matrix

| Operation | What it proves | MySQL 8.4 | PostgreSQL 18 |
| --- | --- | --- | --- |
| connect | reader reaches the engine through the full stack | PASS — 8.4.11 | PASS — PostgreSQL 18.6 |
| read-only | pool session blocks INSERT despite INSERT privilege (#20); reads still work | PASS — error 1792 | PASS — read-only transaction |
| parameters | driver binds `?` / `$1..$N`; injection payload returns 0 rows as data | PASS | PASS |
| pagination | Type 1 is single-shot; Type 2 under `execution_mode=pagination` pages 30 rows in 3×10 with exact totalCount | PASS | PASS |
| streaming | Type 2 on a streaming-capable engine genuinely streams (mode reported, 3 batches) | PASS | PASS |
| types | NULL→nil, DECIMAL/NUMERIC→exact string, BIGINT→int64, text bytes→string, per-mode column typing | PASS | PASS |
| cancellation | heavy self-join cancels <5s in both modes; pool usable after | PASS — 0.70s / 0.70s | PASS — 0.71s / 0.70s |
| error-mapping | syntax, missing-table, and non-SELECT failures map to the stable credential-free texts | PASS | PASS |
| isolation | interleaved continuations on one source don't cross; a bad-credential source doesn't disturb it | PASS | PASS |

Per-run evidence lines live in the dated logs under
[results/](results/).

## Findings (behavior gaps and resolutions)

The matrix records cross-path behavior differences the runs hit. None
broke the checks — they were pinned as current behavior and fixed as
follow-ups; each entry notes the resolving issue.

1. **Standardized column types diverge between execution modes** —
   resolved by #260. Both paths now map covered database families
   through the shared `utils.DataTypeMapper`; the streaming service's
   generic matcher remains only as the fallback for families the shared
   mapper does not cover. The rows below record what the 2026-10-21 run
   measured before the fix:

   | Column | Pagination reports | Streaming reports (pre-fix) |
   | --- | --- | --- |
   | MySQL `BIGINT` | `bigint` | `integer` |
   | MySQL `TEXT` | `text` | `string` |
   | MySQL `DATETIME` | `datetime` | `date` |
   | PostgreSQL `TEXT` | `text` | `string` |
   | PostgreSQL `TIMESTAMP` | `timestamp` | `time` |

2. **Canceled queries map unevenly** — resolved by #261. The stable
   cancellation message now also covers the server-side cancellation
   error (SQLSTATE 57014) lib/pq returns for a cancelled PostgreSQL
   statement. The original 2026-10-21 observation: a context-cancelled
   pagination query surfaced as "The query was cancelled before
   completion." on MySQL (both modes) and on PostgreSQL in streaming
   mode, but as the generic data-source failure message for PostgreSQL
   pagination.

3. **Canceled stream cleanup logs an error** — resolved by #262.
   `Rows.Close` cancellation is now treated as the expected outcome of
   client cancellation: the stream is deregistered without logging.
   The original 2026-10-21 observation: cleanup logged
   `stream session cleanup failed … context canceled` after every
   cancelled streaming query.

4. **Type 1 is single-shot by contract.** A synchronous (Type 1) fetch
   returns the first batch and opens no continuation session —
   `NextURI` stays empty even when more rows exist. The matrix pins this
   so a change is a deliberate, client-visible contract decision.

## Scope and non-goals

- The stack under test ends at the unified query service: no HTTP, JWT,
  or MCP layers (covered by their own suites).
- Not a performance benchmark (see
  [performance/](../performance/postgres-gateway-benchmark.md)).
- Engines outside the validated set are out of scope until their support
  tier is frozen.
