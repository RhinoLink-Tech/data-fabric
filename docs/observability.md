# Prometheus Metrics

Data Fabric exposes an admin-authenticated Prometheus scrape endpoint at GET /metrics on both the REST API and remote MCP HTTP server. Each endpoint exposes metrics for its own process; scrape every process/replica and aggregate externally. The MCP request endpoint remains at /mcp.

## Access control

Scraping the REST API always requires a valid Bearer JWT with the admin role, even when security.enable_auth is disabled for the management API. Remote MCP scraping requires a valid MCP Bearer token whose claims include the admin role. Send the token in the standard Authorization header:

~~~bash
curl --fail --silent --show-error --header "Authorization: Bearer ${DATA_FABRIC_ADMIN_TOKEN}" http://localhost:8080/metrics
~~~

The metrics endpoint stays admin-only regardless of the management API setting. Release mode also requires authentication to be enabled for the rest of the service.

## Exported measurements

- `data_fabric_http_requests_total`: completed HTTP requests, labeled by normalized method, Gin route template, and status code.
- `data_fabric_http_request_duration_seconds`, `data_fabric_http_request_size_bytes`, and `data_fabric_http_response_size_bytes`: HTTP latency and payload sizes.
- `data_fabric_query_total`: initial query executions, labeled by configured execution mode and success/error status.
- `data_fabric_query_duration_seconds`: time spent executing the initial query request.
- `data_fabric_query_rows_read_total`: rows returned in the initial batch.
- `data_fabric_query_errors_total`: failed initial executions with a bounded error category.
- `data_fabric_logical_query_outcomes_total`: one terminal observation per tracked logical query, labeled by actual execution mode and bounded outcome.
- `data_fabric_logical_query_duration_seconds`: elapsed time from the initial query through the final page or observed terminal condition.
- `data_fabric_logical_query_rows_returned_total` and `data_fabric_logical_query_batches_total`: successful rows and batches accumulated across the initial response and continuations.
- `data_fabric_query_admission_rejections_total`: requests rejected by the process-local query admission limits, labeled by the fixed `reason` values `concurrency_limit` or `request_rate_limit` (unexpected values are grouped as `other`).
- `data_fabric_query_admission_active_reservations`: active query/cursor reservations on this process, refreshed at scrape time.
- `data_fabric_query_admission_max_reservations`: configured total query/cursor reservation limit for this process.
- `data_fabric_audit_persist_failures_total`: attempted audit event writes that failed, labeled by the bounded `transport` values `rest`, `mcp-http`, `mcp-stdio`, or `other`.
- `data_fabric_managed_table_policy_status`: one-hot status with fixed `state` values `fresh`, `stale`, or `unconfigured`, refreshed on each scrape.
- `data_fabric_managed_table_policy_last_sync_timestamp_seconds`: Unix time of the last successful managed-policy synchronization, or 0 when unconfigured or never synchronized.
- `data_fabric_managed_table_policy_sync_failures_total`: failed managed-policy refresh attempts on this process.

The initial /fetch request records one query observation. Continuation requests are counted as HTTP requests and do not increment the existing query execution counter again. The logical-query measurements are separate and cover the whole result lifecycle: an empty or single-batch result finishes immediately, while a multi-batch result finishes when its last successful page arrives or when a known terminal condition is observed. Budget exhaustion, cancellation or timeout after the serving session ends, authorization denial, session expiry, owner unavailability, backend failure after the session ends, request rejection, and tracker-capacity overflow use bounded outcome values. Retryable admission/rate-limit failures, stale tokens, invalid credentials, and backend failures that leave the session live do not close an active observation.

Use the new counters to inspect result completion and partial delivery:

~~~promql
sum by (outcome) (rate(data_fabric_logical_query_outcomes_total[5m]))
~~~

~~~promql
histogram_quantile(0.95, sum by (le, execution_mode) (rate(data_fabric_logical_query_duration_seconds_bucket[10m])))
~~~

~~~promql
sum by (execution_mode, outcome) (increase(data_fabric_logical_query_rows_returned_total[1h]))
~~~

Existing data_fabric_query_total, data_fabric_query_duration_seconds, and data_fabric_query_rows_read_total keep their initial-request meaning for compatibility. Logical-query state is bounded to 16,384 active observations per process; if that capacity is reached, tracking_overflow is emitted for an untracked continuation result and must be treated as an observability-capacity signal. Session expiry uses the actual local session expiry where available. Measurements are process-local: scrape every replica and aggregate across targets. A process crash loses its in-flight observations, and no metric can be emitted by a process after it has stopped. They do not provide durable or cluster-wide accounting.

Metric labels use route templates and bounded values. The endpoint does not export SQL text, data-source IDs, raw unmatched URL paths, or backend error messages. Unknown HTTP methods, unmatched routes, and unknown driver types are grouped into fixed labels.

Admission rejections are aggregated across tenants and callers. No tenant or user identifiers are included. For example, `rate(data_fabric_query_admission_rejections_total{reason="request_rate_limit"}[5m])` shows the recent per-second rate of requests rejected by the per-tenant request budget, including fail-closed rejection when the bounded tenant bucket table is full; use `reason="concurrency_limit"` to inspect concurrency pressure.

## Admission and resource pressure

The scrape-time gauges
data_fabric_query_admission_active_reservations and
data_fabric_query_admission_max_reservations show the current and configured
query reservation counts for each process. Active reservations include a
streaming query for its active cursor lifetime; pagination reserves only while
a page request is executing. Scrape each REST/MCP process and keep the process
identity when comparing utilization. These values contain no tenant labels and
do not form a cluster-wide quota. The configured maximum is a guardrail, not a
capacity promise beyond the measured profile.

Show the instantaneous per-process reservation utilization while retaining
Prometheus target labels such as instance:

