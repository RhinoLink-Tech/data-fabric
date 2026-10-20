# API Usage Guide

This guide documents the query and fetch routes currently registered by the server. Register a data source first, then use its returned ID in query requests.

## Base URL and authentication

The default local base URL is `http://localhost:8080`. Query execution, continuation, validation, statistics, and history require a valid Bearer JWT. This remains true when `security.enable_auth` is disabled; that setting gates the data-source and database management endpoints.

Before running the examples, [start the server and mint a JWT](configuration.md#secret-management). Register a data source using `POST /api/v1/datasources`; for example, this request registers PostgreSQL:

```bash
curl -X POST http://localhost:8080/api/v1/datasources \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_ADMIN_JWT" \
  -d '{
    "name": "local-postgres",
    "type": "postgresql",
    "config": {
      "host": "127.0.0.1",
      "port": 5432,
      "database": "YOUR_DATABASE",
      "username": "YOUR_READ_ONLY_USER",
      "password": "YOUR_DATABASE_PASSWORD",
      "ssl": false
    }
  }'
```

Use `data.id` from the response as `dataSourceId` below. Replace the placeholders with values reachable from the Data Fabric server, and avoid putting real credentials in shell history.

Successful responses use this envelope:

```json
{
  "success": true,
  "data": {},
  "correlationId": "req-123",
  "timestamp": "2026-10-20T05:00:00Z"
}
```

Errors use `success: false` and an `error` object containing `code`, `message`, and optional `details`. `correlationId` and `timestamp` remain at the top level.

## SQL and result contract

- `sql` must be one read-only `SELECT` statement accepted by the selected data source. Data Fabric validates queries before execution.
- SQL uses the target data source dialect by default. Optional `sourceDialect` names the input dialect to translate into the target data source dialect for the supported syntax subset. This is not a promise of full SQL:2023 or identical support for every engine-specific function.
- `parameters` optionally carries ordered bind values — null, boolean, number, or string — that the SQL driver binds to `?` (MySQL-family) or `$1..$N` (PostgreSQL-family) placeholders. Values are never interpolated into the SQL text, and the same bound values are reused for every continuation batch and for the row count. At most 64 values are accepted; composite values such as objects or arrays are rejected with a message naming the parameter index and type but never the value. Never concatenate untrusted values into `sql` — pass them in `parameters`.
- Each row in `entries` is an array whose positions match the `columns` array. One wire representation covers pagination and streaming: SQL NULL is JSON `null` (the text string `"NULL"` stays a string), binary-typed columns are base64 strings, text-typed columns — including DECIMAL results, which keep their exact digits — are strings, and booleans, integers, and times pass through as JSON booleans, numbers, and strings.
- `totalCount` reports the row count for the query result. The gateway may issue an additional count query on the data source to provide it, so the count can add backend work and may reflect concurrent source changes between the count and row fetch. A statement the gateway cannot count exactly fails with HTTP 422 `COUNT_UNSUPPORTED` instead of returning a wrong count; simplify the statement or fetch without relying on the total.

## Execute a query

`POST /api/v1/fetch` executes the SQL and returns the first batch. The JSON field for batch size is `batch_size`.

```bash
curl -X POST http://localhost:8080/api/v1/fetch \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -d '{
    "dataSourceId": "UUID-OF-REGISTERED-DATA-SOURCE",
    "sourceDialect": "mysql",
    "sql": "SELECT order_id, created_at FROM orders WHERE status_id = ? ORDER BY order_id",
    "parameters": [1],
    "type": 2,
    "batch_size": 1000,
    "timeout": 120
  }'
```

Request fields:

| Field | Type | Required | Behavior |
|---|---|---:|---|
| `dataSourceId` | string | Yes | UUID of a registered, active data source the caller can read |
| `sql` | string | Yes | One read-only `SELECT` statement |
| `parameters` | array | No | Ordered bind values (null, boolean, number, string), at most 64; bound to `?`/`$N` placeholders, never interpolated into the SQL text |
| `sourceDialect` | string | No | Source SQL dialect for supported translation into the selected data source dialect |
| `type` | integer | No | `1` selects pagination; `2` requests streaming. If omitted, `query.execution_mode` decides using its configured mode and auto policy |
| `batch_size` | integer | No | Requested batch size, 1–10,000; default 10,000 |
| `timeout` | integer | No | Seconds allowed for the initial request; default 60, maximum 1,800 |

A successful response has this shape:

```json
{
  "success": true,
  "data": {
    "queryId": "550e8400-e29b-41d4-a716-446655440000",
    "slug": "a1b2c3d4",
    "token": "session-token",
    "executionMode": "streaming",
    "nextUri": "/api/v1/fetch/550e8400-e29b-41d4-a716-446655440000/a1b2c3d4/session-token?batch_size=1000",
    "columns": [
      {"name": "order_id", "type": "integer", "nullable": false},
      {"name": "created_at", "type": "datetime", "nullable": true}
    ],
    "entries": [
      [101, "2026-10-19T10:00:00Z"],
      [102, "2026-10-19T10:05:00Z"]
    ],
    "totalCount": 2400
  },
  "correlationId": "req-123",
  "timestamp": "2026-10-20T05:00:00Z"
}
```

The sample shows two rows for readability; a real response may contain up to the requested batch size.

`executionMode` is `streaming` or `pagination`. `nextUri` is omitted when the response has no continuation session. For a multi-batch result, request `type: 2` and follow every returned `nextUri` until the server stops returning one.

## Fetch the next batch

Call the exact relative URI returned in `data.nextUri`, adding the same base URL and a valid Bearer token:

```bash
curl -X GET "http://localhost:8080/api/v1/fetch/QUERY_ID/SLUG/TOKEN?batch_size=1000" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

The path `token` is a per-session continuation token; it does not replace the Bearer JWT. Use each new `nextUri` returned by the previous response rather than reusing an older token.

Query sessions and streaming cursors are held in process memory for up to 30 minutes. Continuation links stop working after a server restart and are not shared across gateway replicas. Until shared session storage or stateless continuation is available, run one instance or route every continuation request to the process that created the query. See [issue #26](https://github.com/RhinoLink-Tech/data-fabric/issues/26).

## Execution mode selection

`query.execution_mode` accepts `auto`, `streaming`, or `pagination`; an explicit configuration overrides the request hint.

In `auto` mode:

- `type: 1` uses pagination.
- `type: 2` requests streaming.
- With `type` omitted, the service considers streaming when `query.prefer_streaming` is enabled, `batch_size` is at least 1,000, the SQL does not declare a result limit of 1,000 rows or fewer, and the driver advertises streaming support.
- Unsupported drivers use pagination. If stream capacity is reached before execution starts, auto mode also falls back to pagination. Errors after stream execution begins are returned without re-running the SQL.
- The response `executionMode` identifies the path that actually handled the request.

See [query configuration](configuration.md#logging-query-and-mcp-configuration) for the configuration defaults.

## Query validation, statistics, and history

`/api/v1/query` is a namespace for validation and query metadata. SQL execution uses `POST /api/v1/fetch`.

### Validate SQL

Validation checks the data source access and SQL without executing the query:

```bash
curl -X POST http://localhost:8080/api/v1/query/validate \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -d '{
    "dataSourceId": "UUID-OF-REGISTERED-DATA-SOURCE",
    "sql": "SELECT order_id FROM orders WHERE status_id = 1",
    "sourceDialect": "postgres"
  }'
