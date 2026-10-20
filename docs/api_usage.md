# API Usage Guide

## Overview

Data Fabric provides a unified API for querying configured data sources. This guide covers the main API endpoints and usage patterns.

## Basic Query API

### Query-engine federation gateway

Register a Trino, Presto, or OpenLooKeng coordinator as one managed data
source. These engines expose a compatible statement REST protocol, so Data
Fabric reuses one connection adapter while selecting the correct engine header
family. Set `database` to the coordinator's default catalog; `schema` is
optional. Catalogs configured on the coordinator can then be referenced
together in one SQL statement.

Choose `type` as `trino`, `presto`, or `openlookeng` to match the
coordinator:

```bash
curl -X POST http://localhost:8080/api/v1/datasources \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -d '{
    "name": "query-engine-prod",
    "type": "trino",
    "config": {
      "host": "coordinator.example.com",
      "port": 8443,
      "database": "iceberg",
      "schema": "analytics",
      "username": "data-fabric",
      "password": "YOUR_PASSWORD",
      "ssl": true,
      "timeout": 30,
      "additionalProps": {"source": "data-fabric"}
    }
  }'
```

Basic-auth passwords require HTTPS. The default HTTP port is `8080`; set the
port exposed by your deployment. A coordinator's configured catalogs and
connectors determine which source systems and SQL features are available.

Use the returned data source ID for a query. Set `sourceDialect` when the
input SQL uses a dialect that should be translated to the query engine dialect.
Presto SQL translation is also used as the baseline for OpenLooKeng; engine
specific extensions may still require query adjustments.

```bash
curl -X POST http://localhost:8080/api/v1/query \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -d '{
    "dataSourceId": "uuid-of-query-engine-datasource",
    "sourceDialect": "mysql",
    "sql": "SELECT o.order_id, c.customer_name FROM iceberg.sales.orders o JOIN postgresql.crm.customers c ON o.customer_id = c.customer_id",
    "limit": 1000
  }'
```

SQL conversion is limited by the syntax supported by the selected source and
target dialects.

### Querying Snowflake Data Warehouse

```bash
curl -X POST http://localhost:8080/api/v1/query \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -d '{
    "dataSourceId": "uuid-of-snowflake-datasource",
    "sql": "SELECT * FROM orders WHERE order_date >= ?",
    "parameters": ["2024-01-01"],
    "limit": 100
  }'
```

### Querying S3 Parquet Files

```bash
curl -X POST http://localhost:8080/api/v1/query \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -d '{
    "dataSourceId": "uuid-of-s3-parquet-datasource",
    "sql": "SELECT * FROM s3://my-bucket/data/*.parquet WHERE year = ? AND month = ?",
    "parameters": ["2024", "01"],
    "limit": 1000
  }'
```

### Querying one S3 CSV object

S3 CSV sources use the normal `/api/v1/fetch` query path. Register one bucket
object as a table; `table_name` is optional and defaults to the object filename
without `.csv`.

```bash
curl -X POST http://localhost:8080/api/v1/datasources \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -d '{
    "name": "orders-csv",
    "type": "s3_csv",
    "config": {
      "host": "reports/orders.csv",
      "port": 443,
      "database": "analytics-bucket",
      "username": "YOUR_ACCESS_KEY",
      "password": "YOUR_SECRET_KEY",
      "ssl": true,
      "timeout": 30,
      "additionalProps": {
        "region": "us-east-1",
        "object_key": "reports/orders.csv",
        "table_name": "orders",
        "has_header": true,
        "delimiter": ",",
        "skip_header_rows": 0,
        "null_values": ["", "NULL"]
      }
    }
  }'
```

For S3-compatible storage, set `additionalProps.endpoint_url` and
`additionalProps.force_path_style` as required by that service. The access key
and secret key use the standard `username` and `password` fields. The region
defaults to `us-east-1` when omitted.

The supported SQL subset is a single configured table with `SELECT *` or named
columns, `WHERE` comparisons combined with `AND` (including `IN`, `LIKE`, and
NULL checks), `ORDER BY`, and `LIMIT`/`OFFSET`. The gateway adds batch paging
around this query for `/api/v1/fetch`; request cancellation is passed through to
the object request and CSV scan. Joins, `OR`, grouping, and general SQL
expressions are not supported by this file adapter.

The current reader downloads the complete CSV object and materializes its rows
before filtering and sorting. Use it for small files; large object scans should
use a query engine designed for distributed file processing.

### Time Travel Query (Delta Lake/Iceberg/Hudi)

