# MCP OAuth protected resource discovery (opt-in)

Set DATA_FABRIC_MCP_OAUTH_RESOURCE to the exact external HTTPS MCP URL ending
in /mcp and set the OIDC audience to that same URL. The configured OIDC issuer
is advertised unchanged; it must be the verified issuer for access tokens.
A configuration mismatch fails startup. When omitted, existing behavior remains
unchanged. The server does not infer public URLs from request headers.

The server exposes RFC 9728 protected-resource metadata at
/.well-known/oauth-protected-resource/mcp and a root fallback. A 401 from /mcp
includes its path-specific resource_metadata challenge. A reverse proxy must
forward both metadata paths and /mcp, preserve the configured Host, and
terminate HTTPS. The restricted tunnel bridge also permits these metadata paths.

This only implements resource-server discovery; it does not implement dynamic
client registration, authorization endpoints, token issuance or refresh. Those
must come from the configured authorization server. For WorkBuddy cloud OAuth,
follow the official connector requirements, including platform registration,
callback allowlisting, PKCE and token refresh. The authorization server must
issue access tokens whose audience matches the exact configured resource URI;
verify resource indicator support end to end. This setting does not grant source
management permission, create user accounts or make an identity provider safe
to expose publicly.


The optional WorkBuddy identity proxy template forwards exactly the two
protected-resource metadata paths to the gateway, preserving its fixed public
Host. It does not enable discovery: an unconfigured gateway still returns 404
for metadata paths. The proxy continues to deny unrelated paths, including the
management API. The preview.2 source includes the gateway discovery feature;
an operator must still opt in and configure the proxy and issuer consistently.

Before enabling discovery, verify that existing clients receive tokens with the
configured resource audience. Changing the gateway's accepted audience to the
resource URL can break existing WorkBuddy clients whose tokens use a different
audience. Coordinate the identity-provider audience mapping and compatibility
checks before rollout; preserve the existing default when not explicitly enabled.
Discovery alone does not create clients, register callbacks, or issue tokens.

### Optional legacy-audience transition

For an explicitly planned transition, set `MCP_OIDC_AUDIENCE` and
`DATA_FABRIC_MCP_OAUTH_RESOURCE` to the same canonical HTTPS `/mcp` resource,
and set `MCP_OIDC_LEGACY_AUDIENCE` to one distinct existing audience. The
gateway accepts an access token containing either exact audience, while still
requiring the configured issuer, signature, expiry, scopes and source/tenant
authorization. The legacy setting is empty by default and requires OIDC with
resource discovery. It is a compatibility exception, not a resource-bound
token: any trusted issuer token for the legacy audience that meets the same
scope and authorization checks is accepted. Choose a legacy audience dedicated
to this gateway; do not enable this when that audience is shared with other
services or its token issuance and scopes are not understood.

Configure the identity provider to issue tokens with the resource audience for
new clients, verify both clients' issuance and refresh behavior in isolation,
and inventory existing clients before changing a live service. Discovery and
the dual-audience setting do not add an authorization server, client
registration, audience mapping, or refresh support. Remove the legacy setting
after clients migrate and verify revocation and denied access again. Existing
installations remain unchanged unless the operator explicitly configures it.

## Standard Compose profile

In `deploy/mcp/.env`, optionally set `DATA_FABRIC_MCP_OAUTH_RESOURCE` to the
exact external HTTPS URL ending in `/mcp`, and set `MCP_OIDC_AUDIENCE` to the
same value. Leave the resource blank to keep discovery disabled. Run
`python3 scripts/mcp/onboard.py doctor` to check the URL and audience, then
recreate the MCP container through your normal deployment process. The doctor
checks configuration, not issuer token behavior or external proxy routing.
Docker Compose environment variables exported in the shell take precedence over
`.env` when running Compose directly; verify the effective MCP configuration
without exposing secrets before restarting the container.
Verify both public metadata paths, the 401 challenge, token audience, callback
and refresh using an authorized client before claiming a working connection.
The authorization server must support the chosen client onboarding method.
Changing the audience can invalidate existing WorkBuddy tokens; do not change a
live deployment until the identity provider and all affected clients are ready.

## Check the public proxy

From a host that should reach the deployment, use the exact public resource and
configured issuer (replace the example values):

```sh
python3 scripts/mcp/oauth_probe.py \
  --resource https://fabric.example.com/mcp \
  --issuer https://identity.example.com/realms/team
```

This optional, unauthenticated HTTPS check compares both public metadata paths
with the resource and issuer, and checks that `/mcp` returns a 401 Bearer
challenge pointing to the path-specific metadata. A 404 may indicate a missing
gateway or reverse-proxy route; network or TLS errors require a reachable host
and a trusted certificate. Redirects are not followed. The check sends no
token or source credentials and does not test login, token issuance, refresh,
permissions or actual client use. Use the configured issuer exactly.
