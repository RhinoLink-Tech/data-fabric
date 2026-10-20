# Data Fabric MCP Server

Data Fabric exposes an optional MCP server over stdio or authenticated Streamable HTTP. Both transports reuse the configured data sources and unified query service. Remote calls require a standard Authorization: Bearer JWT and are authorized using the token identity.

## Requirements

- Go 1.26.1 or later.
- A configured metadata database.
- Python 3 with sqlglot installed when SQL dialect translation tools are used.
- Start the application from the repository root so the translation script can be located.

## Configure and run

Set the following in configs/config.yaml:

~~~yaml
mcp:
  enabled: true
  transport: "stdio"
~~~

Then run:

~~~bash
go run ./cmd/server
~~~

MCP is disabled by default. Stdio mode should be launched and managed by a trusted local MCP client. Do not redirect application output to the protocol client's stdout.

For a remote endpoint, set `transport: "streamable-http"`. It listens on `127.0.0.1:8081` by default at `/mcp`; put it behind an HTTPS reverse proxy, or configure both `tls_cert_file` and `tls_key_file` for direct TLS. Without direct TLS, startup rejects non-loopback bind addresses. Every HTTP request requires an unexpired Bearer JWT signed with the configured `SECURITY_JWT_SECRET`. Tokens must include `exp` and a `sub` identity claim (legacy `userId` is accepted); the token identity is used for owner and tenant authorization. The deployment's trusted identity service or operator issues these tokens. See [MCP 使用说明](MCP_DOCUMENTATION.md) for configuration examples.

## Tools

- list_data_sources
- get_data_source_info (does not return usernames or passwords)
- execute_sql_query
- fetch_next_batch (uses the query_id, slug and token returned by execute_sql_query)
- validate_sql_query
- list_tables
- translate_sql
- list_supported_dialects

SQL translation supports MySQL, PostgreSQL, Trino, Spark, Oracle, SQL Server, Snowflake, SQLite, DuckDB and Redshift, with aliases for some compatible database types. Successful translation does not guarantee semantic equivalence on the target database.

## Security

The stdio process runs with the permissions of its parent client and should be limited to trusted local clients. Streamable HTTP requires a valid Bearer JWT on every request and applies the existing owner, tenant, and administrator data-source rules. Protect remote traffic with HTTPS and use least-privilege database accounts. The self-hosted verifier does not provide an OAuth authorization server or dynamic client registration.
