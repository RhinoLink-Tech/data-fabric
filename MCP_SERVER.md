# Data Fabric MCP Server

Data Fabric provides an optional MCP server over local stdio or remote Streamable HTTP. Both transports use the configured sources and shared query service. The complete source-build deployment and first-query walkthrough is [Self-hosted Remote MCP Deployment](docs/mcp-deployment.md); it is the recommended path for a reproducible first use.

## Requirements

- Go 1.26.1 or later for running from source.
- A configured, reachable metadata database and a registered source for queries.
- Python 3 and the pinned SQLGlot dependency in requirements-sql-translation.txt. The default runtime uses SQLGlot for read-only SQL analysis as well as translation.
- Start from the repository root so scripts/sql_translate.py can be located.
- The Docker image includes Python and the pinned SQLGlot package.

## Local stdio

MCP is disabled by default. For a trusted local client, enable MCP and select stdio:

~~~yaml
mcp:
  enabled: true
  transport: stdio
~~~

Then start the process from the repository root with go run ./cmd/server. Stdio occupies the process input and output; let the MCP client launch and manage it, and do not redirect application logs to the protocol output.

## Remote Streamable HTTP and authentication

The endpoint defaults to 127.0.0.1:8081 at /mcp. Use direct TLS by setting both tls_cert_file and tls_key_file, or bind to loopback and put an HTTPS reverse proxy in front. Without direct TLS, startup rejects non-loopback bind addresses.

Remote HTTP always requires an Authorization: Bearer token. The verifier is selected by mcp.auth_mode:

- local-jwt is for development. It validates a local HS256 JWT using SECURITY_JWT_SECRET.
- oidc validates an access-token JWT against the configured HTTPS OIDC issuer discovery document and rotating JWKS, then checks issuer, audience, expiry, required scopes, and mapped identity claims. Configure mcp.oidc_issuer, mcp.oidc_audience, and mcp.oidc_required_scopes. The stable sub claim becomes the principal; the configured tenant claim (default tenant) and roles claim (default roles) feed the existing tenant and administrator authorization rules.

For server.mode release, remote MCP must use oidc; release mode also requires management authentication, database-enforced read-only access, and audit to remain enabled. In the documented deployment, REST management uses a short-lived local HS256 JWT while remote MCP uses the identity provider's access token. Do not send the management JWT to MCP or use SECURITY_JWT_SECRET to sign the production OIDC token.

The gateway validates tokens; it does not issue OAuth tokens or provide dynamic client registration. Register each source under the principal and tenant expected from the caller's token. See [MCP deployment](docs/mcp-deployment.md) for the client setup and identity mapping.

## Tools and limits

The server exposes list_data_sources, get_data_source_info, execute_sql_query, fetch_next_batch, validate_sql_query, list_tables, describe_table, translate_sql, and list_supported_dialects. Source information does not include stored usernames or passwords.

Queries remain subject to read-only validation, owner/tenant/source authorization, table/column policies where configured, bounded result and session limits, and audit behavior. SQL translation covers only the documented compatibility subset; successful translation does not guarantee equivalent execution semantics. The support catalog records which connector versions have an end-to-end contract.
