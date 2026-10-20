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

The initial `/fetch` request records one query observation. Continuation requests are counted as HTTP requests and do not increment the query execution counter again.

Metric labels use route templates and bounded values. The endpoint does not export SQL text, data-source IDs, raw unmatched URL paths, or backend error messages. Unknown HTTP methods, unmatched routes, and unknown driver types are grouped into fixed labels.
