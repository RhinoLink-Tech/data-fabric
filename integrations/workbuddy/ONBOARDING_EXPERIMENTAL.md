# WorkBuddy browser onboarding: isolated test deployment

This is an experimental, opt-in deployment mode for validating the path from a
WorkBuddy MCP client to an independently authenticated browser. It is **not** a
release or marketplace setup guide. Do not enable it on a shared or production
instance until the complete identity, tenant isolation, and end-to-end review
is complete. It does not implement the full Data Fabric connector catalog.

The toggle `DATA_FABRIC_WORKBUDDY_ONBOARDING=true` applies a fixed-IP egress
policy to the **entire MCP instance**, including existing sources, metadata,
health checks, and queries. Use a separate metadata database and an isolated
MCP instance. Never run an unrestricted Data Fabric API against that database
or provide another unrestricted database access path. Existing unsupported
sources will stop working in this instance. The policy supports only MySQL and
PostgreSQL with pinned IPv4 destinations. Verified TLS uses system trust or
an operator-provisioned CA; see [TLS source setup](../../docs/workbuddy-tls-sources.md).
Plaintext connections remain available only for environments where the operator
accepts that risk. Policy changes require restarting and draining every instance
and existing connection.

The MCP listener must bind to a loopback IP and an HTTPS reverse proxy must
forward only the trusted public hostname. Never trust arbitrary forwarding
headers or expose that loopback port remotely. MCP must use OIDC and audit must
be enabled; the browser issuer must equal the configured MCP issuer. The same
issuer must be able to issue browser authorization-code/PKCE ID tokens and MCP
access tokens whose subject and tenant match. Browser management grants are
independent of OAuth scopes and are rechecked for every operation. Missing or
revoked grants fail closed. Grant provisioning, revocation and explicit restoration are provided only by
the local operator command below. They require a reviewed operator workflow and
a deployment without any unrestricted writer; there is no web grant API.

When this opt-in integration is enabled, rename, status, delete and connectivity
tests require both the management scope and a current matching grant, including
for administrators. Once revocation completes, older tokens cannot start these
operations. Already running operations finish before revocation returns. Query
authorization is unchanged. The grant check and source mutations commit in the same metadata transaction.
Connection tests can continue briefly after cancellation or revocation because
an already-started network probe cannot be rolled back; no metadata is mutated
by the probe. Management requests have a 30-second deadline and connection
tests have a shorter deadline.

Settings in addition to the normal MCP/OIDC/audit/vault configuration:

- `DATA_FABRIC_WORKBUDDY_ORIGIN`: fixed externally visible HTTPS origin.
- `DATA_FABRIC_WORKBUDDY_OIDC_CLIENT_ID` and
  `DATA_FABRIC_WORKBUDDY_OIDC_CLIENT_SECRET`: browser OIDC client credentials.
- `DATA_FABRIC_WORKBUDDY_OIDC_AUTH_URL` and
  `DATA_FABRIC_WORKBUDDY_OIDC_TOKEN_URL`: the issuer's discovery endpoints;
  they are checked against discovery at startup.
- `DATA_FABRIC_WORKBUDDY_ALLOWED_CIDRS`: comma-separated IPv4 CIDRs.
- `DATA_FABRIC_WORKBUDDY_ALLOWED_PORTS`: comma-separated TCP ports. The
  destination must match both CIDR and port. CIDRs and ports are policy, not
  client-supplied inputs; use the narrowest possible values.

Register the exact redirect `https://<origin>/onboarding/auth/callback` with
the identity provider. Protect secrets using the deployment's secret mechanism;
never paste them into chat or repository files. The browser supplies database
credentials directly to the Data Fabric service; a flow URL is only a
correlation identifier and is not authorization. Existing access-token and
browser-session authorizations are both required. Current sessions have bounded
lifetimes and locally revocable grants, but IdP revocation is not propagated in
real time.

The candidate binary also has an OS-level operator command, available only
when the same onboarding configuration is enabled:

```sh
data-fabric workbuddy-grant add --subject '<oidc-subject>' --tenant '<tenant>'
data-fabric workbuddy-grant revoke --subject '<oidc-subject>' --tenant '<tenant>'
data-fabric workbuddy-grant restore --subject '<oidc-subject>' --tenant '<tenant>'
```

The local OS account and database credentials must be restricted to authorized
operators; the command does not independently authenticate a human operator.
Existing owner/tenant conflicts are checked before adding or restoring, and a
successful grant mutation is written with its audit event in the same database
transaction. An unrestricted service can race that check; it must not share this
metadata database. `add` cannot silently reactivate a revoked grant: an
operator must use `restore` after rechecking authorization. The command records
the OS account, which may identify a container service account rather than an
individual. Review this and the grant lifecycle before external use.

This candidate still needs signed OIDC and real database tests through both
streamable and owner-routed transports, independent review, and independent review of the local grant lifecycle. It must not be submitted to WorkBuddy as the complete product.

## Host tunnel bridge (isolated mode)

`deploy/mcp/docker-compose.onboarding-tunnel.yml` enables a restricted bridge listener in the gateway process. The normal gateway listener remains on `127.0.0.1:8081` inside the container. The bridge dispatches only `/mcp` and `/onboarding/*` on port 8082 to the same handler; Docker publishes that port only to host loopback. The bridge also listens on the container network interfaces, so isolate the Compose network from untrusted containers; only expose it through an authenticated HTTPS tunnel. Point the host tunnel at the published loopback port and route both paths on the same HTTPS origin. Keep request, query, authorization, and cookie logging disabled in every upstream proxy and tunnel.

Compose 2.24+ supports the `!override` tags. The overlay disables the legacy API by profile and removes it from MCP dependencies; do not start that API or any unrestricted instance against this metadata database. Set the variables in the overlay, use a trusted identity provider, provision and audit grants, and configure explicit source networks and ports. This bridge does not provide TLS itself: the host tunnel must terminate HTTPS. `DATA_FABRIC_WORKBUDDY_ORIGIN` must be the exact external HTTPS origin.

This overlay only addresses the container-to-host networking issue. It does not establish production readiness, certify continuous availability, configure an identity provider, or validate every connector. The initial onboarding egress mode has additional restrictions above. Recreating a sample database container may change its IP; source addresses and explicit network policy must remain consistent.

## Onboarding request budget

All onboarding HTTP routes share one MySQL-backed fixed-window budget across
replicas: 300 requests per minute, including static assets and invalid paths.
It does not use forwarded headers or persist flow IDs. Over-budget requests
receive HTTP 429; metadata database failures and backwards clock movement
fail closed with HTTP 503. Requests for unrelated MCP routes are unchanged.
A fixed window may admit bursts across its boundary. Because the host tunnel
does not provide a trusted per-user address, all clients share this quota; a
noisy client can temporarily affect others. Use a separately configured edge
rate limit for additional network protection and capacity planning.