```bash
curl -X POST http://localhost:8080/api/v1/query \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -d '{
    "dataSourceId": "uuid-of-delta-lake-datasource",
    "sql": "SELECT * FROM orders VERSION AS OF 5",  -- Delta Lake
    "limit": 100
  }'
```

### Querying TiDB with Distribution Info

```bash
curl -X POST http://localhost:8080/api/v1/query \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -d '{
    "dataSourceId": "uuid-of-tidb-datasource",
    "sql": "SELECT * FROM users WHERE id = ?",
    "parameters": ["12345"],
    "explain": true  -- Returns region distribution info
  }'
```

## Streaming Large Result Sets

For large result sets the gateway supports a streaming mode that keeps a server-side cursor
open and returns results in batches to avoid re-executing the full query with LIMIT/OFFSET.

- Use `/api/v1/query` for an inline query. When the backend supports streaming, the
  response may contain a `nextUri` field. Call that URI to retrieve the next batch.
- Use `/api/v1/fetch` to explicitly request batch/streaming behavior. This endpoint
  attempts to open a streaming cursor and returns a `queryId`, `slug`, `token`, and
  `nextUri` when more data is available.

Example: Execute query and get `nextUri` from `/query` (or use `/fetch`):

```bash
curl -X POST http://localhost:8080/api/v1/query \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -d '{
    "dataSourceId": "uuid-of-large-datasource",
    "sql": "SELECT * FROM very_large_table WHERE created_at >= ?",
    "parameters": ["2024-01-01"],
    "limit": 1000
  }'
```

If the response contains `nextUri`, call it to fetch subsequent batches:

```bash
curl -X GET "http://localhost:8080/api/v1/fetch/{queryId}/{slug}/{token}?batch_size=1000" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

Notes:

- In `query.execution_mode: auto`, `type: 2` requests streaming. Untyped requests
  use streaming when the batch is at least 1,000 rows, the SQL does not declare a
  result limit of 1,000 rows or fewer, `query.prefer_streaming` is enabled, and
  the source driver advertises streaming support. Smaller or explicitly limited
  results use pagination.
- If the selected driver does not support streaming, or the stream capacity is
  full before execution begins, auto mode uses LIMIT/OFFSET pagination. Errors
  after a stream starts are returned without re-running the SQL.
- Every fetch response includes `executionMode` (`streaming` or `pagination`).
  Continuation requests are routed to the session engine that owns the query.
- Streaming sessions are maintained server-side for a configurable TTL (default 30 minutes).
- A continuation token is single-use. Requests reusing the same token are serialized: the first accepted request advances the cursor and rotates the token; later replays with the old token are rejected. Use the token returned by each successful batch.
- Always close or exhaust the stream; the gateway will clean up expired sessions automatically.

## Fetch API - High-Performance Segmented Data Query

### Overview

The Fetch API provides high-performance segmented data query functionality supporting bulk retrieval of large amounts of data, using a common batched query pattern.

### Main Features

- **Segmented Query**: Supports fetching large datasets in batches
- **Streaming Processing**: Supports streaming queries (type=2)
- **Timeout Control**: Configurable query timeout
- **Batch Size Differentiation**: Supports custom batch sizes per request
- **Security Authentication**: Supports internal and external API authentication

### API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/v1/health` | Health check |
| GET | `/api/v1/drivers` | List all supported database drivers |
| GET | `/api/v1/datasources` | List all data sources |
| POST | `/api/v1/datasources` | Create data source |
| GET | `/api/v1/datasources/:id` | Get data source details |
| PUT | `/api/v1/datasources/:id` | Update data source |
| DELETE | `/api/v1/datasources/:id` | Delete data source |
| POST | `/api/v1/query` | Execute SQL query |
| POST | `/api/v1/query/validate` | Validate SQL query |
| POST | `/api/v1/datasources/:id/test` | Test database connection |
| GET | `/api/v1/datasources/:id/schema` | Get schema information |
| POST | `/api/v1/fetch` | Initial segmented query |
| GET | `/api/v1/fetch/{queryId}/{slug}/{token}` | Fetch next batch |

### Public Fetch API

#### POST /api/v1/fetch
Executes initial query, returns first batch of data and pagination information.

**Request Example:**
```bash
curl -X POST "http://localhost:8080/api/v1/fetch" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-token" \
  -d '{
    "dataSourceId": "12345678-1234-1234-1234-123456789012",
    "sql": "SELECT * FROM large_table WHERE created_at > \"2024-01-01\"",
    "type": 1,
    "batch_size": 1000,
    "timeout": 120
  }'
```

