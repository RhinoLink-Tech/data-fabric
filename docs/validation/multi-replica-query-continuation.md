# Two-replica continuation acceptance matrix

This matrix defines the checks for the implementation proposed by
[ADR-003](../decisions/adr-003-query-continuation-across-replicas.md).
Record the commit, image, source and client versions, effective configuration,
and observed status and resource counters for every executed scenario.

## Automated pull request coverage

The required Public REST and MCP reference acceptance check builds one
immutable image containing the API server and replica-ingress, then starts the
pinned MySQL/PostgreSQL sources, two REST owners, two remote MCP owners, the
OIDC fixture, and the ingress in an isolated Compose project. Its -multi-replica
run exercises R1 and M1 through the published loopback ingress: it verifies
signed owner slots, switches new work from A to B, follows REST continuations
and MCP tool calls on A, checks MCP GET/DELETE session affinity, and starts
fresh work on B. The runner records its source commit, image digest, container
resource snapshot, and per-case owner slots.

The required check also runs the focused component cases in
internal/replicarouting and internal/mcp. Those cases cover marker rejection,
principal binding, expiry, capacity, drain and cleanup. Together the container
and component tests give an automated contract for R1 and M1 and supporting
owner lifecycle behavior. They do not claim that the full matrix below has
run. Record the remaining cases separately before using owner routing in a
deployment; policy publication and fleet-wide revocation remain tracked in
https://github.com/RhinoLink-Tech/data-fabric/issues/99.

