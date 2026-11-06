# ADR-004: Versioned table-policy publication and revocation

| Field | Value |
| --- | --- |
| Status | Accepted |
| Date | 2026-11-06 |
| Related | [#99](https://github.com/RhinoLink-Tech/data-fabric/issues/99), [#365](https://github.com/RhinoLink-Tech/data-fabric/issues/365), [#409](https://github.com/RhinoLink-Tech/data-fabric/issues/409), [#403](https://github.com/RhinoLink-Tech/data-fabric/issues/403) |

## Context

Table and column authorization is currently supplied as startup configuration. Updating a grant across REST and remote MCP owners requires a consistent activation point, a bounded period during which owners may hold an older snapshot, safe behavior when an owner loses synchronization, and a way to identify the policy used for each request. The deployment already requires a shared metadata database; adding a separate coordination service would increase the single-replica operating burden.

## Decision

Use the existing transactional metadata database as the policy authority. Do not add another mandatory service.

### Bootstrap and authority

- When table authorization is first enabled and no managed head exists, startup validates the YAML policy and seeds immutable revision 1.
- Once the managed head exists, it is authoritative. Startup YAML cannot disable, replace, or rewind it. This prevents an owner with stale local configuration from reopening a revoked grant.
- Owners in one deployment must use the same transactional metadata database connection. Policy synchronization must not read from an asynchronously lagging replica.

### Publication and rollback

- Administrators preflight a complete enabled policy, then publish with an expected revision.
- Publication uses compare-and-swap against the database head. A stale writer receives a conflict and cannot overwrite a newer version.
- A valid publication stores a new immutable version, advances the head, and stores its publisher and outcome in one database transaction. Invalid input and conflicts retain the active version and record their outcome when the database is available.
- If the policy publication record cannot be committed, activation does not occur. API responses expose whether audit was recorded.
- Historical versions are available for inspection. Rollback copies a historical configuration into a new, monotonically increasing revision; it never moves the head backward.
- REST admin endpoints and conditional remote MCP admin tools expose the same preflight, publication, status and history behavior. The existing role system remains the authorization boundary.

### Request snapshots and revocation

- REST requests and MCP tool calls pin one immutable policy snapshot at their request boundary. A request never mixes policy revisions.
- Every new query, metadata operation and continuation is checked against the snapshot pinned for that request. A continuation denied under the new policy is retired with its cursor/session resources.
- An already-running request may finish against its pinned snapshot. Subsequent continuation requests are separate requests and must pass current authorization.
- Owners poll the shared database every 250 ms. The declared revocation propagation window is three seconds. The reference two-REST/two-MCP-owner acceptance requires every owner to report the published revision as fresh within that window before the change is considered converged.
- Freshness is based on the last successful synchronization. After three seconds without a successful sync, an owner fails closed for table queries and table metadata, including continuation fetches. Its status reports active revision, last successful sync, freshness and the last synchronization error.

### Audit boundary

The policy-version publisher/outcome journal is committed atomically with policy activation. A failed journal write prevents activation. Query and metadata access events include the policy revision pinned to their request when managed authorization is active. General access-event storage retains its existing failure semantics; this ADR does not guarantee durable storage of every access when the general audit writer fails.

Static `routing.policy_revision` and the dynamic table-policy revision are separate values. The former identifies deployed routing/authentication configuration; the latter identifies the active table/column policy.

## Consequences

- Single-replica mode continues to work with the metadata database already required by the service.
- A database interruption can make table-governed reads and metadata unavailable after the freshness bound; the service does not fall back to an older grant.
- Administrators must use the current expected revision and re-read after a conflict.
- Rollback is auditable and monotonic but requires publishing a new revision.
- Data-source ownership and tenant authorization remain first; general ABAC, policy UI, row-level filtering and compliance certification remain out of scope.
