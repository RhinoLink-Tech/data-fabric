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
| `list_source_types` | — | `type`, `category`, `format`, `include_details` | Discover registered connector types, categories, formats and version-specific verification overview in this server build. Set include_details=true for operations, authentication disclosures and limitations. These are types, not configured data sources. |
| `list_data_sources` | — | — | List the caller's data sources (own, tenant-shared, or everything for the local stdio identity). |
| `execute_sql_query` | `datasource_id`, `sql` | `source_dialect`, `parameters` (≤ 64 items; each `null`/boolean/number/string), `batch_size` (default 10000, clamped to 1–10000), `timeout` (seconds; default 60, clamped to 1–1800), `execution_mode` (`pagination` or `streaming`) | Execute a read-only query. `pagination` re-executes bounded LIMIT/OFFSET pages and releases the source connection between batches; `streaming` holds a source cursor and source resources until completion. The default prefers streaming, but server configuration can override the requested path; inspect `executionMode` in the result. `parameters` are bound by the driver to `?` (MySQL) or `$1..$N` (PostgreSQL) placeholders — never interpolated into the SQL text. |
| `fetch_next_batch` | `query_id`, `slug`, `token` | `batch_size` (default 10000) | Fetch the next page from a previous `execute_sql_query` result. Tokens are in-memory; they do not survive a gateway restart. |
| `get_data_source_info` | `datasource_id` | — | One data source's non-secret connection details. |
| `rename_data_source` | `datasource_id`, `name` | — | Rename an owned source; requires authenticated `data-fabric.sources.manage` scope. Remote HTTP only. |
| `set_data_source_status` | `datasource_id`, `status` | — | Activate or deactivate an owned source; status is `active` or `inactive`. Requires authenticated `data-fabric.sources.manage` scope. Remote HTTP only. |
| `validate_sql_query` | `datasource_id`, `sql` | `source_dialect` | Validate a query (read-only rules, dialect) without executing it. |
| `list_tables` | `datasource_id` | — | Enumerate tables in a data source. |
| `describe_table` | `datasource_id`, `table_name` | `schema` | One table's columns, types, nullability, primary key, and indexes. Use `list_tables` first; pass `schema` when table names repeat across schemas. |
| `search_schema` | `datasource_id`, `query` | `schema`, `limit` (default 10, max 20) | Case-insensitive 2–128 character substring search over permitted catalog/schema/table names and visible column names/comments. Returns stable identifiers, matched fields, and explicit `ambiguous`/`truncated` flags; comments themselves are not returned. |
| `translate_sql` | `sql`, `source_dialect`, `target_dialect` | — | Translate SQL between dialects. |
| `list_supported_dialects` | — | — | Enumerate the dialects `translate_sql` accepts. |
| `get_table_policy` | — | `revision` | Read the active policy, version and freshness, or inspect an immutable historical revision. Administrator role required. |
| `validate_table_policy` | `policy_json` | — | Preflight a complete enabled table/column policy without publishing it. Administrator role required. |
| `publish_table_policy` | `expected_revision`, `policy_json` | — | Compare-and-swap publish a policy. Rollback creates a new revision. Administrator role required. |
| `list_table_policy_publications` | — | `limit` (1–100; default 50) | List publisher, revision and outcome history. Administrator role required. |

## Conditional registration

- Source lifecycle tools are registered for remote HTTP with the data-source service configured. They require the `data-fabric.sources.manage` scope and the existing owner/admin write check; query scope alone cannot change sources. These tools never accept connection settings or secrets.

- `translate_sql` and `list_supported_dialects` are registered only when
  `sql_translation.enabled` is true (`sql_translation.*` settings).
- `describe_table` and `search_schema` are registered only when the gateway is
  built with schema discovery for the deployment (both server modes register them today).
- The four table-policy administration tools are registered only while an
  enabled managed table policy is loaded. They always require the administrator
  role and are available through remote MCP; policy publication uses the shared
  metadata database and the same revision contract as the REST admin endpoints.

## Failure semantics (two channels)

Tool failures reach the client through two distinct channels:

1. **Tool-level errors** — the JSON-RPC response is a *successful* call whose
   result carries `isError: true` with a human-readable text message. Used for
   conditions the client can act on: unknown or inaccessible
   `datasource_id`, invalid `parameters`, rejected SQL, stale managed table
   policy (which fails closed), query failures
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
