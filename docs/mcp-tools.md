# MCP Tool Reference

The Data Fabric MCP server exposes the tools below over both transports
(local stdio and remote Streamable HTTP). `internal/mcp` keeps a
contract test synchronized with this page in both directions: every
registered tool must be documented here, and every tool documented here
must be registered. Changing a tool's name or required parameters is a
client-visible contract change — update this page in the same PR.

## Tools

| Tool | Required | Optional | Purpose |
| --- | --- | --- | --- |
| `list_data_sources` | — | — | List the caller's data sources (own, tenant-shared, or everything for the local stdio identity). |
| `execute_sql_query` | `datasource_id`, `sql` | `source_dialect`, `parameters` (≤ 64 items; each `null`/boolean/number/string), `batch_size` (default 10000, clamped to 1–10000), `timeout` (seconds; default 60, clamped to 1–1800) | Execute a read-only query. `parameters` are bound by the driver to `?` (MySQL) or `$1..$N` (PostgreSQL) placeholders — never interpolated into the SQL text. |
| `fetch_next_batch` | `query_id`, `slug`, `token` | `batch_size` (default 10000) | Fetch the next page from a previous `execute_sql_query` result. Tokens are in-memory; they do not survive a gateway restart. |
| `get_data_source_info` | `datasource_id` | — | One data source's non-secret connection details. |
| `validate_sql_query` | `datasource_id`, `sql` | `source_dialect` | Validate a query (read-only rules, dialect) without executing it. |
| `list_tables` | `datasource_id` | — | Enumerate tables in a data source. |
| `describe_table` | `datasource_id`, `table_name` | `schema` | One table's columns, types, nullability, primary key, and indexes. Use `list_tables` first; pass `schema` when table names repeat across schemas. |
| `translate_sql` | `sql`, `source_dialect`, `target_dialect` | — | Translate SQL between dialects. |
| `list_supported_dialects` | — | — | Enumerate the dialects `translate_sql` accepts. |

## Conditional registration

- `translate_sql` and `list_supported_dialects` are registered only when
  `sql_translation.enabled` is true (`sql_translation.*` settings).
- `describe_table` is registered only when the gateway is built with schema
  discovery for the deployment (both server modes register it today).

## Failure semantics (two channels)

Tool failures reach the client through two distinct channels:

1. **Tool-level errors** — the JSON-RPC response is a *successful* call whose
   result carries `isError: true` with a human-readable text message. Used for
   conditions the client can act on: unknown or inaccessible
   `datasource_id`, invalid `parameters`, rejected SQL, query failures
   (messages come from the gateway's sanitized mapping — internal hostnames,
   DSNs, and driver errors are never echoed), and expired pagination tokens.
2. **Transport-level errors** — the JSON-RPC response itself is an error
   object. Reserved for gateway-side faults such as the metadata repository
   failing or a response failing to marshal.

## Credential handling

`list_data_sources` and `get_data_source_info` build responses from an
explicit allowlist of fields (id, name, type, status, host, port, database,
timestamps). Stored credentials — password, username, and additional
properties — are never included in any tool response.
