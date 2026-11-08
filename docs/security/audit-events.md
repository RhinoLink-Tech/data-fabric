# Access audit events

Data Fabric stores structured access events in its metadata database for REST
API requests and MCP tool calls. Remote MCP authentication failures are logged
without retaining bearer tokens. Events include the verified actor and tenant
when available, transport, operation, data-source ID, decision, reason code,
HTTP status when applicable, duration, returned row count, timestamp, and
correlation ID. Table names are recorded when explicit in the request or
resolved by the table-policy analyzer. When managed table authorization is
active, events include the policy revision pinned to that request. Column
identifiers are not currently produced by that analyzer. Policy publication
outcomes are stored separately in the metadata database transaction that
activates a revision; failure to write that record prevents activation.

Query text, bind values, credentials, result rows, and model reasoning are not
stored. SQL is represented by an HMAC-SHA-256 fingerprint derived from the
configured JWT signing secret when one is configured. For local MCP stdio
deployments without a JWT secret, the process creates an ephemeral key, so
fingerprints are not comparable across restarts. HMAC prevents the audit table
alone from serving as a dictionary for common SQL statements. Keep the signing
secret stable when comparing fingerprints across restarts; rotating it
intentionally changes the fingerprint namespace.

## Configuration

```yaml
audit:
  enabled: true
  retention_days: 90
```

The default retention is 90 days. Expired events are pruned at startup and
daily. Release mode requires auditing to remain enabled. Audit write failures
are reported to the service log and counted by the bounded
`data_fabric_audit_persist_failures_total` Prometheus metric when a recorder is
configured; they do not change a completed query's result. See the
[observability guide](../observability.md#audit-persistence-failures) for alert
and recovery steps. Failed events are not automatically replayed, so an outage
can leave a permanent audit gap even when the query itself succeeded. Public
acceptance case A27 injects this failure only in the disposable synthetic
reference stack, checks the bounded failure counter, restores the metadata
database, and verifies a later correlated audit event.

## Review events

Only callers with the `admin` role may list events, even when
`security.enable_auth` is disabled for the management API:

```http
GET /api/v1/audit/events?limit=100&offset=0&decision=deny
Authorization: Bearer <admin-token>
```

Available filters are `actor_id`, `tenant_id`, `transport`, `operation`,
`data_source_id`, `decision` (`allow`, `deny`, or `error`), `correlation_id`,
`from`, and `to`.
Time filters use RFC 3339. The endpoint returns at most 500 records per page,
ordered newest first. `queryFingerprint` is a correlation value, not executable
SQL and not a substitute for database-native query logs.
