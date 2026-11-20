# Experimental local SQLite profile

[中文](sqlite-first-use-zh.md) · [SQLite prototype](sqlite-metadata-prototype.md) · [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444)

This is an isolated, **local-only developer evaluation** using the unpublished SQLite/Core image. It exposes the REST API and TLS MCP only on the host's loopback interface, uses short-lived local JWTs, and does not provide public WorkBuddy/OAuth authorization. SQLite is debug-only and is not a supported production or high-availability profile. The Core image includes MySQL, MariaDB, PostgreSQL and Trino, not the full connector catalog.

Requirements: Linux with Docker Compose, Python 3 and OpenSSL. Building locally downloads base images and dependencies; remote CI can validate the image instead. Use an isolated machine and data source. Docker Desktop and other host-network arrangements are not covered by the recorded Linux CI evidence.

From the repository root:

```sh
cd deploy/sqlite
python3 ../../scripts/sqlite/init.py
docker compose --project-name data-fabric-sqlite up --build --detach --wait
curl --fail http://127.0.0.1:8080/health
```

Initialization creates an owner-only `.env` and a localhost certificate in `tls/`; it keeps existing valid values. If the metadata volume already exists while `.env` is missing, it stops rather than silently generating incompatible keys. Preserve and separately protect the original `.env` and credential keyring: losing the keyring makes stored source credentials unreadable. The TLS certificate is self-signed, so clients must explicitly trust `tls/tls.crt`; never bypass certificate verification. Both host ports default to loopback and can be changed through the protected `.env`.

Create a local test JWT for the same identity used by REST and MCP:

```sh
DF_TOKEN="$(python3 ../../scripts/mcp/onboard.py token --directory . --user local-operator --tenant local)"
curl --fail -H "Authorization: Bearer $DF_TOKEN" http://127.0.0.1:8080/api/v1/datasources
```

The token is valid for 15 minutes by default and is not automatically refreshed. Rerun the token command when it expires; do not put it in chat, source control or shared logs. MCP is at `https://localhost:8443/mcp` using the same token. Whether a specific client can trust this certificate and configure the token must be verified in that client. This profile does not implement the public WorkBuddy automatic OAuth flow.

Register an authorized read-only data source through the REST API, then query it through REST or MCP; see [API usage](../api_usage.md) and [MCP tools](../mcp-tools.md). For a database on the Docker host, the profile provides `host.docker.internal`; a database must be reachable from the containers and the supplied account should be limited to the intended tables.

Stop without deleting persistent metadata:

```sh
docker compose --project-name data-fabric-sqlite down
```

Do not add `--volumes` unless intentionally discarding the metadata. See the [prototype backup and restore procedure](sqlite-metadata-prototype.md#experimental-backup-and-restore); store snapshots and key backups separately from the live volume. The remote CI acceptance exercises a synthetic PostgreSQL source, TLS MCP, cross-tenant denial, snapshot restore and restart; it does not establish all database, client, platform or disaster-recovery behavior.