For each REST or remote MCP run, start two replicas of that server mode
behind one ingress. Use distinct owner slots and boot identifiers and share a
metadata database, auth and policy configuration, and a deterministic source.
Use a read-only source account and a query with a unique, stable order that
returns at least three batches. Run REST against forced pagination and
streaming and a native API fixture with a continuation cursor. Test the
remote MCP path with the normal initialize and tool calls, including a
transport GET and DELETE with a session-aware client. Use a shorter configured
query TTL for the expiry checks when
[#403](https://github.com/RhinoLink-Tech/data-fabric/issues/403) is available.
A fixture result covers that fixture's cursor behavior.

Start with A marked `accept_new` in the ingress's static configuration.
Reload the same map with B as the sole owner for new work and A retained for
continuations. Drive requests through the public ingress and verify the
actual upstream through test counters or redacted logs. Keep the private
owner listener accessible only to the ingress and test operator. Use two
users in one tenant and distinct tenants with the same subject that both
have source access. For the latter, verify each claim can start a new query
under the existing data-source policy. For remote MCP, initialize two
sessions for one principal.

| ID | Setup and action | Expected outcome |
| --- | --- | --- |
| R1 | Start a query on A. Switch the new-work pool to B and follow the returned REST `nextUri` through ingress. | Each batch reaches A. Results match the expected ordered rows, token changes after advancement, and terminal fetch clears continuation state. A new independent query reaches B. |
| R2 | Send two concurrent REST requests with the same current token. Replay that token again after success. | One fetch advances and returns the next batch. Other uses get the session-unavailable response. No repeated or skipped rows occur when the winner's URI is used. |
| R3 | Drop the response after A advances. Observe the upstream calls and retry the previous URI. | Ingress did not retry the fetch. The old token is unavailable. A new query starts a separate result sequence. |
| R4 | Swap two query IDs or slugs, alter the marker or key ID, use the wrong deployment or transport scope, or target an unregistered slot. | Ingress returns the same generic unavailable response. It does not make an upstream request, disclose private addresses, or accept a client-supplied endpoint. |
| R5 | Send valid routing metadata with missing or expired Bearer, another user, or the same subject in another tenant. Then retry as the creator with a fresh valid token. | Unauthenticated requests fail authentication. Other principals cannot advance the session even with source access. The creator's current continuation still works. |
| R6 | Let the absolute query TTL elapse, including a case where an earlier batch was fetched shortly before expiry. | The old URI is unavailable. Fetching does not extend the lifetime. The owner releases associated cursor state and admission slots within its configured cleanup bounds. |
| R7 | Kill A mid-session and bring up a replacement with A's slot and a fresh boot. | A's old link stays unavailable. If the static slot still points to A's replacement, it rejects the old boot before session lookup. Neither B nor the replacement runs the original SQL for the continuation. A fresh query through ingress works. |
| R8 | Force the router's new-work view to lag after A starts draining. Try a new REST query, then continue an earlier query on A. | A returns HTTP 503 with `QUERY_OWNER_DRAINING` for the new query. Its existing valid continuations can finish before the deadline. Repeating the private admin drain call leaves the deadline unchanged. |
| R9 | Leave an active SQL stream, native API cursor and in-flight request open at the drain deadline. | The owner reports retired and stops serving continuations, even before the ingress map is reloaded. In-flight work is cancelled or bounded by the shutdown deadline. Cursors and admission slots close before pools. Existing credentials cannot reopen a cursor. |
| R10 | Cancel an in-flight continuation and exhaust a query budget in separate cases. | Cancellation reaches the owner and the supported source. Streaming cleanup releases resources. A budget rejection before advancement preserves the token. Budgets are observed per owner. |
| A1 | Disable or remove the source or change the caller's effective access after the first batch. Include a query that would return only terminal metadata. | The next request checks current policy, returns no rows or columns, and retires the affected session and cursor. |
| A2 | Roll out a policy revision that removes a grant while A still holds the old revision. Send new and continuation requests before and after reopening ingress. | During the rollout barrier no old-policy route can grant access. Old sessions are unavailable after retirement. Before reopening, private status reports the new revision on all mapped owners. Under the new policy, denied requests cannot reveal rows or metadata. |
| M1 | Initialize an MCP session on A. Execute a multi-batch query and remove A from the new-work pool. Send tool calls, a listening GET, and DELETE through ingress. | Every request with the returned transport header reaches A while the session is live. The result batches match the REST expected rows. DELETE ends the session and releases its query resources. |
| M2 | Use A's header with another caller or tenant, omit the header after initialization, or pass query credentials to a second session for the same principal. | The owner rejects invalid identity and session bindings before serving tools or SSE. No result, session notification, or cursor advancement is exposed. |
| M3 | Submit `initialize` or `execute_sql_query` to a draining owner, including via a valid existing transport header. Follow an existing continuation. | New session and query work is rejected. Continuation can finish within the deadline. At cutoff active SSE connections and affected queries terminate. |
| M4 | Expire or terminate the transport session or restart its owner, then use the old header on POST, GET and DELETE. Initialize again and present the old query credentials. | Old headers cannot create new transport state or execute tools. HTTP 404 on lost sessions allows reinitialization. A new transport session cannot reuse the prior query. |
| M5 | Reach the configured transport-session capacity and fail an initialization after a reservation. | Capacity is enforced before admitting more sessions. Failure, DELETE, or expiry releases reservations. Rejected requests do not allocate unbounded owner or ingress state. |
| O1 | Add the new key at ingress and start a new owner using it while the old owner drains. Retire the old key after the old sessions end. Test an unrelated deployment with a different routing scope. | Existing supported markers survive the overlap. Retired keys and wrong scope fail uniformly without routing to another owner. |
| O2 | Run the existing standalone REST and MCP/stdio client flows with owner routing disabled. Check routed responses with an existing REST client and a session-aware MCP client. | The standalone wire contract still works without extra services. Routed clients follow relative URIs and echo MCP session headers without parsing owner markers. |
| O3 | Inspect ingress and owner access logs, metrics, and request traces for successful, denied, expired and failed requests. Check that public ingress cannot expose private replica status or drain and that non-admin direct requests are rejected. | Observations distinguish routing, authorization, rate limiting and drain failures without storing raw session markers, tokens, query parameters or source credentials. |
