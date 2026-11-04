# Configuration Guide

## Overview

Data Fabric is configured primarily through a YAML configuration file located at `configs/config.yaml`. This document explains all available configuration options and their purposes.

## Tencent COS Avro data source

The cos_avro connector exposes one configured Tencent COS Avro Object
Container File (OCF) as a read-only SQL table. Set the bucket in database,
SecretId and SecretKey in username and password, region in
additionalProps.region, and the object key in additionalProps.object_key (or
the legacy host field). An HTTP(S) endpoint origin can be provided in
additionalProps.endpoint_url; otherwise the COS SDK uses the regional endpoint.
The https option defaults to true for the regional endpoint.

Optional additionalProps fields:

| Field | Default | Purpose |
|---|---:|---|
| table_name | Object filename without extension | SQL table name |
| https | true | Use HTTPS with the default regional endpoint |
| max_file_bytes | 16 MiB | Per-object read limit; configurable up to 64 MiB |
| max_rows | 100,000 | Maximum decoded records; this is also the hard maximum |

The adapter uses SecretId and SecretKey credentials; temporary COS credentials
and STS session tokens are not supported. It reads one OCF object into gateway
memory and supports the shared read-only SQL subset over top-level primitive
fields and nullable primitive unions. Nested and complex Avro fields, raw datum
streams, schema registries, directory scans, multiple objects, writes, and
streaming are not supported. The local COS protocol fixture covers signed
requests, schema discovery, bounded reads, cancellation, and SQL behavior; it
does not establish a live Tencent COS version contract.

## Tencent COS ORC data source

The cos_orc connector exposes one configured Tencent COS .orc object as a
read-only SQL table. Set the bucket in database, SecretId and SecretKey in
username and password, region in additionalProps.region, and the object key in
additionalProps.object_key (or the legacy host field). An HTTP(S) endpoint
origin can be provided in additionalProps.endpoint_url; otherwise the COS SDK
uses the regional endpoint. The https option defaults to true and controls the
scheme used for the regional endpoint.

Optional additionalProps fields:

| Field | Default | Purpose |
|---|---:|---|
| table_name | Object filename without extension | SQL table name |
| https | true | Use HTTPS with the default regional endpoint |
| max_file_bytes | 16 MiB | Per-object read limit; configurable up to 64 MiB |
| max_rows | 100,000 | Maximum decoded rows; this is also the hard maximum |

The adapter uses SecretId and SecretKey credentials; temporary COS credentials
and STS session tokens are not supported by this baseline. It reads one ORC
object into gateway memory and supports the shared read-only SQL subset over
top-level primitive columns. Nested and repeated ORC
fields, directory scans, multiple objects, writes, and streaming are not
supported. The local COS protocol fixture covers signed requests, schema
discovery, bounded reads, cancellation, and SQL behavior; it does not establish
a live Tencent COS version contract.

## Alibaba OSS CSV data source

The `oss_csv` connector exposes one configured OSS `.csv` object as a
read-only SQL table. Set the bucket in `database`, the access key ID and secret
in `username` and `password`, and the object key in
`additionalProps.object_key` (or the legacy `host` field). Set
`additionalProps.endpoint_url` to the OSS HTTP(S) endpoint origin. The
`endpoint` property is accepted as an alias.

Optional `additionalProps` fields:

| Field | Default | Purpose |
|---|---:|---|
| `table_name` | Object filename without extension | SQL table name |
| `session_token` | — | STS session token (`security_token` is an alias) |
| `force_path_style` | `false` | Use path-style bucket addressing |
| `delimiter` | `,` | One-character CSV delimiter |
| `has_header` | `true` | Treat the first non-skipped record as column names |
| `skip_header_rows` | `0` | Number of initial records to skip |
| `null_values` | — | CSV values treated as SQL NULL |
| `max_file_bytes` | 16 MiB | Per-object read limit; configurable up to 64 MiB |

The adapter reads one object into gateway memory and supports the shared
read-only CSV SQL subset. It rejects writes, files over the configured byte
limit, and results with more than 100,000 CSV rows. The local OSS protocol
fixture validates the signed SDK request path; it does not claim a live OSS
service version contract.

## Alibaba OSS JSON data source

The `oss_json` connector exposes one configured OSS JSON object as a read-only
SQL table. Set the bucket in `database`, the access key ID and secret in
`username` and `password`, and the object key in `additionalProps.object_key`
(or the legacy `host` field). Set the required endpoint origin in
`additionalProps.endpoint_url`; `additionalProps.endpoint` is an alias. Object
keys must end in `.json`, `.jsonl`, or `.ndjson`.

Optional `additionalProps` fields:

| Field | Default | Purpose |
|---|---:|---|
| `table_name` | Object filename without extension | SQL table name |
| `session_token` | — | Optional STS token (`security_token` is an alias) |
| `force_path_style` | `false` | Use path-style bucket addressing |
| `flatten_nested` | `false` | Expose supported nested scalar fields as underscore-separated columns; collisions and arrays are rejected |
| `max_file_bytes` | 16 MiB | Per-object read limit; configurable up to 64 MiB |
| `max_records` | 100,000 | Maximum decoded records; this is also the hard maximum |

JSON objects, arrays of records, and JSON Lines are supported. By default only
top-level scalar fields are queryable; `flatten_nested=true` enables supported
nested scalar fields. The adapter downloads one object into gateway memory and
uses the shared read-only SQL subset. Prefix scans, multiple objects, writes,
and streaming are not supported. The local OSS SDK fixture covers signed
requests and STS token propagation; it does not establish a live OSS version
contract.

## Alibaba OSS Avro data source

The oss_avro connector exposes one configured Alibaba OSS Avro Object
Container File (OCF) as a read-only SQL table. Set the bucket in database,
the access key ID and secret in username and password, the object key in
additionalProps.object_key (or the legacy host field), and the required
HTTP(S) endpoint origin in additionalProps.endpoint_url. The endpoint
property is accepted as an alias. Object keys must end in .avro.

Optional additionalProps fields:

| Field | Default | Purpose |
|---|---:|---|
| table_name | Object filename without extension | SQL table name |
| session_token | - | Optional STS token (security_token is an alias) |
| force_path_style | false | Use path-style bucket addressing |
| max_file_bytes | 16 MiB | Per-object read limit; configurable up to 64 MiB |
| max_rows | 100,000 | Maximum decoded rows; this is also the hard maximum |

The adapter reads one OCF object into gateway memory and supports the shared
read-only SQL subset over top-level primitive fields and nullable primitive
unions. Nested and other complex Avro field types, directory scans, multiple
objects, raw datum streams, writes, and streaming are not supported. The local
OSS protocol fixture covers signed requests and STS token propagation; it does
not establish a live OSS version contract.

## Alibaba OSS ORC data source

The oss_orc connector exposes one configured Alibaba OSS .orc object as a
read-only SQL table. Set the bucket in database, the access key ID and secret
in username and password, and the object key in
additionalProps.object_key (or the legacy host field). Set the required
HTTP(S) endpoint origin in additionalProps.endpoint_url; endpoint is an
alias. Object keys must end in .orc.

Optional additionalProps fields:

| Field | Default | Purpose |
|---|---:|---|
| table_name | Object filename without extension | SQL table name |
| session_token |  | Optional STS token (security_token is an alias) |
| force_path_style | false | Use path-style bucket addressing |
| max_file_bytes | 16 MiB | Per-object read limit; configurable up to 64 MiB |
| max_rows | 100,000 | Maximum decoded rows; this is also the hard maximum |

The adapter reads one ORC object into gateway memory and supports the shared
read-only SQL subset over top-level primitive columns. Nested and repeated ORC
fields, directory scans, multiple objects, writes, and streaming are not
supported. The local OSS protocol fixture covers signed requests, STS token
propagation, schema discovery, bounded reads, and SQL behavior; it does not
establish a live OSS version contract.

## Tencent COS CSV data source

The `cos_csv` connector exposes one configured Tencent COS `.csv` object as a
read-only SQL table. Set the bucket in `database`, SecretId and SecretKey in
`username` and `password`, the region in `additionalProps.region`, and the
object key in `additionalProps.object_key` (or the legacy `host` field).
`additionalProps.endpoint_url` is an optional HTTP(S) endpoint override.
COS uses HTTPS for the standard regional endpoint. When `endpoint_url` is set,
its scheme controls whether the connection uses TLS.

Optional `additionalProps` fields:

| Field | Default | Purpose |
|---|---:|---|
| `table_name` | Object filename without extension | SQL table name |
| `https` | `true` | TLS for the standard regional endpoint; `endpoint_url` takes precedence |
| `delimiter` | `,` | One-character CSV delimiter |
| `has_header` | `true` | Treat the first non-skipped record as column names |
| `skip_header_rows` | `0` | Number of initial records to skip |
| `null_values` | — | CSV values treated as SQL NULL |
| `max_file_bytes` | 16 MiB | Per-object read limit; configurable up to 64 MiB |

The adapter reads one object into gateway memory and supports the shared
read-only CSV SQL subset. It rejects writes, objects over the configured byte
limit, and results with more than 100,000 rows. The local COS protocol fixture
checks signed requests but does not claim a live COS service version contract.

## Tencent COS JSON data source

The `cos_json` connector exposes one configured Tencent COS JSON object as a
read-only SQL table. Set the bucket in `database`, SecretId and SecretKey in
`username` and `password`, the region in `additionalProps.region`, and the
object key in `additionalProps.object_key` (or the legacy `host` field). The
object key must end in `.json`, `.jsonl`, or `.ndjson`. An optional
`additionalProps.endpoint_url` overrides the regional COS endpoint.

Optional `additionalProps` fields:

