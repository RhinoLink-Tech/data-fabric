# ADR-003 Query continuation across replicas

| Field | Value |
| --- | --- |
| Status | Accepted |
| Date | 2026-11-06 |
| Related | [#365](https://github.com/RhinoLink-Tech/data-fabric/issues/365), [#28](https://github.com/RhinoLink-Tech/data-fabric/issues/28), [#403](https://github.com/RhinoLink-Tech/data-fabric/issues/403) |

## Context

The REST API and remote MCP run in separate server modes. Each process keeps
its own pagination sessions, native API cursors, and live SQL streaming cursors.
A continuation uses the original query ID and slug with the latest rotating
token. The owner serializes advancement and rechecks access against the
authenticated caller, the current data-source record, and its loaded table and
column policy. The baseline absolute query session lifetime is 30 minutes.
Process exit loses all three kinds of continuation. The
[API guide](../api_usage.md#fetch-the-next-batch) describes the current contract.

The current query session creator check uses `Caller.Key()`, which returns the
user ID. Remote MCP validates a Bearer token per HTTP request. The pinned
mcp-go SDK issues an `Mcp-Session-Id` by default and keeps active transport
state in the serving process. Its default ID manager does not enforce owner or
caller binding. In particular, the SDK's GET handler has a separate path from
POST session validation. Admission and request rate limits are also local to
each process.

## Options

| Option | Deployment and resource cost | Failure behavior and security |
| --- | --- | --- |
| Route each continuation to its owner | Optional ingress routing and small signed routing markers. SQL and result state stay local. | Continues while the owner lives. A boot identifier fences replaced processes. Owner authentication and authorization still apply. |
| Share session storage | Requires an additional state store or extending the metadata database, atomic advancement, retention, and storage of sensitive query state. | Can recover selected reconstructable sessions with added semantics. Live SQL connections and in-memory cursors still die with the owner. |
| Reconstruct from a stateless cursor | Adds connector-specific ordering, snapshots, opaque upstream tokens, or durable result materialization. | Re-execution can skip or repeat rows as source data changes. Safe portable replay needs an explicit per-connector contract and independent authorization and expiry checks. |

## Decision

Implement owner routing and bounded drain in optional stages. A standalone
server keeps the current wire values and local session behavior, with no extra
runtime service. In a routed deployment, continuation remains available while
its owner is reachable within the session lifetime. Owner loss requires a new
query, and a lost MCP transport session requires initialization again.

### Routing contract

Each process has an operator-assigned owner slot and a fresh random boot
identifier of at least 128 bits. The ingress uses a static operator-configured
map from slots to private upstreams, separately for REST and MCP. Each entry
also controls eligibility for new work. Use the existing Go HTTP stack and
`httputil.ReverseProxy` for a reference ingress. An existing ingress can
implement the same contract. Keep one live process per slot. A replacement
joining before its predecessor exits uses a different slot.

These are proposed configuration names and defaults for the follow-up.

| Location | Setting | Purpose |
| --- | --- | --- |
| Owner | `routing.enabled` | Defaults to false. Enables the routed contract for this process. |
| Owner and ingress | `routing.deployment_id` | Operator-defined namespace shared by the deployment, included in marker verification. |
| Owner | `routing.owner_slot` | Required when enabled. A short operator-defined identifier drawn from lowercase letters, digits, and hyphens. |
| Owner | `routing.policy_revision` | Required when enabled. The revision of the deployed authentication and access-policy configuration, reported by the owner after startup succeeds. |
| Owner | `routing.drain_timeout_seconds` | Defaults to 120, with a maximum of 1800 seconds. Caps how long the owner serves continuations after entering drain. |
| Owner and ingress | `DATA_FABRIC_ROUTING_KEYS` | Separate routing HMAC key ring with key IDs. Each key contains at least 32 random bytes. |
| Owner | `DATA_FABRIC_ROUTING_ACTIVE_KEY_ID` | Selects the signing key from the routing key ring. |
| Ingress | `rest_upstreams` and `mcp_upstreams` | Separate static maps from slot to operator-configured `url` and `accept_new` values. Validate and atomically replace the in-memory map on operator reload. |
| Remote MCP owner | `mcp.routed_session_ttl_seconds` and `mcp.max_routed_sessions` | Fixed transport lifetime and bounded owner-local registry. Proposed defaults are 3600 seconds and 1024 live sessions. |

Ingress startup validates each private destination and its transport pool.
A continuation marker selects a slot in this fixed map. The boot identifier
is generated and checked by the owner, so the ingress requires no live
registration service. A stale marker may reach a replacement at the same
private URL. The replacement rejects the old boot before looking up a
session or executing SQL.

| Request | Routing key | Behavior |
| --- | --- | --- |
| REST initial `POST /api/v1/fetch` | No continuation key | Select a ready REST owner. |
| REST continuation `GET /api/v1/fetch/{query_id}/{slug}/{token}` | Signed owner marker in the existing opaque `slug`, bound to the path's `query_id` | Send to the REST slot in the static map. The owner verifies its boot. Keep the UUID query ID and rotating token separate and keep `nextUri` relative to the public origin. |
| Remote MCP `POST /mcp` with no `Mcp-Session-Id` | No continuation key | Select a ready MCP owner. The owner accepts a new session only through authenticated initialization. |
| Remote MCP `POST`, `GET`, and `DELETE /mcp` with `Mcp-Session-Id` | Signed owner marker in the transport session ID, with a random session nonce | Send all requests in that transport session to its MCP slot. The owner verifies its boot. The ingress routes by header and leaves JSON-RPC handling to the owner. |

The marker contains a format version, signing key ID, deployment and transport
scope, owner slot, boot identifier, expiry, and the query ID or transport
session nonce. Sign a canonical encoding with a routing-only HMAC key.
Use a bounded URL-safe wire format and reject ambiguous paths or headers,
invalid lengths, versions, signatures, scopes, and expiry before selecting
an upstream. The marker carries
no credentials for source systems, raw SQL, results, or caller attributes. Its
slot selects only an operator-configured private endpoint and never supplies
an address. The receiving owner verifies the signature and checks its
deployment, transport, slot, and boot independently. For key rotation,
provision the verification key ring at ingress before enabling a new signing
key on new owners. Keep old keys while old owners are serving sessions.

Malformed, unknown, expired, or unreachable continuation routes and stale
boot identifiers use a generic unavailable result. REST uses HTTP 404 with
`QUERY_SESSION_UNAVAILABLE`. An invalidated MCP transport session uses
HTTP 404 so the client can initialize again. A request missing a required
MCP session header uses HTTP 400. A routable request missing valid Bearer
authentication still receives HTTP 401 at the owner. A live MCP session with
an unavailable query returns the existing tool error. New work with no
eligible owner receives HTTP 503. A late new REST query on a draining owner
returns HTTP 503 with `QUERY_OWNER_DRAINING`. A late MCP initialization
receives HTTP 503 and an existing MCP session receives a tool error when it
tries to start a new query.

Disable automatic retries, redirects to private endpoints, fallback to other
replicas, and response caching on continuation routes. A REST continuation
GET advances state. If the owner has begun sending a response when the
connection fails, close the response and let the client use its normal
session recovery flow. Log route templates and generic failure reasons.
Redact raw continuation paths, session headers, and tokens in both ingress
and owner logs. Reuse the REST log redaction from
[#403](https://github.com/RhinoLink-Tech/data-fabric/issues/403) once it is
available.

Use the pinned SDK's `WithSessionIdManagerResolver` to issue owner-bound MCP
session IDs during initialization. Validate session state in an authenticated
owner middleware before SDK dispatch for GET, POST, and DELETE. Maintain a
bounded, expiring local registry for transport sessions. Store the verified
principal and expiry, and reject expired, terminated, or unknown sessions.
Enforce the registry's cap before accepting initialization and release
reservations on initialization failure. Bind query state created by
`execute_sql_query` to the validated transport session. A second session with
the same Bearer identity must start a new query. Closing or expiring the
transport session cancels and releases its associated query states.

### Authorization and lifecycle

- Record the verified `(tenant ID, user ID)` as a structured query owner key,
  including the tenant-less case. Use a dedicated session binding so changes
  do not alter admission bucket or history keys. For remote MCP, compare the
  same principal against the transport session and query session on each call.
  This session rule is independent of data-source authorization. The existing
  data-source owner grant can allow a user ID across tenant claims, so source
  access alone does not establish session ownership. Authorize each request
  with current verified roles and scopes. Refreshing a token for the same
  principal preserves the binding.
- Keep the query ID, stable slug, and current token checks and rotate the
  token under the owner's per-session lock. Only one concurrent request can
  advance with a token. A retry after the owner advanced but the response was
  lost returns the stale-token error. Keep the absolute query TTL. Reuse
  `query.session_ttl_seconds` once [#403](https://github.com/RhinoLink-Tech/data-fabric/issues/403)
  is available. Its proposed default and upper bound are 1800 seconds. A zero
  value selects the default. Use the earlier of query and transport expiry
  for remote MCP. Release resources on completion, cancellation and
  expiration. A request denied before source work cannot advance the cursor
  or rotate the token.
- Recheck the current data-source record and permissions before every batch,
  including an empty terminal batch. After a source is disabled, deleted, or
  the continuation is denied by policy, close the affected cursor and
  invalidate the session. Revocation applies to subsequent requests and does
  not recall rows already returned.
- Require the same effective authentication mapping and authorization policy
  revision across every replica serving a pool. Table and column policies are
  loaded locally today. Deploy the same versioned policy configuration and
  revision to each owner. The operator checks the loaded revision through
  the private status endpoint. For a policy rollout that removes access, pause
  affected ingress routes including continuations, stop or cancel old-policy
  work and retire the affected owners, then reopen only after all serving
  owners report the new revision. Prevent direct client access to upstreams.
  Short-lived Bearer expiry or explicit identity-provider controls still
  bound revocation of already issued credentials. A routine binary rollout
  with the same policy revision can drain live continuations.
- Keep query concurrency and request-rate budgets on each owner, using the
  existing admission checks. Limit new work at ingress and configure each
  owner against the total allowed backend load. A tenant that starts on
  multiple owners can use capacity on each, so a strict cluster-wide tenant
  quota needs a separate design.

### Planned drain and compatibility

1. Call the owner directly on its private listener with the existing admin
   Bearer credentials. `POST /internal/replica/drain` starts drain and reports
   its deadline. Repeated calls return the same state and deadline. The
   ingress never exposes this endpoint. The owner rejects new query starts
   and MCP initializations after the drain gate closes, including query starts
   over a live MCP session. Set that slot's `accept_new` to false and reload
   the ingress map. The owner gate covers the interval before the reload.
2. Leave the owner's continuation route in the static map. The private
   admin-only `GET /internal/replica` reports admission state, the drain
   deadline, loaded policy revision, and pending query work. In-progress
   requests can finish within
   the deadline. Honor valid REST and MCP continuation credentials and
   current authorization while draining. The deadline caps the remaining
   lifetime of these sessions.
3. At the deadline or when query work is exhausted, the owner enters retired
   state and closes remaining transport streams and query work. Cancel
   active requests, close native and SQL cursors and release query slots.
   Remove the retired slot from the ingress map, then use the existing
   process shutdown to close pools. A restarted process presents a new boot
   identifier. Unplanned exit makes outstanding continuations for that boot
   unavailable.

Enable routing only after the ingress and all participating replicas support
the marker version. Keep legacy sessions on their original standalone
endpoint until they finish or expire during migration. Existing REST clients
follow the returned URI and treat slug and token as opaque. Existing remote
MCP clients carry the returned session header. Request bodies, tool names,
query result fields, and the default standalone and stdio deployment retain
their current contracts. Cross-transport continuation is outside the routed
contract because REST and remote MCP are separate server processes.

### Implementation slices

1. Add a dedicated principal binding and owner lifecycle state. Provide the
   opt-in REST marker, fixed upstream routing and generation checks, and
   bounded drain for pagination, streaming, and native API sessions. Exercise
   the REST scenarios in the [acceptance matrix](../validation/multi-replica-query-continuation.md).
2. Add the owner-aware remote MCP session contract and drain gate, then run
   the MCP scenarios in the same matrix. Reuse the query authorization and
   lifecycle code from the REST slice.
3. Publish a two-replica deployment example and the observed results after
   the relevant scenarios pass. Reuse the existing single-node deployment
   path and verified source fixtures. The current design and matrix are
   acceptance criteria for these follow-ups.
