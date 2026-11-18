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
