# Warehouse batch route: engineering reference

## Status

The maintainer confirmed that the batch-route reset is a real business scenario and that synthetic records are acceptable for the reference. This profile uses invented batch IDs and an isolated PostgreSQL 17 schema. It does not connect to a live source or count as a completed customer pilot.

The [context v1](../../../examples/warehouse-batch-route/context-v1.yaml) and [parameterized query](../../../examples/warehouse-batch-route/route.sql) are an application-side asset. The query Skill tells an agent to inspect the selected schema, compare required columns and types, validate the query, and bind the batch number. This comparison is documented guidance; the gateway does not watch or invalidate this file automatically. The gateway applies the caller's current policy on every execution.

## Acceptance

For synthetic batch 9002, batch 9001's completed empty phase must not be reused. The result is route origin `WAIT` and destination `FULL_PALLET`. In the documented drift drill, renaming a required column makes v1 stale; the agent stops before execution until an owner reviews the schema and publishes version 2.

## Issue progress

| Issue | Progress | Remaining evidence |
|---|---|---|
| #67 business context | Bounded implementation is in progress for the maintainer-confirmed task. | Live source identity and any later fixed-model comparison are not collected; this reference does not claim customer value. |
| #68 schema drift | The example defines a versioned reference asset and a manual describe_table mismatch/refresh path. It does not implement automatic gateway invalidation. | No live source schema change has been observed; the drift drill is synthetic. |
| #73 result masking | Not triggered by this task. | No identity or field requirement shows that column denial or a restricted view is insufficient. |

Raw rows, credentials, tenant identifiers, and unredacted prompts are not stored here.
