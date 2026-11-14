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
