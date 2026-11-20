# Proposed explicit no-count query mode

[中文](query-no-count-mode-zh.md) · [Issue #450](https://github.com/RhinoLink-Tech/data-fabric/issues/450) · [Current result contract](../query-result-semantics.md)

This is the pre-implementation design and measurement record. See the [current result contract](../query-result-semantics.md) for the implemented source scope and semantics; availability in a released image depends on its version. The default remains exact counting.

## Why

An AI client may only need the first bounded preview, yet SQL pagination runs a separate exact-count statement when the first page is full; streaming currently counts before its first chunk. After the MCP default first page became 100 rows, a 101–9,999-row result can encounter a count that a 10,000-row page previously avoided. Users should be able to request a complete ordered result without paying for an exact total when their task does not need it. This is not a promise that queries, full scans, sorting, aggregates, or remote response times become cheap.

A local synthetic experiment in the companion benchmark uses an in-memory 2,000-row SQLite database, a non-indexed filter, ordered ID, 100-row pages, actual `database/sql` pagination, JSON encoding, and a driver wrapper that counts queries. The exact case uses the existing service; the no-count case is **test-only prototype code** using a 101-row bounded lookahead. Three runs on Linux/amd64 WSL, Intel Core Ultra 9 275HX, Go benchmark `-benchtime=1s`:

| Task | Exact count | Test-only lookahead |
| --- | --- | --- |
| First page, source SQL statements | 2 | 1 |
| First page, time/op | 151–156 µs | 111–114 µs |
| First page, Go allocations/op | 48.4 KB | 46.6 KB |
| All 2,000 rows, source SQL statements | 21 | 20 |
| All rows, time/op | 2.588–2.605 ms | 2.563–2.606 ms |
| All rows, Go allocations/op | 954.6–955.0 KB | 946.7 KB |

Both cases returned the same ordered IDs; first-page JSON was 7,622 bytes and all pages together 155,493 bytes in this prototype. The prototype encodes the unknown `totalCount` as `null` but does not include every field proposed below; these are not final protocol-size measurements. Times include SQLite query execution, row conversion, checks and JSON encoding, not MCP/HTTP transport, network, a real remote database, production concurrency, peak RSS, or client/model time. The lookahead can re-read one row per subsequent offset page; count removal may not reduce source work or improve latency in other workloads. Exact and unknown totals are different information, not interchangeable results.

Reproduce with cached Go dependencies and CGO/SQLite available (remove `GOPROXY=off` if dependencies must be downloaded):

```sh
CGO_ENABLED=1 GOPROXY=off go test -tags datafabric_sqlite ./internal/service \
  -run '^$' -bench '^BenchmarkCountChoiceSynthetic$' -benchmem -benchtime=1s -count=3
```

## Proposed public contract

- Initial REST `POST /api/v1/fetch` and MCP `execute_sql_query` accept optional `count_mode` with `exact` or `none`. Omitted/empty uses existing `exact`; invalid types or values fail before source execution. The MCP default initial batch size remains 100; REST remains unchanged.
- `exact` keeps the current `totalCount` integer and existing field behavior. No existing response field changes for callers that omit the option.
- `none` never issues a helper count, reports `countMode: "none"`, `totalCount: null` and an explicit boolean `hasMore` on **every** successful initial and continuation page. Unknown is not zero, an estimate or a lower bound. Even the final page retains `totalCount: null`: a changing offset query cannot retroactively prove a stable global total.
- `hasMore: true` and a nonempty `nextUri` appear together; `hasMore: false` and no `nextUri` mean the gateway observed that this execution is exhausted. The client must use the latest query ID, slug and token, and must not call a partial answer complete. The continuation inherits the original count mode; it cannot change the mode of an existing session.
- The result describes the submitted SQL, including its own `LIMIT`/`OFFSET`. `hasMore: false` does not claim that rows outside the SQL limit or a filtered data set were retrieved. Pagination can still produce duplicates or omissions under concurrent mutation; a cursor has source-specific consistency and lifetime, not a universal snapshot promise.
- An unsupported executor or unsafe pagination transformation fails explicitly and safely before returning a page; it must not silently run an exact count, emit a fake total, or mislabel incomplete data. No advertised source support until that source path has meaningful tests.

Illustrative proposed response (fields abbreviated, not yet available):

```json
{"countMode":"none","totalCount":null,"hasMore":true,"entries":[[1],[2]],"queryId":"...","slug":"...","token":"...","nextUri":"..."}
```

A successful terminal page would use `"hasMore":false`, keep `"totalCount":null`, and omit `nextUri`. Existing client models that require an integer total must keep `exact`; `none` requires clients that understand nullable totals. Older servers may ignore unknown fields and still perform an exact count. Clients must verify `countMode: "none"` and a null total in the response before treating no-count as active; absence means the feature is not established, not a successful no-count result.

## Implementation constraints

1. Validate and carry the mode through REST/MCP and unified routing into the selected service. Protect against a server configuration overriding the requested execution mode. Preserve bound parameters, read-only SQL validation, policies, grants and audit.
2. For SQL pagination, use at most one extra row under a safe driver-specific wrapper, return no more than the requested page size, and use the extra row only to establish `hasMore`. On continuation re-execute at the offset of **returned** rows; preserve the original SQL and bound parameters, including user limits. Explicitly account for drivers whose maximum bounded request cannot accept `batch_size + 1`.
3. For a live SQL cursor, skip the helper count and retain at most one looked-ahead row under the existing stream lock. The pending row must survive size changes, count toward resource limits when observed, and never be dropped or emitted twice. Close on exhaustion, failure, expiry or cancellation.
4. Native API and object adapters may use a verified bounded lookahead or an authoritative continuation signal. Do not infer absence from a provider-imposed short page. Where support cannot be proven, return a clear unsupported error. Do not manufacture cross-source support from a SQLite experiment.
5. Preserve session ownership, token rotation/replay rejection, serialization, reauthorization, source status, query budgets, deadlines, admission, owner routing, revocation and cancellation. Count both visible work and the extra lookahead in a documented budget; failure must not turn a partial response into a successful terminal page. At most one retained lookahead row per live session; protect against an oversized row.
6. On session loss, expired token, budget exhaustion, driver error, or denied access, return the existing safe failure semantics, not `hasMore: false`. Describe restart rather than cursor failover.

## Verification before enabling

A separate implementation PR must exercise real REST and MCP paths with omitted/`exact`/`none`, invalid inputs, and independently verify zero helper counts for `none`. For pagination and streaming, cover empty, less-than-page, exactly-full, page-plus-one, several pages, explicit SQL limits/offsets, typed/binary/null results, bound parameters, changing requested page sizes and full ordered result. Include count failure in exact mode, interrupted lookahead, provider short pages and unsupported sources. Re-run replay/concurrent-fetch, ownership/tenant, policy revocation, restart/owner loss, source disable, budgets, cancellation and cleanup tests. Verify a pinned real PostgreSQL and MySQL source in isolated CI; report additional source modes only after their own evidence. Compare first and complete task times and source calls without extrapolating the synthetic prototype into production claims.

Update API schemas, bilingual user docs, MCP tool help and Skill guidance only when the implementation is verified. Marketplace updates require the user's prior confirmation. Keep #450 open until implementation and evidence satisfy its acceptance criteria.
