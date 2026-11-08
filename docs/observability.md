# Prometheus Metrics

Data Fabric exposes a Prometheus scrape endpoint at `GET /metrics` when running in HTTP server mode.

## Access control

Scraping always requires a valid Bearer JWT with the `admin` role, even when `security.enable_auth` is disabled for the management API. Send the token in the standard `Authorization` header:

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
- `data_fabric_query_admission_rejections_total`: requests rejected by the process-local query admission limits, labeled by the fixed `reason` values `concurrency_limit` or `request_rate_limit` (unexpected values are grouped as `other`).
- `data_fabric_audit_persist_failures_total`: attempted audit event writes that failed, labeled by the bounded `transport` values `rest`, `mcp-http`, `mcp-stdio`, or `other`.

The initial `/fetch` request records one query observation. Continuation requests are counted as HTTP requests and do not increment the query execution counter again.

Metric labels use route templates and bounded values. The endpoint does not export SQL text, data-source IDs, raw unmatched URL paths, or backend error messages. Unknown HTTP methods, unmatched routes, and unknown driver types are grouped into fixed labels.

Admission rejections are aggregated across tenants and callers. No tenant or user identifiers are included. For example, `rate(data_fabric_query_admission_rejections_total{reason="request_rate_limit"}[5m])` shows the recent per-second rate of requests rejected by the per-tenant request budget, including fail-closed rejection when the bounded tenant bucket table is full; use `reason="concurrency_limit"` to inspect concurrency pressure.

## Audit persistence failures

Alert when an attempted audit event write fails:

~~~promql
sum by (transport) (increase(data_fabric_audit_persist_failures_total[5m])) > 0
~~~

The counter only records failed writes when a recorder is configured; it does not measure whether auditing was disabled or prove audit completeness. On an alert, use the service log's correlation ID to locate the failure, check metadata-database reachability, write permissions, capacity, locks, and timeout pressure, then verify that later audit events persist after recovery. Failed events are not automatically replayed, so record the affected time window as an audit gap. The metric is process-local; scrape every replica and aggregate across targets.
