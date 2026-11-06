# Replica routing and drain

Replica routing keeps paginated queries, native SQL cursors, and remote MCP
sessions on the process that created them. The public ingress reads a signed
owner marker and sends continuations to a fixed operator-configured private
upstream. Query results, SQL and source credentials are never carried in a
routing marker. A process restart changes its boot identifier, so markers from
the previous process become unavailable and clients start a new query or MCP
session.

Routing is opt-in. Standalone REST, remote MCP without routing, and stdio keep
their existing local behavior. Enable routing only after every owner and the
ingress use the same marker contract and a shared verification key ring.

## Topology

```text
REST client -- HTTPS --> public ingress -- private HTTP(S) --> REST owner A
                         /api/v1/fetch/...                   REST owner B

MCP client -- HTTPS --> public ingress -- private HTTPS --> MCP owner A
                        /mcp + Mcp-Session-Id               MCP owner B
```

REST and MCP use separate upstream pools. A process serves one mode, matching
the existing server startup model. Put the owner listeners on a private
network reachable only by the ingress and operator tooling. The reference
ingress binds to loopback by default and should sit behind the organization's
TLS reverse proxy or load balancer.

## Owner configuration

Enable the routed REST process or remote MCP process with a unique slot:

```yaml
routing:
  enabled: true
  deployment_id: "analytics-prod"
  owner_slot: "rest-a"
  policy_revision: "policy-2026-11-06"
  drain_timeout_seconds: 120
```

For MCP owners, use a distinct MCP-pool slot and configure the bounded
transport session registry:

```yaml
routing:
  enabled: true
  deployment_id: "analytics-prod"
  owner_slot: "mcp-a"
  policy_revision: "policy-2026-11-06"
  drain_timeout_seconds: 120

mcp:
  enabled: true
  transport: "streamable-http"
  address: "0.0.0.0:8081"
  tls_cert_file: "/run/secrets/mcp.crt"
  tls_key_file: "/run/secrets/mcp.key"
  routed_session_ttl_seconds: 3600
  max_routed_sessions: 1024
```

The signing key ring is independent of JWT and credential-vault keys. Generate
at least 32 random bytes, encode with unpadded base64url, and distribute the
same key ring to each owner and ingress:

```bash
openssl rand -base64 48 | tr '+/' '-_' | tr -d '=\n'
export DATA_FABRIC_ROUTING_KEYS='{"key-2026-11":"<base64url-key>"}'
export DATA_FABRIC_ROUTING_ACTIVE_KEY_ID='key-2026-11'
```

Keep the previous verification key in the ring while an old owner can still
serve sessions. Install the new key on every ingress first, then configure
new owners to sign with it. Remove the old key after its owners and sessions
retire. The owner slot and deployment ID must be lowercase letters, digits
and hyphens. Use one live process per slot. A replacement that starts before
the old process exits must use a new slot.

All owners serving the same pool must load identical authentication mapping
and access-policy configuration. Set the same `policy_revision` only after
deploying the corresponding policy. The private status endpoint reports the
loaded revision. During a policy change, pause affected routes, retire old
owners and verify the new revision on every serving owner before reopening
the routes.

## Reference ingress

The reference router starts from a single JSON file. Copy the example at
[deploy/replica-ingress/config.example.json](../deploy/replica-ingress/config.example.json)
and replace service names with private upstream addresses:

```json
{
  "deployment_id": "analytics-prod",
  "rest_upstreams": {
    "rest-a": {"url": "https://rest-a.internal:8443", "accept_new": true},
    "rest-b": {"url": "https://rest-b.internal:8443", "accept_new": true}
  },
  "mcp_upstreams": {
    "mcp-a": {"url": "https://mcp-a.internal:8443", "accept_new": true},
    "mcp-b": {"url": "https://mcp-b.internal:8443", "accept_new": true}
  }
}
```

Build and run the reference ingress from the repository root:

