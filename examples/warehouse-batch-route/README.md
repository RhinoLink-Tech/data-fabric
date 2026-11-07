# Warehouse batch route reference

This example captures one bounded task from a real warehouse workflow:

> When a new batch is queued after the previous batch completed its empty-container phase, what route should be shown before the new batch reports its own phase event?

The route for the new batch starts at `WAIT` and points to `FULL_PALLET`. A successful phase event from an older batch is historical context; it must not become the route origin for the new batch.

All batch IDs, station labels, timestamps, and rows in this example are invented. The profile uses a separate PostgreSQL 17 reference source. It contains no production records, credentials, or source-system connection details.

## Files

- `schema.sql` defines the small reference shape.
- `seed.sql` places a completed prior batch beside a newly queued batch.
- `route.sql` is the Data Fabric query template. Bind the requested batch number as parameter 1.
- `route.psql.sql` runs the same bounded query directly with psql for local inspection.
- `context-v1.yaml` records the meaning, source/schema scope, permission boundary, and invalidation rule for this draft asset.

## Run against a new isolated PostgreSQL 17 database

Use a new local database owned by this reference project. Do not point these commands at an existing application database.

```sh
psql "$ROUTE_REFERENCE_DSN" -v ON_ERROR_STOP=1 -f schema.sql -f seed.sql
psql "$ROUTE_REFERENCE_DSN" -v ON_ERROR_STOP=1 -v batch_no=9002 -f route.psql.sql
```

Expected row:

| batch_no | route_origin | next_destination |
|---:|---|---|
| 9002 | WAIT | FULL_PALLET |

The prior batch has a later successful `empty` event, but the query joins phase events using the selected task's `batch_no`. It does not read a global latest-event row and apply it to another batch.

## Use through Data Fabric

Register only the isolated reference database with a read-only source identity. Grant the caller only the required table and column access. Submit the contents of `route.sql` to `POST /api/v1/fetch` with:

- `dataSourceId`: the reference source ID
- `parameters`: `[9002]`
- `sql`: the template in `route.sql`

The gateway still authenticates the caller, checks current source/table/column policy, validates the read-only statement, and binds the parameter for every execution. This asset does not grant access. If different users may see different batches, expose a source-side view or role that enforces that row scope; table/column policy alone is not row-level authorization.

## Scope and evidence boundary

This is a deterministic reference query, not an AI evaluation and not a Data Fabric runtime feature. It covers only the first route decision while a task is queued and before its own phase event starts. Other task states return `OUT_OF_SCOPE`.

The draft asset describes a real business rule with synthetic rows. It does not prove that Data Fabric has reproduced the original failure, that this schema matches an approved live source, or that a real team has adopted the asset. See the [candidate evidence report](../../docs/validation/results/2026-11-07-warehouse-route-candidate.md) before treating this as pilot evidence.