~~~promql
data_fabric_query_admission_active_reservations
/
clamp_min(data_fabric_query_admission_max_reservations, 1)
~~~


Use the rejection counter to distinguish request-rate pressure from exhausted
concurrent-query capacity:

~~~promql
sum by (reason) (rate(data_fabric_query_admission_rejections_total[5m]))
~~~

For an authorized point-in-time view of initialized source connection pools,
query the admin API:

~~~bash
curl --fail --silent --show-error --header "Authorization: Bearer ${DATA_FABRIC_ADMIN_TOKEN}" http://localhost:8080/api/v1/database/connections/stats
~~~

The response includes aggregate open, active, idle, wait-count and wait-duration
values plus per-pool diagnostics. It omits configured sources that have not
initialized a pool. This endpoint is an admin diagnostic snapshot, not a
time-series metric; compare it with the process-local logical-query outcomes,
durations and admission rejection rate. A growing wait duration with
concurrency rejections points to source or pool saturation; request-rate
rejections indicate the configured per-tenant rate budget. Check source
health, long-running result lifecycles and the workload profile before changing
limits. Candidate task SLOs and capacity budgets must be justified by the
end-to-end baseline tracked in [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441).

## Current query and cursor capacity

The measured preview defaults are four active query or stream reservations per
process and two per validated tenant. Configure them with
QUERY_MAX_CONCURRENT_QUERIES and
QUERY_MAX_CONCURRENT_QUERIES_PER_TENANT; each accepts 0 (use its default)
through 100. Values above 100 are rejected at startup, and the limiter itself
also clamps programmatic values to 100. This is a configuration guardrail, not
a measured safe capacity above the four-slot profile. A concurrency rejection
is returned before the source connection is opened.

A streaming reservation stays held while its source cursor is active and is
released on completion, cancellation, deadline, expiry, failed initialization,
explicit close, or a same-process data-source update/deactivation. The pool
invalidation callback closes matching active streams and removes their
continuation sessions. Pagination reserves capacity only while a page request
is executing. Invalidation is process-local: other replicas are not notified
and retain their own cursors until a local close or expiry. Existing
per-source database/sql pool limits still apply as a second bound; the default
pool has at most 10 open connections per source and process. With the measured four active slots, a SQL streaming task may
temporarily use a cursor connection plus a count connection. These are
process-local bounds: the separate REST and MCP processes each have their own
four-slot limit, and every additional replica multiplies it. There is no
cluster-wide tenant quota.

The profile has a 64 MiB per-query returned-result budget, but that is not a
whole-process heap cap or a bound on source scans, object-store buffers, or
external engine work. The #441 three-row fixture does not validate memory use
at the configured result limit. The 39.26 MiB whole-stack residual after the
one-minute wait is an observation, not a proven leak or a resource guarantee.
Do not advertise a hard memory envelope until a representative large-result
profile is measured under explicit container/cgroup limits.

The six rejected tasks in the #441 MCP/PostgreSQL mixed burst were reported as
HTTP/MCP request-rate rejections after the shared request budget had been
consumed by earlier benchmark calls. They are separate from the per-tenant
query-admission counter and do not demonstrate the process concurrency cap.
The load runner records these classes separately; tune HTTP request-rate and
query-request-rate limits deliberately for the deployment rather than raising
the concurrency cap to hide a rate-limit response.

## Managed table-policy freshness and synchronization

Each process reports its own managed table-policy state. Exactly one
`data_fabric_managed_table_policy_status{state=...}` series is 1: `fresh`
means this replica synchronized within the configured maximum staleness window
(3 seconds in the current implementation),
`stale` means its last successful synchronization is too old, and
`unconfigured` means managed table authorization is not active. Scrapes refresh
the state dynamically, so a failed metadata-database connection can become
stale even when the process remains alive. The timestamp stays at the last
successful sync; it is not refreshed by failed attempts.

Alert on a stale configured policy and inspect recent sync failures:

~~~promql
data_fabric_managed_table_policy_status{state="stale"} == 1
~~~

~~~promql
increase(data_fabric_managed_table_policy_sync_failures_total[5m]) > 0
~~~

These measurements are process-local. Scrape every replica; compare revision
and freshness through the admin table-policy status endpoint and, for routed
replicas, each owner's `/internal/replica` status when diagnosing disagreement.
An unconfigured policy is a separate state and should
not be treated as an incident unless the deployment requires managed policy.

On a stale-policy alert, restore metadata-database reachability, credentials,
and write/read availability. Confirm that each replica returns to `fresh` and
that its last-sync timestamp advances. Then verify a new correlated audit
event is persisted. Preserve the affected time range as an audit gap because
failed audit writes are not replayed automatically. The reference public
acceptance case A27 injects a metadata-database outage in its disposable
synthetic stack and checks both signals and recovery; it is not production
availability evidence.

This diagnostic and recovery guidance supports the operator restore work in
[#452](https://github.com/RhinoLink-Tech/data-fabric/issues/452) and informs
the independent adoption evaluation in
[#458](https://github.com/RhinoLink-Tech/data-fabric/issues/458). Synthetic
reference results are engineering evidence only.

## Audit persistence failures

Alert when an attempted audit event write fails:

~~~promql
sum by (transport) (increase(data_fabric_audit_persist_failures_total[5m])) > 0
~~~

The counter only records failed writes when a recorder is configured; it does not measure whether auditing was disabled or prove audit completeness. On an alert, use the service log's correlation ID to locate the failure, check metadata-database reachability, write permissions, capacity, locks, and timeout pressure, then verify that later audit events persist after recovery. Failed events are not automatically replayed, so record the affected time window as an audit gap. The metric is process-local; scrape every replica and aggregate across targets.