**Response Example:**
```json
{
  "code": "SUCCESS",
  "message": "OK",
  "data": {
    "queryId": "550e8400-e29b-41d4-a716-446655440000",
    "slug": "a1b2c3d4",
    "token": "e5f6g7h8-i9j0-k1l2-m3n4-o5p6q7r8s9t0",
    "executionMode": "pagination",
    "nextUri": "/api/v1/fetch/550e8400-e29b-41d4-a716-446655440000/a1b2c3d4/e5f6g7h8-i9j0-k1l2-m3n4-o5p6q7r8s9t0?batch_size=1000",
    "columns": [
      {
        "name": "id",
        "type": "integer",
        "nullable": false
      },
      {
        "name": "name",
        "type": "string",
        "nullable": true
      },
      {
        "name": "created_at",
        "type": "datetime",
        "nullable": false
      }
    ],
    "entries": [
      ["1", "Alice", "2024-01-01T10:00:00Z"],
      ["2", "Bob", "2024-01-01T11:00:00Z"],
      ["3", "Charlie", "2024-01-01T12:00:00Z"]
    ],
    "totalCount": 5000
  },
  "correlationId": "req-123456"
}
```

#### GET /api/v1/fetch/{query_id}/{slug}/{token}
Fetches next batch of data.

**Request Example:**
```bash
curl -X GET "http://localhost:8080/api/v1/fetch/550e8400-e29b-41d4-a716-446655440000/a1b2c3d4/e5f6g7h8-i9j0-k1l2-m3n4-o5p6q7r8s9t0?batch_size=2000" \
  -H "Authorization: Bearer your-token"
```

### Parameter Specifications

#### FetchQueryRequest Request Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `dataSourceId` | string | Yes | Data source ID (UUID format) |
| `sql` | string | Yes | SQL query statement (SELECT only) |
| `type` | integer | Yes | Query type: 1-sync query, 2-streaming query |
| `batchSize` | integer | No | Batch size per request, range: 1-10000, default 10000 |
| `batch_size` | integer | No | Batch size per request, range: 1-10000, default 10000 |
| `timeout` | integer | No | Query timeout (seconds), range: 1-1800, default 60 |

#### Query Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query_id` | string | Yes | Query ID |
| `slug` | string | Yes | Query signature |
| `token` | string | Yes | Query token |
| `batch_size` | integer | No | Current batch size, range: 1-10000, default 10000 |

### Response Data Format

#### FetchQueryResponse Response

| Field | Type | Description |
|-------|------|-------------|
| `queryId` | string | Query session ID |
| `slug` | string | Query signature |
| `token` | string | Authentication token |
| `nextUri` | string | Next batch data URI (present when more data available) |
| `columns` | array | Column information array |
| `entries` | array | Data rows array, each row is string array |
| `totalCount` | integer | Total number of rows |

#### ColumnInfo Column Information

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Column name |
| `type` | string | Data type |
| `nullable` | boolean | Whether NULL is allowed |

### Usage Flow

1. **Initiate Initial Query**: Call POST /api/v1/fetch with SQL and query parameters
2. **Get Pagination Information**: Retrieve `nextUri`, `queryId`, `slug`, and `token` from response
3. **Fetch Subsequent Batches**: Use pagination information to call GET interface for next batch
4. **Repeat Until Complete**: Continue calling until all data is retrieved (no more `nextUri`)

### Best Practices

#### 1. Batch Size Selection
- Small datasets: 100-1000 records
- Medium datasets: 1000-5000 records
- Large datasets: 5000-10000 records

#### 2. Timeout Settings
- Simple queries: 30-60 seconds
- Complex queries: 60-300 seconds
- Large datasets: 300-1800 seconds

#### 3. Error Handling
- Check response status codes and messages
- Handle session expiration situations
- Retry failed pagination requests

#### 4. Performance Optimization
- Use appropriate indexes
- Avoid SELECT *
- Filter with WHERE conditions during pagination

### Security Considerations

- All SQL queries undergo security validation
- Only SELECT statements allowed, data modification operations prohibited
- Supports IP whitelisting and request frequency limiting
- Internal API requires valid account authentication

### Limitations

1. Maximum batch size: 10,000 records
2. Maximum timeout: 1,800 seconds (30 minutes)
3. Session timeout: 30 minutes
4. Maximum query result set: depends on database configuration

### Troubleshooting

#### Common Errors

1. **INVALID_REQUEST**: Request parameter error
2. **DATASOURCE_NOT_FOUND**: Data source does not exist
3. **SECURITY_VIOLATION**: SQL security validation failed
4. **SESSION_EXPIRED**: Session expired
5. **INVALID_PARAMETERS**: Pagination parameter error

#### Debugging Suggestions

1. Check data source configuration and connection status
2. Validate SQL syntax and permissions
3. Confirm authentication information is valid
4. Monitor query performance and resource usage