```

On success, `data` contains `valid: true` and a `queryId`. A query validation failure is returned as `valid: false` with a safe error message.

### Read query statistics

```bash
curl http://localhost:8080/api/v1/query/stats \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

The response `data` includes `totalQueries`, `successfulQueries`, `failedQueries`, `avgExecutionTime` (seconds), `lastQueryTime`, and `queriesByType`.

### Read query history

```bash
curl "http://localhost:8080/api/v1/query/history?limit=50&offset=0&dataSourceId=UUID-OF-REGISTERED-DATA-SOURCE" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

History is process-local, retains at most the most recent 1,000 records, and is scoped to the authenticated user. The response data contains `queries`, `total`, `limit`, and `offset`; SQL text is not returned.

## Compatibility note

This guide corrects the published examples and [OpenAPI contract](../specs/001-phase1-enhanced-single-source/contracts/openapi.yaml) to match the routes and response models in the current server. It does not change runtime API behavior. Clients should use `POST /api/v1/fetch` for SQL execution; `POST /api/v1/query/validate` only validates a query.

The MCP `execute_sql_query` tool accepts the same `parameters` array. The MCP SDK decodes JSON-RPC arguments with standard JSON parsing, so integers beyond ±2^53 degrade to floating point before the gateway receives them; the HTTP API has no such limit and preserves full 64-bit integers exactly.