```bash
go build -o ./bin/replica-ingress ./cmd/replica-ingress
./bin/replica-ingress -listen 127.0.0.1:8082 -config ./deploy/replica-ingress/config.json
```

The ingress validates the deployment ID, fixed URL origins, slot labels and
pool size at startup. Upstream hosts must be private IPs, single-label service
names, or names under `.internal`, `.local`, `.localhost`, `.svc`, or
`.cluster.local`. It accepts only public health/API paths and `/mcp`;
private status and drain routes are never forwarded. A reload validates the
whole replacement map before atomically switching it:

```bash
kill -HUP "$(pgrep -x replica-ingress)"
```

A failed reload leaves the previous map active. The reference process has no
live registration service or mutable client-selected target URL. Use an
existing reverse proxy or service mesh instead if it implements the same
routing contract and disables retries and redirects on continuation requests.

The ingress forwards new work round-robin to entries with `accept_new: true`.
For a REST continuation it verifies the signed `slug` and path query ID, then
uses the marker's slot in the fixed REST map. For remote MCP it verifies the
signed `Mcp-Session-Id` header and forwards POST, GET and DELETE to that
owner. It does not retry, redirect, cache or fail over a continuation. A
failed continuation returns a generic unavailable response. The client uses
its existing recovery flow and starts a new query or transport session.

## Draining an owner

Call the owner's private listener directly with an administrator Bearer token.
The ingress intentionally does not expose either route.

REST owner:

```bash
curl -H "Authorization: Bearer $ADMIN_JWT" http://rest-a.internal:8080/internal/replica
curl -X POST -H "Authorization: Bearer $ADMIN_JWT" http://rest-a.internal:8080/internal/replica/drain
```

Remote MCP owner:

```bash
curl -H "Authorization: Bearer $ADMIN_OIDC_TOKEN" https://mcp-a.internal:8443/internal/replica
curl -X POST -H "Authorization: Bearer $ADMIN_OIDC_TOKEN" https://mcp-a.internal:8443/internal/replica/drain
```

The MCP token must be a valid configured access token with the administrator
role. Repeated drain requests return the same deadline. After starting drain,
set the owner's `accept_new` value to `false` and reload the ingress map. Keep
the owner in the map so existing continuations still reach it. The owner also
gates new REST query starts and MCP initialization itself, which covers the
interval before the ingress reload. A live MCP session may continue an
existing query but cannot start a new SQL query after drain begins.

Poll the private status route. It reports `state`, `drainDeadline`,
`pendingSessions`, `inFlight`, `cleanupComplete` and `policyRevision`.
Continuations remain available while the owner drains and the deadline has
not passed. When the work completes, the owner retires itself and releases
transport sessions, native cursors, SQL streams and query admission slots.
At the deadline, in-flight work is canceled and the owner becomes unavailable.
After cleanup completes, remove the slot from both owner maps and stop the
process. A restarted owner gets a new boot identifier and cannot resume the
old sessions.

## Wire behavior

| Request | Owner selection |
| --- | --- |
| REST `POST /api/v1/fetch` | Any configured REST entry with `accept_new: true` |
| REST `GET /api/v1/fetch/{query_id}/{slug}/{token}` | Signed REST owner marker bound to the path query ID |
| MCP `POST /mcp` without `Mcp-Session-Id` | Any configured MCP entry with `accept_new: true` |
| MCP POST, GET or DELETE with `Mcp-Session-Id` | Signed MCP session marker |

The marker is opaque to clients. Existing REST clients follow the returned
relative `nextUri`; MCP clients echo the returned session header. Each
continuation still authenticates the caller and checks the current source and
table/column policy on the owner. A different user or tenant cannot advance a
session, even if the source's own sharing policy would otherwise grant access.

If a process exits, loses its SQL cursor, or retires after the drain deadline,
its continuations return unavailable and must be restarted as new work. Routing
does not replicate query data or create cluster-wide quota counters. Query
concurrency and request-rate budgets remain per owner.
