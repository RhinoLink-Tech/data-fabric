# Warehouse batch route

## Business task

A real warehouse workflow starts a new batch after the previous batch finishes its empty-container return. Until the new batch reports its own phase event, its route must start at `WAIT` and point to `FULL_PALLET`. The last event from the older batch must not set the new route.

Batch IDs, station labels, and records below are synthetic. They reproduce this business scenario in an isolated PostgreSQL 17 profile.

## Run the reference

Load `schema.sql` and `seed.sql` into a new disposable PostgreSQL 17 database. Do not point it at an existing application database.

The two synthetic batches are:

- `9001`: previous batch with a successful `empty` phase.
- `9002`: new pending batch with no phase event.

Use the Data Fabric query Skill with the `context-v1.yaml` asset:

1. Select the isolated reference source.
2. Call `describe_table` for `pallet_tasks` and `tray_records`; compare the required columns and types in the asset.
3. Call `validate_sql_query` for `route.sql`.
4. Call `execute_sql_query` with parameter `[9002]`.

Expected result: `route_origin=WAIT`, `next_destination=FULL_PALLET`.

The query joins events by the selected task's `batch_no`. It never treats an older batch's latest event as the current task's event.

## Simple schema-drift check

On this disposable database only, rename a referenced column:

```sql
ALTER TABLE public.pallet_tasks
  RENAME COLUMN pallet_started_command_id TO pallet_start_event_id;
```

A new `describe_table` result no longer matches context v1. Stop before query execution and mark v1 stale. After reviewing the new schema, update the template and asset to version 2, then replay the same task.

The context file is application-side guidance. An agent compares its columns with describe_table before use; this is not automatic gateway monitoring. Every query still uses the current caller's Data Fabric policy and read-only validation; the asset grants no access.
