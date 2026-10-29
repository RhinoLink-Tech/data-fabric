# SQL compatibility and examples

## Choose the source's supported SQL

Use the selected source's native dialect when possible and omit
`source_dialect` in that case. If translation is needed, use an advertised
dialect and the same `source_dialect` for validation and execution. Passing
already-translated SQL with the old input dialect can translate it twice.
Translation success does not establish engine compatibility or equivalent
function, timezone, NULL, collation, or aggregate semantics.

The gateway accepts a single read-only SELECT/UNION statement and rejects
writes, DDL, procedure calls, multiple statements, and SQL comments. Its
shared SQL scope covers ordinary projection, filters, ordering, relational
joins, and aggregates where the selected connector supports them. CTEs,
window functions, engine-specific functions, and time travel are not a
universal compatibility promise; validate native syntax and the connector's
actual behavior before relying on them.

MongoDB and CSV adapters have narrower SQL subsets. Start with a single
collection or table and supported projections/filters; do not infer join,
function, or aggregate support from validation alone. A registered source
type is not proof of an operational or fully verified query path.

Cross-catalog joins require a configured Trino, Presto, or OpenLooKeng source
whose coordinator exposes those catalogs. Independent source IDs are not SQL
catalogs and are not automatically federated by Data Fabric.

For previews, use native row-limiting syntax supported by the selected
engine, necessary columns, and a deterministic ordering where available.
MySQL and PostgreSQL accept `LIMIT`; do not copy that syntax to every engine.
`batch_size` limits one page and does not substitute for a SQL limit, filter,
or source-side aggregate.

## Bind values separately

Where the selected driver supports parameters, use MySQL `?` or PostgreSQL
`$1`, `$2`, ... with an ordered `parameters` array on `execute_sql_query`.
Use discovered identifiers in the statement; bind placeholders represent
values, not table or column names. Do not interpolate arbitrary user values
into SQL text.

The validation tool takes no `parameters` argument: it checks the statement's
structure and access policy, not that supplied bind values will execute or
have the intended types. Keep the same SQL and dialect for the execution
call. If validation rejects placeholders or the connector does not support
binding, report that limitation or use a verified adapter rather than
silently treating validation as successful. The example below uses no bind
values and does not assume binding support for other connectors.

## Worked example: a bounded orders preview

Assume the user requested up to 20 orders, highest amount first. The table
and columns below are fictional: use them only if metadata actually returns
them. Replace uppercase placeholders with values from tool results. These
are tool argument objects, not client-specific invocation syntax.

1. Discover sources with `list_data_sources` and `{}`. If multiple visible
   sources match "orders", ask which one to use before querying. After
   selection, call `list_tables`:

   ```json
   {"datasource_id":"SELECTED_SOURCE_ID"}
   ```

2. For a selected PostgreSQL source whose table listing contains `orders`,
   with `public` already verified as the intended schema, call `describe_table`:

   ```json
   {"datasource_id":"SELECTED_SOURCE_ID","table_name":"orders","schema":"public"}
   ```

3. Only if the returned schema includes `order_id`, `customer`, and `amount`,
   call `validate_sql_query`:

   ```json
   {"datasource_id":"SELECTED_SOURCE_ID","sql":"SELECT order_id, customer, amount FROM public.orders ORDER BY amount DESC, order_id LIMIT 20"}
   ```

   Inspect the result wrapper and the decoded JSON payload. Execute only
   when validation reports `valid: true`; `valid: false` is a rejection even
   if the tool-level error flag is false.

4. Execute the same SQL with `execute_sql_query`:

   ```json
   {"datasource_id":"SELECTED_SOURCE_ID","sql":"SELECT order_id, customer, amount FROM public.orders ORDER BY amount DESC, order_id LIMIT 20","batch_size":10,"timeout":30}
   ```

5. Read `columns` and `entries`. If the response has a non-empty `nextUri`
   and more rows are needed to provide the requested preview, call
   `fetch_next_batch` with values from that latest response:

   ```json
   {"query_id":"LATEST_QUERY_ID","slug":"LATEST_SLUG","token":"LATEST_TOKEN","batch_size":10}
   ```

   `LATEST_QUERY_ID` comes from the response's `queryId`, not a guessed
   source ID. After a successful page, replace all continuation values.
   Stop when the requested 20 rows are available or `nextUri` is absent or
   empty. Report how many rows were returned and whether more remain.

## Recovery examples

- No discovery/execution tools: explain that the skill needs a connected
  Data Fabric server or an equivalent adapter. Installing the skill alone
  has not supplied a connection; do not claim to have inspected any data.
- `valid: false` with an unknown column: inspect the selected table schema,
  correct the column only when its intended meaning is clear, and validate
  again. Do not execute the rejected query.
- A partial result already answers the user's preview: summarize that page
  and identify it as partial instead of fetching every remaining page.
- An expired token or lost continuation response: stop paging and explain
  that a fresh query may be needed. Do not replay consumed tokens or append
  a restarted result to the old snapshot.
