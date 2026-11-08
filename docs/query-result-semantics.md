# Query result semantics

中文版：[查询结果语义](query-result-semantics-zh.md)。

This document describes the current public result contract for REST POST /api/v1/fetch and MCP execute_sql_query / fetch_next_batch. The recommended PostgreSQL/MySQL task profile uses the same row encoder for paginated and streamed batches.

## Response and continuation

A fetch response contains columns, entries, totalCount, an optional nextUri, and the execution mode. Each row in entries is an array aligned with columns. REST wraps this object in the standard response envelope; MCP returns the same JSON object in tool text content.

If nextUri is present, request that exact continuation and use the newest session token returned by each successful batch. A successful response that omits nextUri is the API's completion indicator: the selected path has no further batch according to the count/cursor state it observed. This does not promise a cross-page snapshot when source rows change. Batch length is not a completion signal: a short page can still have a continuation, and a full page can be final. Follow nextUri for type 1, type 2, automatic mode, and a fallback to pagination alike.

totalCount is calculated separately from the returned page. For the PostgreSQL/MySQL profile it describes the SQL result count observed by that count operation, with the request's bound parameters and SQL limit scope preserved. It is not a cross-page snapshot count and may disagree with rows traversed if the source changes while the query is starting or pages are being fetched. A count that cannot be computed safely may fail the request rather than report a fabricated value.

## Execution modes

- **Pagination** re-executes the query with LIMIT and OFFSET for each continuation. It is bounded and releases the query connection between requests, but each page can observe a different source state.
- **Streaming** retains the source cursor between requests until the stream is exhausted, cancelled, expired, or stopped by a query budget. It holds source resources for the cursor lifetime. Source isolation and driver behavior determine whether that cursor sees a stable view; Data Fabric does not promise a cross-driver snapshot.
- **Automatic mode** reports the actual path as executionMode. REST callers select pagination with type: 1 or request streaming with type: 2; MCP callers can pass execution_mode: "pagination" or "streaming". The default prefers streaming. Explicit server execution configuration may override a request, so clients should inspect executionMode. The continuation contract is the same for either path.

A timeout, expired session, cancellation, source failure, or query-budget error means the logical result is incomplete if the client has already received earlier pages. Do not present those pages as exhaustive. On a continuation error, follow the documented error and token state; if the session cannot continue, restart the logical query and discard or clearly label its earlier pages.

## Ordering and changes between pages

For a fixed source state, specify ORDER BY with a unique, stable tie-breaker when row order matters. Without ORDER BY, SQL does not guarantee page order. A non-unique order can still move equal-key rows between pages.

Pagination is not a snapshot. Inserts, deletes, or updates to selected, filtered, or ordered values between page requests can shift offsets and produce duplicate or missing rows relative to one imagined point-in-time result. A unique order makes a fixed result deterministic but does not prevent this mutation effect. The current public profile does not claim snapshot-stable pagination. Use a read-only, quiescent reference fixture for repeatable adoption tasks; a production task requiring a cross-page snapshot needs a separately supported consistency mechanism.

Streaming holds a cursor instead of re-running the query, but its snapshot/isolation guarantee depends on the source driver and transaction behavior. totalCount is issued separately from the cursor query, so it is not a proof of cursor snapshot size.

## Values and current evidence boundary

The shared fetch encoder preserves SQL NULL as JSON null, keeps text bytes as text, encodes binary bytes as standard base64, keeps int64 values as JSON integers, and preserves DECIMAL text. The public reference fixture checks exact integer and decimal values and distinguishes SQL NULL from the string "NULL". Its A28 mutation observations are pinned to the reference MySQL/PostgreSQL images; they do not establish a cross-driver snapshot guarantee. Temporal values, timezone/fraction precision, floating-point boundaries, binary round trips, and source-specific count cost still need candidate-matched contract records before they are described as uniform cross-source guarantees.

The public acceptance matrix records inserts, deletes, and updates between pages through actual REST/MCP responses for unique, non-unique, and absent ordering, and distinguishes re-executed pagination from held-cursor streaming. See the candidate artifact for exact synthetic row IDs. Issue #455 remains open until the selected candidate passes those outcomes and any silent truncation is gated.