| Field | Default | Purpose |
|---|---:|---|
| `table_name` | Object filename without extension | SQL table name |
| `https` | `true` | TLS for the standard regional endpoint; `endpoint_url` takes precedence |
| `flatten_nested` | `false` | Expose supported nested scalar fields as underscore-separated columns; collisions and arrays are rejected |
| `max_file_bytes` | 16 MiB | Per-object read limit; configurable up to 64 MiB |
| `max_records` | 100,000 | Maximum decoded records; this is also the hard maximum |

JSON objects, arrays of records, and JSON Lines are supported. By default only
top-level scalar fields are queryable; `flatten_nested=true` enables supported
nested scalar fields. The adapter downloads one object into gateway memory and
uses the shared read-only SQL subset. Prefix scans, multiple objects, writes,
and streaming are not supported. Local SDK fixtures cover signed COS requests
and the configured limits; they do not establish a live Tencent COS version
contract.

## Access audit events

REST API and MCP access outcomes are stored in the metadata database when
auditing is enabled. It is enabled by default with a 90-day retention window;
adjust `audit.retention_days` to match the deployment policy. Release mode does
not permit disabling auditing. See [Access audit events](security/audit-events.md)
for recorded fields, admin access, and filters.

## Main Configuration File

### Example Configuration (`configs/config.yaml`)

```yaml
server:
  port: "8080"
  mode: "debug"
  host: "0.0.0.0"
  read_header_timeout: "10s"
  read_timeout: "30s"
  write_timeout: "31m"
  idle_timeout: "120s"
  max_request_body_bytes: 1048576

database:
  host: "localhost"
  port: "3306"
  database: "gateway_db"
  username: "gateway_user"
  password: ""
  ssl: "false"

security:
  # Required for the HTTP API; keep this in a secret manager or environment.
  jwt_secret: ""
  jwt_expiration: "24h"
  rate_limit_per_minute: 60
  rate_limit_burst: 10
  enable_auth: true
  enable_rate_limit: true
  enforce_read_only: true

logging:
  level: "info"
  format: "json"

query:
  prefer_streaming: true
  execution_mode: "auto"

mcp:
  enabled: false
  transport: "stdio"
  address: "127.0.0.1:8081"
  auth_mode: "local-jwt"
  oidc_issuer: ""
  oidc_audience: ""
  oidc_required_scopes: "data-fabric.query"
  oidc_scope_claim: "scope"
  oidc_tenant_claim: "tenant"
  oidc_roles_claim: "roles"
  oidc_username_claim: "preferred_username"
  oidc_jwks_refresh_interval: "5m"

sql_translation:
  enabled: true
  timeout: "5s"
  max_concurrent: 4
  max_sql_bytes: 1048576
  max_output_bytes: 1048576
  python_path: "python3"
  script_path: "./scripts/sql_translate.py"
  python_module_path: "/opt/data-fabric/python"
```

Copy `configs/config.yaml.example` to `configs/config.yaml` for local use. Keep real secrets out of source control.

## Configuration Sections

### Server Configuration

| Field | Type | Default |
|-------|------|---------|
| `server.port` | String | `8080` |
| `server.mode` | String | `debug` (`debug`, `test`, or `release`) |
| `server.host` | String | `0.0.0.0` |

