# Connected tool contracts

These are the current Data Fabric MCP tool names and payloads. A client may
namespace or adapt them: discover its advertised tools and input schemas, then
map the capabilities below to those tools. Do not invent a missing tool or
assume that installing this skill connects a server.

## Discover and inspect

| Capability / tool | Arguments | Successful payload |
| --- | --- | --- |
| `list_data_sources` | None | `{"data_sources":[{"id":"…","name":"…","type":"…","status":"active"}],"count":1}` |
| `get_data_source_info` | `datasource_id` | One source object, including `id`, `name`, `type`, `status`, and non-secret connection metadata. |
| `list_tables` | `datasource_id` | A batch response: table names are values in `entries`, aligned with `columns`. |
| `describe_table` | `datasource_id`, `table_name`; optional `schema` | `{"datasource_id":"…","table":{"name":"…","schema":"…","columns":[{"name":"…","type":"…","nullable":true}]}}` |

Use source IDs returned by discovery. `data_sources` can be `null` when
`count` is zero; treat this as an empty list. Only sources visible to the
current caller are returned. Connection credentials are not returned by these
tools, and this skill contains none.

Table metadata may also include `primaryKey`, `indexes`, `foreignKeys`, and
table `properties`. Enumerate tables before describing a selected table.
Supply a discovered schema when the same table name occurs in multiple
schemas; do not guess one. Check that the returned `table.schema` confirms
the intended schema; if ambiguity remains or the schema does not match,
report the metadata limitation rather than assuming it was resolved.
Table discovery itself can be batched: check its
`nextUri` before claiming the list is complete.

## Validate and execute

| Tool | Required arguments | Optional arguments |
| --- | --- | --- |
| `validate_sql_query` | `datasource_id`, `sql` | `source_dialect` |
| `execute_sql_query` | `datasource_id`, `sql` | `source_dialect`, `parameters`, `batch_size`, `timeout` |
| `fetch_next_batch` | `query_id`, `slug`, `token` | `batch_size` |

Validation returns either `{"valid":true,"sql":"…"}` or
`{"valid":false,"error":"…"}`. **A rejected validation can have
`isError` absent or false**: inspect `valid` inside the JSON payload and do not
execute when it is false. Validation does not execute the query and does not
establish that every connector can run or count it successfully.

For execution, explicitly send a modest `batch_size` appropriate to the task;
the accepted positive range is 1–10000, with a default of 10000. `timeout` is
in seconds, accepts 1–1800, and defaults to 60. The fetch tool also defaults to
10000 rows per page and has no configurable `timeout` argument.

`parameters` is an ordered array of at most 64 JSON scalars: null, boolean,
number, or string. Objects and nested arrays are unsupported. Use the driver's
supported placeholders instead of inserting values into SQL text. The
validation tool does not accept a `parameters` argument. When using
`source_dialect`, keep it consistent between validation and execution; the
gateway performs translation, so do not also translate the same SQL twice.

## Read batches and continue only when needed

Successful execution and continuation use this shape:

```json
{
  "queryId": "returned-query-id",
  "slug": "returned-slug",
  "token": "returned-current-token",
  "nextUri": "/api/v1/fetch/...",
  "executionMode": "streaming",
  "columns": [{"name": "label", "type": "string", "nullable": false}],
  "entries": [["example"]],
  "totalCount": 42
}
```

Each row in `entries` is an array whose positions match `columns`. SQL NULL is
JSON `null`; the string `"NULL"` is still text. Binary-typed values are base64
strings; text-typed values, including exact decimal text, remain strings.
Preserve these distinctions when presenting results.

For the next MCP call, map the latest response's **`queryId` to `query_id`**
and pass its `slug` and `token` unchanged. Use the connected continuation tool
rather than making a new HTTP request to `nextUri`. After each successful
page, replace the stored continuation values with those in that response.
Tokens rotate, are single-use, and are tied to the caller and in-memory
session. Do not issue concurrent continuation calls or replay an old token.

An absent or empty `nextUri` means the result is exhausted, even if `token`
is present. A non-empty `nextUri` means more rows are available, not that they
must all be fetched. Stop once the requested information or agreed row budget
is satisfied, and disclose when the answer uses only part of the result.
`totalCount` is supplied by the gateway and may involve an extra backend
count; do not substitute the number of rows fetched for the total.

## Capability gaps and failures

`describe_table` depends on the server's metadata support. `translate_sql`
and `list_supported_dialects` are optional and only registered when translation
is enabled. If exposed, `translate_sql` requires `sql`, `source_dialect`, and
`target_dialect`; `list_supported_dialects` takes no arguments. Missing
translation tools do not prevent a native-dialect query. Missing required
discovery, metadata, validation, or execution capabilities must be reported;
use a verified equivalent adapter or user-supplied schema where appropriate.

MCP success payloads are normally JSON text inside a result's `content` text
blocks; an adapter may expose that JSON directly. Inspect the wrapper before
parsing the payload:

- `isError: true` is a tool failure even when the JSON-RPC call succeeded.
- A JSON-RPC/transport error is not query data. A lost continuation response
  leaves token consumption uncertain, so do not blindly replay it.
- `valid: false` is a validation failure regardless of the wrapper flag.

For invalid SQL or an unsupported dialect/statement, revise a bounded query
using verified metadata and revalidate it. For permission denial or an
inactive/missing source, stop that path and explain the required access or
configuration correction. Do not try another identity or infer hidden source
or table existence from a denial.

For an expired/invalid session, stop pagination and explain that a fresh query
is needed. A restarted query is a new result set; do not merge its pages into
the previous partial result. For timeouts or unavailable engines, report the
failure and any partial-result limits; do not silently retry with a broader
query, larger batch, or longer timeout.