`server.host` is the actual bind address of the HTTP listener (#127): an IP
address (IPv4 or bracket-escaped IPv6) or a hostname the listener resolves.
An empty value binds every interface, which keeps container deployments
working. An invalid or already-occupied address fails startup with the
address in the error.
| `server.read_header_timeout` | Duration | `10s` |
| `server.read_timeout` | Duration | `30s` |
| `server.write_timeout` | Duration | `31m` |
| `server.idle_timeout` | Duration | `120s` |
| `server.max_request_body_bytes` | Integer | `1048576` (1 MiB) |

The body limit returns HTTP 413 with error code `REQUEST_TOO_LARGE`. It wraps JSON request endpoints; fetch continuation uses GET and is unaffected.

### Database Configuration

| Field | Type | Default |
|-------|------|---------|
| `database.host` | String | `localhost` |
| `database.port` | String | `3306` |
| `database.database` | String | `gateway_db` |
| `database.username` | String | `gateway_user` |
| `database.password` | String | Empty |
| `database.ssl` | String | `false` |

`database.ssl` controls TLS for the metadata database connection (#143):
`false` (default) connects in plaintext, `true` requires TLS and verifies the
server certificate against the system roots and the connected hostname, and
`skip-verify` requires TLS without certificate verification for self-signed
servers. Any other value fails startup.

### Security Configuration

| Field | Type | Default |
|-------|------|---------|
| `security.jwt_secret` | String | None; required by the HTTP server |
| `security.jwt_expiration` | Duration | `24h` |
| `security.rate_limit_per_minute` | Integer | `60` |
| `security.rate_limit_burst` | Integer | `10` |
| `security.enable_auth` | Boolean | `true` |
| `security.enable_rate_limit` | Boolean | `true` |
| `security.enforce_read_only` | Boolean | `true` |

In `release` mode, authentication and read-only session enforcement cannot be disabled. The fetch API always requires a valid JWT, so the HTTP server requires a non-placeholder `security.jwt_secret` of at least 32 bytes in every mode.

### Logging, Query, and MCP Configuration

| Field | Type | Default |
|-------|------|---------|
| `logging.level` | String | `info` |
| `logging.format` | String | `json` |
| `query.prefer_streaming` | Boolean | `true` |
| `query.execution_mode` | String | `auto` (`auto`, `streaming`, or `pagination`) |
| `query.max_concurrent_queries_per_tenant` | Integer | `25`; limits combined active streams and in-flight paginated queries per tenant (falls back to user ID when no tenant claim is present) |
| `query.max_concurrent_streams_per_tenant` | Integer | Deprecated alias for `query.max_concurrent_queries_per_tenant` |
| `query.rate_limit_per_minute_per_tenant` | Integer | `120`; shared query execution and continuation request budget per tenant |
| `query.rate_limit_burst_per_tenant` | Integer | `20`; initial burst capacity for the per-tenant query request budget |
| `mcp.enabled` | Boolean | `false` |
| `mcp.transport` | String | `stdio` |
| `mcp.address` | String | `127.0.0.1:8081` |
| `mcp.max_concurrent_requests` | Integer | `64`; caps in-flight requests on the streamable-HTTP entrance (body size falls back to `server.max_request_body_bytes`) |
| `mcp.auth_mode` | String | `local-jwt` (development); `oidc` for remote MCP in release mode |
| `mcp.oidc_issuer` | HTTPS URL | Empty; required for OIDC |
| `mcp.oidc_audience` | String | Empty; required for OIDC |
| `mcp.oidc_required_scopes` | Space-separated scopes | `data-fabric.query` |
| `mcp.oidc_scope_claim` | Claim path (string or string array) | `scope` |
| `mcp.oidc_tenant_claim` | Claim path | `tenant` |
| `mcp.oidc_roles_claim` | Claim path | `roles` |
| `mcp.oidc_username_claim` | Claim path | `preferred_username` |
| `mcp.oidc_jwks_refresh_interval` | Duration | `5m` (1m–1h) |

In `auto` mode, `type: 1` explicitly keeps a request on pagination. `type: 2`
requests streaming. Requests without a type use pagination for batches below
1,000 rows or SQL with an explicit result limit of 1,000 rows or fewer. Larger
unbounded batches use streaming only when `query.prefer_streaming` is true and
the registered source driver advertises streaming support. Unsupported drivers
fall back to pagination. If the stream concurrency limit is reached before the
query starts, auto mode also falls back; errors after execution begins are
returned without retrying the SQL.

The response includes `executionMode` with the engine that handled the request
(`streaming` or `pagination`). Explicit `query.execution_mode: streaming` and
`pagination` force the configured path; `auto` applies the selection rules
above.

The gateway enforces a shared process-wide cap of 100 query operations. A
stream keeps its reservation while the result cursor is open; a paginated query
holds a reservation only while its initial query or one continuation batch is
using the source. Limits are process-local and are not shared across replicas.
Set `query.max_concurrent_queries_per_tenant` to a positive integer to tune the
tenant cap. The older `query.max_concurrent_streams_per_tenant` key remains
supported as a deprecated alias.

Query execution and continuation requests also share a process-local token
bucket per tenant across HTTP and MCP. The default budget is 120 requests per
minute with a burst of 20; tenant-less authenticated callers are bucketed by
user ID. The process keeps at most 10,000 tenant/user buckets. A new identity
without a bucket receives the standard HTTP 429 `QUERY_RATE_LIMIT_EXCEEDED`
response (or the same stable message through MCP) while the table is full;
existing identities keep their independent budgets. Idle buckets older than 10
minutes are pruned at most once per minute, allowing new identities to use the
freed capacity. Set `query.rate_limit_per_minute_per_tenant` and
`query.rate_limit_burst_per_tenant` to `0` to disable this query-specific
budget. Rejections happen before the connector is used. The bucket table does
not coordinate across gateway replicas.


## SQL dialect translation runtime

SQL translation is enabled by default. Before the metadata database is opened,
startup checks that the configured interpreter, translation script, and SQLGlot
dependency are available. SQLGlot is also required for dialect-aware read-only
SQL validation, even when translation is disabled. The release container
includes Python 3 and the pinned dependency; for local development, install it with
python3 -m pip install --requirement requirements-sql-translation.txt. Set
sql_translation.enabled: false to disable translation. In that mode, the
HTTP translation endpoints return 503, MCP does not advertise translation
tools, and cross-dialect query requests fail closed; same-dialect query
requests continue to use SQLGlot for read-only parsing and security checks.

| Field | Type | Default | Limit |
|-------|------|---------|-------|
| <code>sql_translation.enabled</code> | Boolean | true | — |
| <code>sql_translation.timeout</code> | Duration | 5s | 0 &lt; value &lt;= 1m |
| <code>sql_translation.max_concurrent</code> | Integer | 4 | 1..32 per process |
| <code>sql_translation.max_sql_bytes</code> | Integer | 1048576 | 1..16777216 |
| <code>sql_translation.max_output_bytes</code> | Integer | 1048576 | 1..16777216; stderr separately capped at 16 KiB |
| <code>sql_translation.python_path</code> | String | python3 | Required when enabled |
| <code>sql_translation.script_path</code> | String | ./scripts/sql_translate.py | Required when enabled |
| <code>sql_translation.python_module_path</code> | String | /opt/data-fabric/python | SQLGlot module search path |

All SQL translation entry points in one service process share the same timeout,
concurrency, and size limits. Request cancellation and timeout terminate the
translation subprocess. The subprocess receives only PATH, PYTHONPATH, PYTHONUNBUFFERED, and
PYTHONNOUSERSITE; application secrets are not copied into its environment.

## S3 ORC object source

The S3 ORC connector exposes one configured object as one read-only SQL table.
It accepts a flat ORC struct with primitive fields. The current reader supports
uncompressed, ZLIB, and Snappy ORC stripes; nested/repeated fields and other
compression codecs are rejected. Object bytes are fetched with ranged S3
requests, while a query currently decodes the full object into gateway memory
before applying filters, ordering, and pagination. Keep this path for bounded
files; it is not a distributed scan engine.

| Data-source field | S3 ORC meaning |
| --- | --- |
| database | Bucket name. |
| host or additionalProps.object_key | Object key; object_key takes precedence. |
| username and password | Optional S3 access key and secret key; configure both or neither. |
| additionalProps.region | AWS region; defaults to us-east-1. |
| additionalProps.endpoint_url | Optional S3-compatible endpoint. |
| additionalProps.force_path_style | Enable path-style addressing for compatible services such as Garage. |
| additionalProps.table_name | SQL table name; defaults from the object key. |
| additionalProps.session_token | Optional temporary credential session token. |
| additionalProps.role_arn and external_id | Optional AWS role assumption settings. |

The read-only query subset supports column projection, scalar filters joined by
AND, IS NULL, ORDER BY, LIMIT/OFFSET, and COUNT(*). Writes, joins, grouping,
nested ORC values, multi-object scans, and partition discovery are not
supported. The S3-compatible contract is pinned to Garage v2.4.1; that result
does not establish compatibility with every S3 provider or release. See the
support catalog and driver contract report for the current evidence.

## S3 JSON object source

The S3 JSON connector exposes one configured object as one read-only SQL
table. It accepts a single JSON object, an array of JSON objects, or JSON Lines
and NDJSON. Schema inference examines every record: fields missing from some
records and explicit nulls are nullable; integer and floating-point values
promote to a floating-point column. Incompatible types are rejected instead
of silently coercing values.

| Data-source field or property | S3 JSON meaning |
| --- | --- |
| database | Bucket name. |
| host or additionalProps.object_key | Object key; object_key takes precedence. |
| username and password | Optional S3 access key and secret key; configure both or neither. |
| additionalProps.region | AWS region; defaults to us-east-1. |
| additionalProps.endpoint_url | Optional S3-compatible endpoint. |
| additionalProps.force_path_style | Enable path-style addressing for compatible services such as Garage. |
| additionalProps.table_name | SQL table name; defaults from the object key. |
| additionalProps.flatten_nested | Flatten nested object fields into underscore-separated columns; name collisions are rejected. Arrays remain unsupported. |
| additionalProps.session_token | Optional temporary credential session token. |
| additionalProps.role_arn and external_id | Optional AWS role assumption settings. |

The read-only SQL subset supports top-level scalar projections, scalar filters
joined by AND, IS NULL, ORDER BY, LIMIT/OFFSET, and COUNT(*). Nested objects
require flatten_nested; arrays, heterogeneous values, writes, joins, grouping,
multi-object scans, partition discovery, and S3 Select execution are not
supported. Each query downloads the full configured object into gateway
memory before parsing and applying filters or pagination. The S3-compatible
contract is pinned to Garage v2.4.1; it does not establish compatibility with
every S3 provider or release. See the support catalog and driver contract
report for current evidence.

## S3 Avro OCF object source

The S3 Avro connector exposes one Avro Object Container File (OCF) as one
read-only SQL table. It reads the writer schema from the OCF header and accepts
flat records whose fields are primitive values or nullable unions containing
null and one primitive value. Nested records, arrays, maps, named unions,
schema registries, raw Avro datum streams, and scans across multiple objects
are outside this adapter's boundary. On a cache miss, the gateway downloads and
decodes the full configured object into memory before applying filters or
pagination. Decoded results with the same ETag can be reused for up to five
seconds across count and page requests, avoiding repeated downloads within a
single query flow.

| Data-source field or property | S3 Avro meaning |
| --- | --- |
| database | Bucket name. |
| host or additionalProps.object_key | Object key; object_key takes precedence. |
| username and password | Optional S3 access key and secret key; configure both or neither. |
| additionalProps.region | AWS region; defaults to us-east-1. |
| additionalProps.endpoint_url | Optional S3-compatible endpoint. |
| additionalProps.force_path_style | Enable path-style addressing for compatible services such as Garage. |
| additionalProps.table_name | SQL table name; defaults from the object key. |
| additionalProps.session_token | Optional temporary credential session token. |
| additionalProps.role_arn and external_id | Optional AWS role assumption settings. |

The read-only SQL subset supports column projection, scalar filters joined by
AND, IS NULL, ORDER BY, LIMIT/OFFSET, and COUNT(*). Writes, joins, grouping,
nested Avro values, multi-object scans, and partition discovery are not
supported. LIMIT 0 reads the OCF header to return schema without decoding
records. The S3-compatible contract is pinned to Garage v2.4.1; it does not
establish compatibility with every S3 provider or release. See the support
catalog and driver contract report for the current evidence.

## Iceberg REST table source

The Iceberg connector uses the Iceberg REST catalog to load the current table
snapshot and reads the referenced data files in the gateway. It is registered
as experimental until a live, versioned catalog and storage contract test is
recorded. Check [the support catalog](data-source-support.md) before relying on
it in production.

Configure a data source with these fields:

| Data-source field | Iceberg meaning |
| --- | --- |
| `host` and `port` | REST catalog host and port; the default port is `8181`. |
| `ssl` | Selects HTTPS when `host` is a hostname. |
| `database` | REST warehouse identifier; `additionalProps.warehouse` overrides it. |
| `schema` | Default namespace for unqualified table names; `additionalProps.namespace` overrides it. |
| `username` and `password` | OAuth client ID and secret, sent as the REST credential when no explicit credential is configured. |

`additionalProps` accepts `catalog_uri` to override the full HTTP(S) catalog
URL, `token` for a static bearer token, `credential` for the REST OAuth
credential, and `oauth2-server-uri` for a custom token endpoint. A configured
`token` takes precedence over `credential`; otherwise `username:password` is
used when both are present. Storage settings with `s3.*` and REST client
settings with `rest.*` are forwarded to the Iceberg client. Keep tokens,
credentials, and cloud secrets in the normal encrypted data-source credential
flow.

`additionalProps.max_rows` sets the maximum rows returned by one SQL scan. It
defaults to `10000` and must be between `1` and `100000`. Queries must include
`LIMIT` so scans remain bounded; `OFFSET + LIMIT` cannot exceed `100000`. The
read-only SQL subset accepts one table, column projections, `AND`-combined
scalar comparisons and `IS NULL` checks, `LIMIT`/`OFFSET`, and `COUNT(*)`.
Joins, `OR`, `ORDER BY`, grouping, other aggregates, and writes are rejected
with an explicit error. `COUNT(*)` streams rows and may scan the full filtered
table when there is no inner limit.

## Apache Druid SQL source

The Druid adapter sends read-only SQL to the configured Druid Router or Broker
through the Druid SQL HTTP API. It is experimental because its gateway contract
uses an in-process protocol fixture rather than a pinned live Druid cluster;
see the [support catalog](data-source-support.md) for the current boundary.

| Data-source field | Druid meaning |
| --- | --- |
| `host` and `port` | Router or Broker hostname and HTTP port; the default port is `8082`. A host may also include an explicit port. |
| `ssl` | Uses HTTPS instead of HTTP when `host` is a hostname. |
| `username` and `password` | Optional HTTP basic authentication credentials. |
| `database` and `schema` | Accepted by the common data-source model but not sent to Druid; qualify Druid datasources in SQL where needed. |

`additionalProps.scheme` can explicitly select `http` or `https`. The adapter
accepts one top-level `SELECT` or `EXPLAIN` statement and uses Druid dynamic
parameters for bound values. DML, transactions, native JSON queries, async
statements, and streaming are unavailable on this path. Use a Druid account
with only the datasource permissions required by the gateway; application
validation and adapter-level rejection complement those source permissions.

## MinIO CSV source

MinIO CSV uses the same read-only SQL adapter as S3-compatible CSV sources.
Register the source with type `minio_csv`; the gateway treats `database` as the
bucket and reads one configured object. It supports the documented restricted
SELECT subset, pagination, bound parameters, and write rejection. The complete
object is downloaded into gateway memory for each query, so set source-side
object sizes and gateway memory limits accordingly. Table discovery reports the
single configured table. Describing it reads the object and infers column types
from its contents; columns with no non-null values default to `VARCHAR`.

| Data-source field or property | MinIO CSV meaning |
| --- | --- |
| `database` | MinIO bucket name. |
| `username` and `password` | MinIO access key and secret key. |
| `additionalProps.endpoint_url` | Full MinIO endpoint, such as `http://minio.internal:9000`. |
| `additionalProps.force_path_style` | Set to `true` for MinIO. |
| `additionalProps.object_key` | Required CSV object key. `host` is accepted only as a legacy fallback. |
| `additionalProps.table_name` | SQL table name; defaults to the object filename without `.csv`. |
| `additionalProps.region` | S3 signing region; defaults to `us-east-1`. |
| `additionalProps.has_header`, `delimiter`, `skip_header_rows`, `null_values` | Optional CSV parsing controls shared with S3 CSV. |

The adapter has deterministic connection-pool and unified-query coverage plus a
pinned MinIO `RELEASE.2025-04-22T22-12-26Z` contract for access-key
authentication. TLS, STS, distributed MinIO deployments, and other releases
remain outside that baseline; consult the [support catalog](data-source-support.md)
before production use.

## S3 Delta source

The s3_delta source exposes one configured S3-hosted Delta table as a read-only
SQL table. It replays consecutive JSON transaction-log commits from version zero
and reads the active Parquet files. The adapter accepts unpartitioned tables
with flat primitive columns and the shared object SQL subset: projection, simple
comparisons joined by AND, IS NULL, IN, LIKE, ORDER BY, LIMIT, OFFSET, and
COUNT(*).

| Data-source field or property | S3 Delta meaning |
| --- | --- |
| database | S3 bucket name. |
| username and password | Optional access key and secret key; configure both together. If omitted, the AWS SDK credential chain is used. |
| additionalProps.endpoint_url | Optional S3-compatible HTTP(S) endpoint origin. |
| additionalProps.force_path_style | Set to true for compatible services that require path-style addressing. |
| additionalProps.table_path | Delta table root within the bucket; host is accepted as a fallback. |
| additionalProps.table_name | SQL table name; defaults to the final table-path component. |
| additionalProps.region | S3 signing region; defaults to us-east-1. |
| additionalProps.session_token | Optional session token for temporary static credentials. |
| additionalProps.role_arn and external_id | Optional role-assumption settings supported by the shared S3 client. |
| additionalProps.batch_size | Number of Parquet rows decoded per batch; defaults to 1000 and is capped at 10000. |

The adapter rejects checkpoints, partitioned tables, deletion vectors, empty
tables, unknown transaction-log actions, and reader features beyond protocol
version 1. It also rejects snapshots with more than 256 active data files, more
than 64 MiB of active Parquet data, or more than 100,000 decoded rows. Active
files are downloaded into gateway memory before SQL filtering and pagination;
decoded rows add memory use beyond the compressed object sizes. The local signed
S3 protocol fixture does not establish compatibility with AWS S3 or any other
provider release.

## MinIO Delta source

The minio_delta source exposes one configured Delta table as a read-only SQL
table. It resolves the latest snapshot by replaying consecutive JSON transaction
log commits from version zero and reads active Parquet files. It accepts
unpartitioned tables with flat primitive columns and the shared object SQL
subset: projection, simple comparisons joined by AND, IS NULL, IN, LIKE,
ORDER BY, LIMIT, OFFSET, and COUNT(*).

| Data-source field or property | MinIO Delta meaning |
| --- | --- |
| database | MinIO bucket name. |
| username and password | Required MinIO access key and secret key. |
| additionalProps.endpoint_url | HTTP(S) MinIO endpoint origin. |
| additionalProps.table_path | Delta table root within the bucket; host is accepted as a fallback. |
| additionalProps.table_name | SQL table name; defaults to the final path component. |
| additionalProps.region | S3 signing region; defaults to us-east-1. |
| additionalProps.session_token | Optional S3-compatible session token. |
| additionalProps.batch_size | Number of Parquet rows decoded per batch; defaults to 1000 and is capped at 10000. |

The adapter rejects checkpoints, partitioned tables, deletion vectors, empty
tables, unknown transaction-log actions, and reader features beyond protocol
version 1. It also rejects snapshots with more than 256 active data files, more
than 64 MiB of active Parquet data, or more than 100,000 decoded rows. Active
files are downloaded into gateway memory before SQL filtering and pagination;
decoded rows add memory use beyond the compressed object sizes. Local MinIO SDK
fixtures do not establish compatibility with a live MinIO release.

## MinIO JSON source

Register a minio_json source to expose one MinIO-hosted JSON object as a
read-only SQL table. The shared adapter accepts a JSON object, an array of JSON
objects, or JSON Lines/NDJSON in that object.

| Data-source field or property | MinIO JSON meaning |
| --- | --- |
| database | Required bucket name. |
| host or additionalProps.object_key | Required object key. If table_name is omitted, the SQL table name is inferred from the object filename. |
| username and password | Required MinIO access key and secret key. |
| additionalProps.endpoint_url | Required HTTP(S) MinIO endpoint origin; credentials, paths, query strings, and fragments are not allowed. |
| additionalProps.region | S3 signing region; defaults to us-east-1. |
| additionalProps.session_token | Optional temporary S3 credential token. |
| additionalProps.force_path_style | Defaults to true for MinIO-compatible services. |
| additionalProps.table_name | Optional SQL table name; defaults to the object filename without its extension. |
| additionalProps.flatten_nested | Defaults to false; when true, nested object fields are flattened into column names. |

The SQL adapter supports projection, supported scalar filters, ORDER BY,
LIMIT/OFFSET, and COUNT(*). It downloads the full object into gateway memory
before parsing and applying SQL filters or pagination. It supports top-level
scalar fields with stable types; nested object flattening is optional, while
array-valued fields and flattened-name collisions are rejected. Multiple
objects, partition scans, S3 Select, and writes are not supported. The local
signed S3-compatible fixture does not establish compatibility with a live
MinIO release; see the support catalog.

## MinIO Avro OCF source

Register a minio_avro source to expose one Avro Object Container File (OCF)
object stored in MinIO as a read-only SQL table. It uses the shared S3-compatible
Avro reader and SQL adapter.

| Data-source field or property | MinIO Avro meaning |
| --- | --- |
| database | Required bucket name. |
| host or additionalProps.object_key | Required object key. If table_name is omitted, the SQL table name is inferred from the object filename. |
| username and password | Required MinIO access key and secret key. |
| additionalProps.endpoint_url | Required HTTP(S) MinIO endpoint origin; credentials, paths, query strings, and fragments are not allowed. |
| additionalProps.region | S3 signing region; defaults to us-east-1. |
| additionalProps.session_token | Optional temporary S3 credential token. |
| additionalProps.force_path_style | Defaults to true for MinIO-compatible services. |
| additionalProps.table_name | Optional SQL table name; defaults to the object filename without its extension. |

The SQL adapter supports projection, its shared scalar filters, ORDER BY,
LIMIT/OFFSET, and COUNT(*). It exposes one configured flat-record OCF object
and downloads the full object into gateway memory before decoding and applying
SQL filters or pagination. Nested records, arrays, maps, named unions, schema
registries, multiple objects, partition scans, and writes are not supported.
Only null, deflate, and Snappy OCF codecs are supported. The local signed
S3-compatible fixture does not establish compatibility with a live MinIO
release; see the support catalog.

## MinIO ORC source

Register a minio_orc source to expose one ORC object stored in MinIO as a
read-only SQL table. It uses the shared S3-compatible ORC reader and SQL adapter.

| Data-source field or property | MinIO ORC meaning |
| --- | --- |
| database | Required bucket name. |
| host or additionalProps.object_key | Required object key. If table_name is omitted, the SQL table name is inferred from the object filename. |
| username and password | Required MinIO access key and secret key. |
| additionalProps.endpoint_url | Required HTTP(S) MinIO endpoint origin; credentials, paths, query strings, and fragments are not allowed. |
| additionalProps.region | S3 signing region; defaults to us-east-1. |
| additionalProps.session_token | Optional temporary S3 credential token. |
| additionalProps.force_path_style | Defaults to true for MinIO-compatible services. |
| additionalProps.table_name | Optional SQL table name; defaults to the object filename without its extension. |

The SQL adapter supports projection, its shared scalar filters, ORDER BY,
LIMIT/OFFSET, and COUNT(*). It exposes one configured object. ORC data is read
through byte ranges but decoded in gateway memory before SQL filtering and
pagination. Nested/repeated columns, multi-object scans, partition discovery,
and writes are not supported. The local signed S3-compatible fixture does not
establish compatibility with a live MinIO release; see the support catalog.

## MinIO Iceberg source

The minio_iceberg source routes one configured table through an external
Iceberg REST catalog and uses MinIO as the S3-compatible Iceberg FileIO store.
Data Fabric does not run an Iceberg catalog for this source.

| Data-source field or property | MinIO Iceberg meaning |
| --- | --- |
| host or additionalProps.catalog_uri | HTTP(S) Iceberg REST catalog URL. A URL path is allowed; credentials, query strings, and fragments are not. |
| database or additionalProps.warehouse | Iceberg warehouse name. |
| schema or additionalProps.namespace | Iceberg namespace; defaults to schema. |
| additionalProps.table_name | Required, unqualified SQL table name exposed by this source. |
| username and password | Required MinIO access key and secret key for Iceberg S3 FileIO. |
| additionalProps.endpoint_url or s3.endpoint | Required MinIO S3 endpoint origin. |
| additionalProps.region | S3 signing region; defaults to us-east-1. |
| additionalProps.session_token | Optional MinIO S3 session token. |
| additionalProps.force_path_style | Defaults to true for MinIO-compatible endpoints. |
| additionalProps.catalog_token or catalog_credential | Optional REST catalog bearer token or credential, separate from MinIO storage credentials. |
| additionalProps.max_rows | Iceberg SQL scan limit; defaults to 10000 and can be configured up to 100000. |

The adapter exposes only the configured table and supports the shared Iceberg
read-only SQL subset: projections, supported AND filters, LIMIT/OFFSET, and
COUNT(*). Writes, catalog-wide table enumeration, ORDER BY, and direct
metadata-file discovery are not supported. A pinned live MinIO contract verifies S3 FileIO metadata/data object reads and the configured query path through a local REST catalog fixture. It does not
establish compatibility with an external REST Catalog server; see the driver
contract test report.

## MinIO Parquet source

Register a `minio_parquet` source to expose one configured Parquet object as a
read-only SQL table. It uses the common query and metadata paths and supports
flat primitive schemas with projection, `WHERE` comparisons joined by `AND`,
`IS NULL`, `IN`, `LIKE`, `ORDER BY`, `LIMIT`, `OFFSET`, and `COUNT(*)`.
Use the configured table name directly without a schema qualifier or table
alias. Nested/repeated fields, partitions, and queries spanning multiple
objects are not supported. The reader uses ranged object requests to inspect the Parquet
footer, but currently decodes the configured object's rows before applying SQL
filters and pagination; query limits therefore do not cap all object data
reads.

| Data-source field or property | MinIO Parquet meaning |
| --- | --- |
| `database` | MinIO bucket name. |
| `username` and `password` | MinIO access key and secret key. |
| `additionalProps.endpoint_url` | HTTP(S) MinIO endpoint origin, such as `http://minio.internal:9000`; do not include a path or credentials. |
| `additionalProps.object_key` | Parquet object key. `host` is accepted as a legacy fallback. |
| `additionalProps.table_name` | SQL table name; defaults to the object filename without its extension. |
| `additionalProps.region` | S3 signing region; defaults to `us-east-1`. |
| `additionalProps.session_token` | Optional MinIO/S3 session token. |

The adapter has deterministic unified-query coverage and a pinned contract
against MinIO `RELEASE.2025-04-22T22-12-26Z`. This contract does not imply
compatibility with other MinIO releases, S3-compatible providers, TLS, STS, or
distributed deployments; consult the [support catalog](data-source-support.md)
before production use.

## S3 Parquet source

Register an `s3_parquet` source to expose one configured Parquet object as a
read-only SQL table. The adapter supports a flat schema of primitive columns,
projection, `WHERE` comparisons joined by `AND`, `IS NULL`, `IN`, `LIKE`, `ORDER BY`,
`LIMIT`, `OFFSET`, and `COUNT(*)`. Use the configured table name without a schema
qualifier or table alias. Nested or repeated fields, partition scans, and
queries spanning multiple objects are outside this adapter. It reads Parquet
through ranged S3 object requests; it decodes rows before applying SQL filters
and pagination, so SQL limits do not cap all object data reads. S3 Select is
not used.

| Data-source field or property | S3 Parquet meaning |
| --- | --- |
| `database` | S3 bucket name. |
| `username` and `password` | Optional access key and secret key; configure both together. If omitted, the AWS SDK credential chain is used. |
| `additionalProps.object_key` | Parquet object key. `host` is accepted as a fallback. |
| `additionalProps.table_name` | SQL table name; defaults to the object filename without its extension. |
| `additionalProps.region` | S3 signing region; defaults to `us-east-1`. |
| `additionalProps.endpoint_url` | Optional S3-compatible endpoint. |
| `additionalProps.force_path_style` | Set to `true` for compatible services that require path-style addressing. |
| `additionalProps.session_token` | Optional session token for temporary static credentials. |
| `additionalProps.role_arn, external_id` | Optional role-assumption settings supported by the S3 client. |
| `additionalProps.batch_size` | Parquet reader batch size; defaults to `1000`. |

The adapter has deterministic unified-query coverage and a pinned live contract
against Garage v2.4.1 using SigV4 access-key credentials. That contract does
not imply compatibility with AWS S3 or other providers and versions; consult
the [support catalog](data-source-support.md) before production use.

## Azure Blob ORC source

The azure_blob_orc source type exposes one configured ORC object as a
read-only SQL table. It supports the existing flat primitive ORC schema subset;
nested and repeated fields are rejected. The object is downloaded into gateway
memory and limited to 64 MiB and 100,000 rows. The default object-size limit is
16 MiB and can be raised up to the 64 MiB hard cap.

| Data-source field or property | Azure Blob ORC meaning |
| --- | --- |
| database | Azure container name. |
| username | Storage account name. |
| password | Storage account key; optional when sas_token is set. |
| additionalProps.sas_token | Optional SAS query token. A leading question mark is accepted. |
| additionalProps.endpoint_url | Optional HTTP(S) storage endpoint for compatible gateways and local emulators. Credentials, query strings, and fragments are not allowed. |
| additionalProps.object_key | ORC blob name ending in .orc; host is a fallback. |
| additionalProps.table_name | SQL table name; defaults to the blob filename without its extension. |
| additionalProps.max_file_bytes | Maximum object size in bytes; defaults to 16 MiB and cannot exceed 64 MiB. |
| additionalProps.max_rows | Maximum rows; defaults to 100,000 and cannot exceed 100,000. |

The adapter supports read-only projection, filters joined by AND, IS NULL, IN,
LIKE, ORDER BY, LIMIT, OFFSET, and COUNT(*). Writes are not supported.

## Azure Blob Avro source

The azure_blob_avro source type exposes one configured Avro Object Container
File (OCF) as a read-only SQL table. The adapter reads the embedded writer
schema and records from the object. Raw Avro datum streams and directory scans
are not supported. The object is downloaded into gateway memory and limited to
64 MiB and 100,000 rows. The default object-size limit is 16 MiB and can be
raised up to the 64 MiB hard cap.

| Data-source field or property | Azure Blob Avro meaning |
| --- | --- |
| database | Azure container name. |
| username | Storage account name. |
| password | Storage account key; optional when sas_token is set. |
| additionalProps.sas_token | Optional SAS query token. A leading question mark is accepted. |
| additionalProps.endpoint_url | Optional HTTP(S) storage endpoint for compatible gateways and local emulators. Credentials, query strings, and fragments are not allowed. |
| additionalProps.object_key | Avro OCF blob name ending in .avro; host is a fallback. |
| additionalProps.table_name | SQL table name; defaults to the blob filename without its extension. |
| additionalProps.max_file_bytes | Maximum object size in bytes; defaults to 16 MiB and cannot exceed 64 MiB. |
| additionalProps.max_rows | Maximum records; defaults to 100,000 and cannot exceed 100,000. |

The adapter supports read-only projection, filters joined by AND, IS NULL, IN,
LIKE, ORDER BY, LIMIT, OFFSET, and COUNT(*). Nested Avro fields and unsupported
unions are rejected. Writes are not supported.

## Azure Blob JSON source

The azure_blob_json source type exposes one configured JSON blob as a read-only
SQL table. It accepts a single JSON object, an array of objects, or JSON Lines
(`.jsonl` and `.ndjson`). The adapter uses the shared JSON parser and query
path; directory scans, multiple blobs, and streaming are not supported. Objects
are downloaded into gateway memory and limited to 64 MiB and 100,000 records.
The default object-size limit is 16 MiB and can be raised up to the 64 MiB hard
cap.

| Data-source field or property | Azure Blob JSON meaning |
| --- | --- |
| database | Azure container name. |
| username | Storage account name. |
| password | Storage account key; optional when sas_token is set. |
| additionalProps.sas_token | Optional SAS query token. A leading question mark is accepted. |
| additionalProps.endpoint_url | Optional HTTP(S) storage endpoint; use for compatible gateways and local emulators. Credentials, query strings, and fragments are not allowed. |
| additionalProps.object_key | JSON blob name; accepts `.json`, `.jsonl`, and `.ndjson`; host is a fallback. |
| additionalProps.table_name | SQL table name; defaults to the blob filename without its extension. |
| additionalProps.max_file_bytes | Maximum object size in bytes; defaults to 16 MiB and cannot exceed 64 MiB. |
| additionalProps.max_records | Maximum records; defaults to 100,000 and cannot exceed 100,000. |

The adapter supports read-only projection, filters joined by AND, IS NULL, IN,
LIKE, ORDER BY, LIMIT, OFFSET, and COUNT(*). Queryable fields must be top-level
scalars; nested values and writes are rejected.

## Azure Blob CSV source

The azure_blob_csv source type exposes one configured CSV blob as a read-only
SQL table. It reuses the shared CSV parser and query path; directory scans,
multiple blobs, compression, and streaming are not supported. Objects are
downloaded into gateway memory and limited to 64 MiB and 100,000 rows by
default. The default object-size limit is 16 MiB and can be raised up to the
64 MiB hard cap.

| Data-source field or property | Azure Blob CSV meaning |
| --- | --- |
| database | Azure container name. |
| username | Storage account name. |
| password | Storage account key; optional when sas_token is set. |
| additionalProps.sas_token | Optional SAS query token. A leading question mark is accepted. |
| additionalProps.endpoint_url | Optional HTTP(S) storage endpoint; use this for compatible gateways and local emulators. Credentials, query strings, and fragments are not allowed. |
| additionalProps.object_key | CSV blob name; host is accepted as a fallback. |
| additionalProps.table_name | SQL table name; defaults to the blob filename without its extension. |
| additionalProps.max_file_bytes | Maximum CSV object size in bytes; defaults to 16 MiB and cannot exceed 64 MiB. |
| additionalProps.delimiter | One-character field delimiter; defaults to a comma. |
| additionalProps.has_header | Whether the first non-skipped row contains column names; defaults to true. |
| additionalProps.skip_header_rows | Number of leading rows to skip before CSV parsing; defaults to 0. |
| additionalProps.null_values | Optional string array of values exposed as SQL NULL. |

The adapter supports read-only projection, filters joined by AND, IS NULL,
IN, LIKE, ORDER BY, LIMIT, OFFSET, and COUNT(*). Writes and unsupported SQL
are rejected.

## Azure Blob Parquet source

The azure_blob_parquet and azure_parquet source types use the same adapter.
Each exposes one configured Parquet blob as a read-only SQL table. Supported
queries use a flat primitive schema and the shared object SQL subset:
projection, comparisons joined by AND, IS NULL, IN, LIKE, ORDER BY, LIMIT,
OFFSET, and COUNT(*). Nested/repeated values, partitions, and multiple-blob
queries are not supported.

| Data-source field or property | Azure Blob Parquet meaning |
| --- | --- |
| database | Azure container name. |
| username | Storage account name. |
| password | Storage account key; optional when sas_token is set. |
| additionalProps.sas_token | Optional SAS query token. A leading question mark is accepted. |
| additionalProps.endpoint_url | Optional HTTP(S) storage endpoint; use this for compatible gateways and local emulators. Paths are allowed for emulator endpoints, but credentials, query strings, and fragments are not. |
| additionalProps.object_key | Parquet blob name. host is accepted as a fallback. |
| additionalProps.table_name | SQL table name; defaults to the blob filename without its extension. |
| additionalProps.batch_size | Number of Parquet rows decoded per batch; defaults to 1000. |

The adapter downloads the complete blob into gateway memory before decoding
rows and applying SQL filters or pagination. Keep configured blobs within the
gateway memory budget. Local Azure SDK protocol fixtures cover request wiring,
Shared Key signing, and SAS token propagation; they do not establish
compatibility with a live Azure Storage service release.

## Azure Blob Delta source

The azure_delta source exposes one configured Delta table as a read-only SQL
table. It resolves a snapshot from consecutive JSON transaction-log commits
starting at version zero and reads active Parquet files. It accepts unpartitioned
tables with flat primitive columns and the shared object SQL subset: projection,
simple comparisons joined by AND, IS NULL, IN, LIKE, ORDER BY, LIMIT, OFFSET,
and COUNT(*).

| Data-source field or property | Azure Blob Delta meaning |
| --- | --- |
| database | Azure Blob container name. |
| username | Storage account name. |
| password | Storage account key; optional when sas_token is set. |
| additionalProps.sas_token | Optional SAS query token. A leading question mark is accepted by the Azure client. |
| additionalProps.endpoint_url | Optional Azure Blob endpoint; useful for local emulators and compatible endpoints. |
| additionalProps.table_path | Delta table root within the container; host is accepted as a fallback. |
| additionalProps.table_name | SQL table name; defaults to the final table-path component. |
| additionalProps.batch_size | Number of Parquet rows decoded per batch; defaults to 1000 and is capped at 10000. |

The adapter rejects checkpoints, partitioned tables, deletion vectors, empty
tables, unknown transaction-log actions, and reader features beyond protocol
version 1. It also rejects snapshots with more than 256 active data files, more
than 64 MiB of active Parquet data, or more than 100,000 decoded rows. Active
files are downloaded into gateway memory before SQL filtering and pagination;
decoded rows add memory use beyond the compressed object sizes. Local Azure SDK
protocol fixtures do not establish compatibility with a live Azure Storage
release.

## Tencent COS Parquet source

The cos_parquet connector exposes one configured Parquet object as a read-only
SQL table. It supports flat primitive columns and the shared object SQL subset:
projection, comparisons joined by AND, IS NULL, IN, LIKE, ORDER BY, LIMIT,
OFFSET, and COUNT(*). Nested or repeated fields, partition scans, and queries
across multiple objects are not supported.

| Data-source field or property | Tencent COS Parquet meaning |
| --- | --- |
| database | COS bucket name. |
| username and password | Required SecretId and SecretKey. |
| additionalProps.region | Required COS region, such as ap-guangzhou. |
| additionalProps.endpoint_url | Optional HTTP(S) endpoint origin for compatible COS gateways; omit it to use the region-specific Tencent endpoint. |
| additionalProps.https | Whether to use HTTPS for the default endpoint; defaults to true. |
| additionalProps.object_key | Parquet object key. host is accepted as a fallback. |
| additionalProps.table_name | SQL table name; defaults to the object filename without its extension. |
| additionalProps.batch_size | Number of Parquet rows decoded per batch; defaults to 1000. |

The current adapter downloads the complete object into gateway memory before
decoding rows and applying SQL filters or pagination. Keep configured objects
within the gateway memory budget. Local SDK protocol-fixture coverage does not
establish compatibility with a live Tencent COS service release; see the
support catalog (data-source-support.md).

## Alibaba OSS Delta source

The oss_delta source exposes one configured Delta table as a read-only SQL
table. The initial reader resolves the latest snapshot by replaying consecutive
JSON transaction-log commits from version zero and reads active Parquet files.
It accepts unpartitioned tables with flat primitive columns and the shared
object SQL subset: projection, simple comparisons joined by AND, IS NULL, IN,
LIKE, ORDER BY, LIMIT, OFFSET, and COUNT(*).

| Data-source field or property | Alibaba OSS Delta meaning |
| --- | --- |
| database | OSS bucket name. |
| username and password | Required access key ID and access key secret. |
| additionalProps.endpoint_url | Required OSS endpoint origin, with or without an HTTP(S) scheme. |
| additionalProps.table_path | Delta table root within the bucket; host is accepted as a fallback. |
| additionalProps.table_name | SQL table name; defaults to the final path component. |
| additionalProps.session_token | Optional STS session token; security_token is also accepted. |
| additionalProps.force_path_style | Use path-style requests for compatible gateways and local test servers. |
| additionalProps.batch_size | Number of Parquet rows decoded per batch; defaults to 1000. |

The adapter rejects checkpoints, partitioned tables, deletion vectors, empty
tables, unknown transaction-log actions, and reader features beyond protocol
version 1. It also rejects snapshots with more than 256 active data files,
more than 64 MiB of active Parquet data, or more than 100,000 decoded rows.
Active files are downloaded into gateway memory before SQL filtering and
pagination; decoded rows add memory use beyond the compressed object sizes. Local SDK protocol fixtures
do not establish compatibility with a live Alibaba OSS service release.

## Alibaba OSS Parquet source

The oss_parquet connector exposes one configured Parquet object as a
read-only SQL table. It supports flat primitive columns and the shared object
SQL subset: projection, comparisons joined by AND, IS NULL, IN, LIKE,
ORDER BY, LIMIT, OFFSET, and COUNT(*). Nested or repeated fields,
partition scans, and queries across multiple objects are not supported.

| Data-source field or property | Alibaba OSS Parquet meaning |
| --- | --- |
| database | OSS bucket name. |
| username and password | Required access key ID and access key secret. |
| additionalProps.endpoint_url | Required HTTP(S) OSS endpoint origin, such as https://oss-cn-hangzhou.aliyuncs.com; paths, credentials, query strings, and fragments are not accepted. additionalProps.endpoint is a compatibility alias. |
| additionalProps.object_key | Parquet object key. host is accepted as a fallback. |
| additionalProps.table_name | SQL table name; defaults to the object filename without its extension. |
| additionalProps.session_token | Optional STS session token; security_token is accepted as an alias. |
| additionalProps.force_path_style | Optional path-style addressing for compatible endpoints that require it. |
| additionalProps.batch_size | Number of Parquet rows decoded per batch; defaults to 1000. |

The current adapter downloads the complete object into gateway memory before
decoding rows and applying SQL filters or pagination. Keep configured objects
within the memory budget of the gateway. The deterministic OSS SDK protocol
fixture is not a live Alibaba OSS contract and does not establish compatibility
with a particular service release; consult the support catalog
(data-source-support.md) for the current compatibility evidence.

## Data-source credential encryption

The HTTP and MCP servers require an encryption keyring at startup. Keys are injected by the runtime or a secrets manager; they are not stored in the repository or the YAML config. If either variable is missing, malformed, or does not include the active key, startup fails.

Generate a 32-byte key with openssl rand -base64 32 and store it in your secret manager. Inject the active key ID and a JSON map of key IDs to base64 keys:

    export DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=key-v1
    export DATA_FABRIC_CREDENTIAL_KEYS='{"key-v1":"<base64-encoded-32-byte-key>"}'

Use the same key across restarts. Generating a new key without retaining the old key makes previously encrypted data-source credentials unreadable.

At startup, Data Fabric encrypts legacy plaintext credentials and validates existing ciphertext in a database transaction before accepting requests. If migration fails, the transaction is rolled back and the server does not start. Data-source passwords and recognized secret values in additionalProps are encrypted before persistence and decrypted only when loaded for internal connection use.

### Key rotation

1. Keep the current key in the keyring and add a newly generated key under a new ID.
2. Set DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID to the new ID while retaining both keys in DATA_FABRIC_CREDENTIAL_KEYS.
3. Restart the service. Startup migrates all data-source credentials to the new key; any missing or invalid old key stops startup.
4. After the migration has completed successfully on every deployment, remove the old key from the injected keyring.

Back up the database and key material securely before rotation. Do not put actual key values in config files, shell scripts, or source control.

## Secret management

`configs/config.yaml` is a local, untracked file. Start from `configs/config.yaml.example` (placeholder values only) and keep real secrets out of it:

1. Environment variables override YAML settings. Inject DATABASE_USERNAME, DATABASE_PASSWORD, and SECURITY_JWT_SECRET (or the compatible JWT_SECRET alias) through a secret manager in every deployment.
2. The `.env` file is gitignored but **not loaded automatically**. To use one locally: `set -a; source .env; set +a`, or pass `--env-file` to `docker run` / `env_file` in compose.
3. In container deployments, inject secrets through the platform's secret mechanism (Kubernetes Secret, Docker secrets) instead of baking config files into images — the published image ships `config.yaml.example` only.
4. Calling `/api/v1/fetch` always needs a Bearer token, regardless of `security.enable_auth`. There is no token-issuing endpoint yet, so mint one yourself with the same `JWT_SECRET` (requires `pyjwt`):

   ```bash
   python3 -c "import jwt,time; print(jwt.encode({'sub':'dev','tenantId':'acme','username':'dev','roles':['admin'],'iat':int(time.time()),'exp':int(time.time())+86400}, 'YOUR_JWT_SECRET', algorithm='HS256'))"
   ```

   `tenantId` is optional: tokens without it belong to the default bucket (see [Data source authorization](#data-source-authorization)). If neither SECURITY_JWT_SECRET nor its JWT_SECRET compatibility alias is set, HTTP server startup fails; there is no built-in fallback secret.

## Local development vs production

| Setting | Local development (`debug`/`test`) | Production (`release`) |
|---|---|---|
| `security.enable_auth` | May be `false` to leave the data-source management API unauthenticated; `/api/v1/fetch` still requires a Bearer token either way (#6) | Must be `true`; the server refuses to start otherwise |
| `security.enforce_read_only` | May be `false` for engines that reject the read-only init | Must be `true`; the server refuses to start otherwise (#20) |
| `security.jwt_secret` | Required whenever the HTTP server runs: set, not a known placeholder, at least 32 bytes | Same |
| `server.mode` | `debug` / `test` | `release` |

Startup fails closed on insecure authentication settings before the metadata database is connected. `test_fetch_api.sh` reads the Bearer token from `FETCH_TOKEN` — mint one as shown under Secret management and export it before running the script.

## Data source authorization

Every stored data source carries an `ownerId` and a `tenantId`, stamped from the authenticated caller's JWT at creation time (the standard `sub` identity claim; the existing `userId` claim is accepted for compatibility, plus an optional `tenantId` claim). Tokens without a `tenantId` create rows in the default bucket — an empty tenant is never matched as a tenant, so tenantless callers only see their own rows. Rows created before ownership tracking (or while authentication is disabled) are visible only to administrators because their original owner cannot be established safely.

| Caller | Read (get, list, query, fetch) | Write (update, delete, activate, deactivate) |
|---|---|---|
| Admin (`roles` contains `admin`) | everything | everything |
| Owner (`sub` or compatibility `userId` matches `ownerId`) | own rows | own rows |
| Tenant member (`tenantId` matches) | the tenant's rows | no — read sharing only |
| Non-admin caller | not visible (admin only) | no |

Notable behaviors:

- **Uniform 403.** Non-admin callers get the same `403 DATASOURCE_ACCESS_DENIED` ("You do not have access to this data source") whether the data source is missing or belongs to someone else, on every management endpoint and on `/fetch`, so data source existence cannot be probed. Admins keep the distinct `404` for debugging.
- **Legacy rows.** Rows created before this change (or while `security.enable_auth` is disabled) have no owner or tenant. Only administrators can list, read, query, update, delete, activate, or deactivate these rows. A regular user cannot claim an unowned row by guessing its ID or adding request fields.
- **Per-tenant name uniqueness.** Two data sources may share a name across tenants but not within one tenant (or within the default bucket). The pre-upgrade globally unique name index is dropped automatically at startup.
- **Session binding.** Pagination and streaming sessions are bound to the identity that opened them; another user's valid Bearer token cannot continue someone else's session (`404 QUERY_SESSION_UNAVAILABLE`, indistinguishable from an unknown session).
- **Fleet endpoints are admin-only.** `/api/v1/database/health` and `/api/v1/database/connections/stats` enumerate every stored data source, so they require the `admin` role when `security.enable_auth` is on.
- **`security.enable_auth: false` boundary.** The switch only governs the data-source management API, where no identity exists to check: management requests then run in a legacy-equivalent mode, and created rows stay legacy. `/api/v1/fetch` is authenticated either way (#6), and its authorization checks still apply.

The service validates Bearer JWTs but does not provide a built-in login flow or token issuer. The deployment must issue tokens from a trusted identity source and protect the JWT signing secret: anyone who can sign tokens can assert a user ID, tenant, or role. Optional per-user and per-tenant table permissions are documented in [Table and Column Authorization](security/table-level-authorization.md).

## Environment Variables

Environment variables override YAML values. The canonical naming rule converts a config path to uppercase and replaces each dot with an underscore: `server.read_timeout` becomes `SERVER_READ_TIMEOUT`. No prefix is added.

Every service-config field supports this mapping:

| Configuration path | Canonical environment variable |
|--------------------|-------------------------------|
| `server.port` | `SERVER_PORT` |
| `server.mode` | `SERVER_MODE` |
| `server.host` | `SERVER_HOST` |
| `server.read_header_timeout` | `SERVER_READ_HEADER_TIMEOUT` |
| `server.read_timeout` | `SERVER_READ_TIMEOUT` |
| `server.write_timeout` | `SERVER_WRITE_TIMEOUT` |
| `server.idle_timeout` | `SERVER_IDLE_TIMEOUT` |
| `server.max_request_body_bytes` | `SERVER_MAX_REQUEST_BODY_BYTES` |
| `database.host` | `DATABASE_HOST` |
| `database.port` | `DATABASE_PORT` |
| `database.database` | `DATABASE_DATABASE` (`DATABASE_NAME` is a compatibility alias) |
| `database.username` | `DATABASE_USERNAME` |
| `database.password` | `DATABASE_PASSWORD` |
| `database.ssl` | `DATABASE_SSL` |
| `security.jwt_secret` | `SECURITY_JWT_SECRET` (`JWT_SECRET` is a compatibility alias) |
| `security.jwt_expiration` | `SECURITY_JWT_EXPIRATION` |
| `security.rate_limit_per_minute` | `SECURITY_RATE_LIMIT_PER_MINUTE` |
| `security.rate_limit_burst` | `SECURITY_RATE_LIMIT_BURST` |
| `security.enable_auth` | `SECURITY_ENABLE_AUTH` |
| `security.enable_rate_limit` | `SECURITY_ENABLE_RATE_LIMIT` |
| `security.enforce_read_only` | `SECURITY_ENFORCE_READ_ONLY` |
| `table_authorization.enabled` | `TABLE_AUTHORIZATION_ENABLED` |
| `logging.level` | `LOGGING_LEVEL` |
| `logging.format` | `LOGGING_FORMAT` |
| `query.prefer_streaming` | `QUERY_PREFER_STREAMING` |
| `query.execution_mode` | `QUERY_EXECUTION_MODE` |
| `query.max_concurrent_queries_per_tenant` | `QUERY_MAX_CONCURRENT_QUERIES_PER_TENANT` |
| `query.max_concurrent_streams_per_tenant` (deprecated) | `QUERY_MAX_CONCURRENT_STREAMS_PER_TENANT` |
| `query.rate_limit_per_minute_per_tenant` | `QUERY_RATE_LIMIT_PER_MINUTE_PER_TENANT` |
| `query.rate_limit_burst_per_tenant` | `QUERY_RATE_LIMIT_BURST_PER_TENANT` |
| `mcp.enabled` | `MCP_ENABLED` |
| `mcp.transport` | `MCP_TRANSPORT` |
| <code>mcp.max_concurrent_requests</code> | <code>MCP_MAX_CONCURRENT_REQUESTS</code> |
| <code>sql_translation.enabled</code> | <code>SQL_TRANSLATION_ENABLED</code> |
| <code>sql_translation.timeout</code> | <code>SQL_TRANSLATION_TIMEOUT</code> |
| <code>sql_translation.max_concurrent</code> | <code>SQL_TRANSLATION_MAX_CONCURRENT</code> |
| <code>sql_translation.max_sql_bytes</code> | <code>SQL_TRANSLATION_MAX_SQL_BYTES</code> |
| <code>sql_translation.max_output_bytes</code> | <code>SQL_TRANSLATION_MAX_OUTPUT_BYTES</code> |
| <code>sql_translation.python_path</code> | <code>SQL_TRANSLATION_PYTHON_PATH</code> |
| <code>sql_translation.script_path</code> | <code>SQL_TRANSLATION_SCRIPT_PATH</code> |
| <code>sql_translation.python_module_path</code> | <code>SQL_TRANSLATION_PYTHON_MODULE_PATH</code> |

When both a canonical name and its compatibility alias are set, the canonical name takes precedence. Durations use Go duration syntax, such as `30s` or `24h`; booleans use `true` or `false`.

Example:

```bash
export SERVER_PORT=8099
export DATABASE_HOST=postgres
export DATABASE_NAME=data_fabric
export DATABASE_USERNAME=data_fabric
export DATABASE_PASSWORD=secure-password
export JWT_SECRET="$(openssl rand -base64 32)"
```

`DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID` and `DATA_FABRIC_CREDENTIAL_KEYS` configure the separate data-source credential keyring documented above; they are not fields in the Viper service configuration.

## Configuration Validation and Schema Migration

Before opening the metadata database, startup validates the selected server mode, listener and database ports, the bind host, required database host/name/user, the `database.ssl` value, positive HTTP timeouts and request size, query execution mode, and the security requirements for HTTP transports. Database credentials are then used to open and ping the metadata database; a failed connection — or a bind address that cannot be listened on — prevents the service from accepting requests. HTTP and remote MCP also require a non-placeholder JWT secret of at least 32 bytes. Release mode requires authentication and read-only session enforcement. Invalid duration or scalar values that cannot be decoded into the typed configuration are reported during configuration loading.

For HTTP and MCP startup, Data Fabric applies the metadata schema migration before serving requests. The migration uses GORM AutoMigrate for the data-source schema and removes the legacy global name index; it is idempotent. A migration error is returned from startup and the process exits instead of serving against a partial schema.

### Production rollout

Treat metadata schema changes as part of the application release: back up the metadata database, test the release against a database copy, and ensure the startup database account has the DDL privileges needed for the migration. For a schema-changing upgrade, start one service replica first, verify migration and the health endpoint, then scale out the new version. Keep the previous application version available only when its code remains compatible with the migrated schema; schema changes are not automatically rolled back with an application rollback.

## Security Considerations

### Credential Management
- Store sensitive credentials in environment variables rather than config files
- Use secrets management systems in production
- Regularly rotate credentials
- Apply principle of least privilege

### Read-Only Access
- Data-source sessions are opened read-only where the engine supports it
  (`security.enforce_read_only`, #20); see
  [docs/readonly-access.md](readonly-access.md) for the engine support
  matrix, the per-data-source opt-out, and a least-privilege account
  cookbook
- Engines without a session read-only mode rely on SQL validation plus a
  least-privilege account — provision read-only credentials for every data
  source

### Production Settings
- Set server mode to `release` in production
- Use strong JWT secrets
- Configure appropriate rate limiting
- Enable secure logging practices
- Use TLS for all communications


Remote MCP with Streamable HTTP accepts OIDC-issued access tokens configured by
`mcp.oidc_issuer`, `mcp.oidc_audience`, and `mcp.oidc_required_scopes`.
The configured scope claim may be a space-separated string or a string array.
The issuer must expose HTTPS discovery metadata and a signing JWKS. Data Fabric
checks the signature, issuer, audience, expiration, not-before time, and every
configured required scope before forwarding the verified subject, tenant,
roles, and scopes to query authorization. Claim paths support dot notation for
nested provider claims. Local `local-jwt` remains available for development;
release-mode remote MCP requires `oidc`. MCP stdio remains a trusted local
process and does not use remote bearer authentication.

## HDFS CSV data sources

An HDFS CSV source exposes one configured file as one read-only SQL table. Set
host to the NameNode host, port to its RPC port (8020 by default), database to
the absolute HDFS file path, and username to the HDFS simple-user identity. To
list NameNodes explicitly, set additionalProps.namenodes to a comma-separated
string or an array of addresses. Addresses may include their own ports.

Example source configuration:

    {
      "host": "namenode.example.internal",
      "port": 8020,
      "database": "/warehouse/events.csv",
      "username": "data-fabric-reader",
      "additionalProps": {
        "table_name": "events",
        "has_header": true,
        "delimiter": ",",
        "skip_header_rows": 0,
        "max_file_bytes": 16777216
      }
    }

file_path may be used in additionalProps instead of database. The path must be
absolute, must not contain dot or dot-dot path segments, and must end in .csv.
table_name defaults to the file name without its extension. delimiter must be
one character; the standard double quote is the only supported quote character.
null_values accepts a list of field values treated as SQL NULL. Files default
to a 16 MiB maximum and can be raised to 64 MiB; queries reject files with more
than 100,000 records. CSV bytes and parsed rows are held in gateway memory, so
keep configured files within these limits.

The adapter supports the gateway's restricted read-only SQL subset for a single
table: projections, supported WHERE comparisons and IS NULL checks, ORDER BY,
LIMIT/OFFSET, and COUNT(*). It does not enumerate directories or combine files,
and it does not provide partition discovery, streaming, or Kerberos
authentication. The configured HDFS username is forwarded as the simple-user
identity; deployments that require stronger authentication should use an engine
or connector that supports their HDFS security mode.

## HDFS Parquet data sources

An HDFS Parquet source exposes one configured file as one read-only SQL table.
Use the same NameNode host, RPC port (8020 by default), and HDFS simple-user
identity described above. Set database to an absolute path ending in .parquet;
file_path in additionalProps may be used instead. The SQL table name defaults
to the file name without its extension and can be overridden with table_name.

The adapter accepts max_file_bytes in additionalProps. It defaults to 16 MiB
and can be raised to 64 MiB. Queries reject files with more than 100,000 rows.
The configured Parquet file is read into gateway memory before decoding; filters,
projection, ordering, and pagination operate on decoded rows. Use it for small,
flat files whose size and row count fit these limits.

The read-only SQL subset supports projections, supported WHERE comparisons and
IS NULL checks, ORDER BY, LIMIT/OFFSET, and COUNT(*). Directory discovery,
multiple files, partition pruning, row-group or column pruning, streaming,
Kerberos, and HDFS HA guarantees are outside this adapter's contract. The
configured username is forwarded as the HDFS simple-user identity.

## HDFS JSON data sources

An HDFS JSON source reads one configured .json, .jsonl, .ndjson, or
.jsonlines file as a read-only SQL table. Supported document forms are one JSON
object, an array of objects, or JSON Lines records. Use the NameNode host, RPC
port (8020 by default), HDFS simple-user identity, and absolute file path
conventions described above. The table name defaults to the file name without
its extension and can be overridden with additionalProps.table_name.

The max_file_bytes property defaults to 16 MiB and can be raised to 64 MiB;
files with more than 100,000 records are rejected. Only flat top-level scalar
fields are queryable. Nested objects, arrays as field values, directory scans,
multiple files, compression, writes, Kerberos, and HDFS HA are not supported.
Filtering, ordering, pagination, and COUNT(*) use the shared bounded read-only
SQL subset. The complete file is decoded into gateway memory before SQL
filtering, and no live Hadoop release compatibility is claimed yet.

## HDFS Avro OCF data sources

An HDFS Avro source exposes one configured Avro Object Container File as one
read-only SQL table. Use the NameNode host, RPC port (8020 by default), HDFS
simple-user identity, and absolute file path conventions described above. The
path must end in .avro; table_name defaults to the file name without its
extension. max_file_bytes defaults to 16 MiB and can be raised to 64 MiB.
Queries reject files with more than 100,000 records.

Only OCF files with a non-empty record schema and supported top-level primitive
or nullable primitive fields are queryable. Raw Avro datum streams, standalone
schema JSON, nested records, arrays, maps, and named fields are outside this
adapter. The read-only SQL subset supports projections, supported WHERE
comparisons and IS NULL checks, ORDER BY, LIMIT/OFFSET, and COUNT(*). Directory
scans, multiple files, streaming, Kerberos, and HDFS HA guarantees are not
provided. HDFS files and decoded records are held in gateway memory.

## HDFS plain-text data sources

An HDFS text source exposes one configured UTF-8 .txt or .log file as a
read-only SQL table. Each physical line is a row with a 1-based line_number
and the original line text in line. LF and CRLF are supported; blank lines
and a final line without a newline are retained. The table name defaults to
the file name without its extension and can be set with additionalProps.table_name.

Use the NameNode host, RPC port (8020 by default), HDFS simple-user identity,
and absolute file path conventions described in the HDFS CSV section. The
max_file_bytes property defaults to 16 MiB and can be raised to 64 MiB. Files
with more than 100,000 lines are rejected. This source does not parse structured
logs, scan directories, read compressed inputs, tail files, or provide streaming.

## HDFS compressed Parquet data sources

The hdfs_parquet_compressed source type uses the same single-file, read-only
SQL adapter and limits as HDFS Parquet. It decodes compression stored inside
Parquet data pages; the deterministic adapter fixture covers Snappy. Files
wrapped in an external gzip stream are not Parquet files for this connector.
Use an absolute .parquet path, an optional additionalProps.table_name, and the
max_file_bytes setting described in the HDFS Parquet section.


## Apache Ozone object formats through S3 Gateway

The experimental ozone_parquet, ozone_text, ozone_avro, ozone_json, and ozone_csv sources read one configured object by using Apache Ozone's S3-compatible gateway. Configure the bucket in database, the object key in additionalProps.object_key (or host), and the gateway origin in additionalProps.endpoint_url. Keys must end in .parquet, .txt or .log, .avro, .json/.jsonl/.ndjson/.jsonlines, or .csv for the selected source type. The endpoint defaults to port 9878 when the URL has no explicit port.

| Data-source field | Ozone meaning |
| --- | --- |
| database | Bucket name. |
| host or additionalProps.object_key | One relative object key for the selected format; object_key takes precedence. |
| username and password | Required S3 access key and secret key; signed with SigV4. |
| additionalProps.session_token | Optional session token paired with the access key and secret key. |
| additionalProps.endpoint_url | Required S3 Gateway HTTP(S) origin without a path, credentials, query, or fragment. |
| additionalProps.region | S3 signing region; defaults to us-east-1. |
| additionalProps.table_name | SQL table name; defaults from the object key. |
| additionalProps.max_file_bytes | Maximum object bytes; defaults to 16 MiB and cannot exceed 64 MiB. |
| additionalProps.delimiter | CSV field separator; defaults to a comma and must be one character. |
| additionalProps.has_header | Whether the first non-skipped CSV row contains column names; defaults to true. |
| additionalProps.skip_header_rows | Number of leading CSV rows to skip before optional header parsing; defaults to 0. |
| additionalProps.null_values | CSV field values treated as SQL NULL; accepts a list or comma-separated string. |
| additionalProps.quote_char | CSV quoting character; only the standard double quote is supported. |

Requests use path-style addressing. The Parquet adapter exposes one object as a bounded read-only SQL table; the text adapter exposes each physical line as line_number BIGINT and line VARCHAR; Avro exposes one OCF object through the restricted flat-record SQL subset; JSON exposes one object, object array, or JSON Lines file with top-level scalar fields through the restricted read-only SQL subset; CSV exposes one row-oriented file with configurable header, delimiter, skipped header rows, and null markers. Each format caps objects at 64 MiB and results at 100,000 rows, lines, or records. The adapters do not implement native Ozone RPC, Kerberos, object listing, multi-file scans, or bucket/volume administration. See the Ozone support status document for exact limitations and compatibility evidence.
