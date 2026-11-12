# Self-Hosted Remote MCP Deployment

中文部署说明：[docs/mcp-deployment-zh.md](mcp-deployment-zh.md)。

This guide takes a clean environment to a healthy, authenticated remote MCP
endpoint for Data Fabric: a metadata database, a seeded sample source, and the
gateway serving MCP over Streamable HTTP with TLS (#186). Everything runs on
your own infrastructure — no external cloud accounts, no production data, and
no credentials committed to the repository. **Deployment and source
credentials belong to you.**

For using MCP locally next to your editor instead, read
[Local stdio vs remote Streamable HTTP](#local-stdio-vs-remote-streamable-http)
first — the quickstart below is the remote path.

This is the current synthetic first-use profile linked from the [first-adopter brief](product/first-adopter-profile.md). The brief remains a hypothesis: no external team evaluation or production claim is recorded.

## Current published Preview

`v2.1.0-preview.1` was published on 2026-11-11. The GHCR package is public, and the published-image workflow and exact-digest clean-runner acceptance passed. For a no-compile installation, use [Run a published image without compiling source](#run-a-published-image-without-compiling-source) and copy the tag plus digest from the [GitHub Release](https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v2.1.0-preview.1). This release is limited to a single `linux/amd64` host; it has no HA, automatic failover, response-time SLA, or in-place upgrade path from v2.0.0. External first-use evidence remains open under [#442](https://github.com/RhinoLink-Tech/data-fabric/issues/442) and [#446](https://github.com/RhinoLink-Tech/data-fabric/issues/446).

## What gets deployed

When run from a Git checkout, the initializer records the checkout SHA in `.env`; local Compose builds label the image with that revision so a recovery manifest can bind source and image identity. Source archives are marked unknown and cannot produce a verified candidate backup. The source-build Compose profile in [`deploy/mcp/`](../deploy/mcp/) builds one
image from the repository `Dockerfile`; the release overlay pulls an immutable
image digest. Both profiles run four services on a private network. The
gateway runs either the HTTP API or MCP in a given process — never both — so
the stack runs one container per mode against the shared metadata database:

```
MCP client ──HTTPS + IdP access token──▶ data-fabric-mcp :8081 (MCP, direct TLS)
operator  ──HTTP, loopback────────▶ data-fabric-api :8080 (mgmt API)
                                       ├──▶ metadata-db   (MySQL, internal)
                                       └──▶ sample-source (MySQL or PostgreSQL, internal)
```

| Service | Purpose |
| --- | --- |
| `metadata-db` | Stores the configured data sources; schema is migrated automatically at startup. Not published. |
| `sample-source` | MySQL by default, or PostgreSQL via [`docker-compose.postgres.yml`](../deploy/mcp/docker-compose.postgres.yml): a read-only user and three seeded `orders` rows for the first query. Not published. |
| `data-fabric-api` | The gateway's HTTP management API on loopback: registration, health, readiness. |
| `data-fabric-mcp` | The gateway's remote MCP endpoint: Streamable HTTP with direct TLS. Starts after `data-fabric-api` is healthy so schema initialization is serialized. |

## Optional: local DataSemanticHub integration

The standard profile owns its metadata and sample databases. For a container-level
check against an existing local MySQL/MariaDB network, use the API-only
[external MySQL Compose profile](../deploy/mcp/docker-compose.external-mysql.yml)
and the WSL acceptance runner:

```bash
cd /home/fengfei/data-fabric
scripts/mcp/dsh-local-acceptance.sh \
  --image 'ghcr.io/rhinolink-tech/data-fabric@sha256:<64-hex-digest>'
```

The runner defaults to `/home/fengfei/DataSemanticHub/deploy`,
`datasemantichub-mariadb`, and `datasemantichub-network`; override those with
`--deploy-dir`, `--db-container`, and `--network` when needed. It verifies
the deploy directory's MariaDB service, starts that existing container only
when it was stopped, and joins one Data Fabric API container to the existing
network. The API listens on loopback port 18180 by default; `--api-port`
changes it.

The runner requires an immutable Data Fabric image that is already present in
the local Docker engine. It never pulls images. It creates uniquely named,
synthetic metadata and source databases; the source account receives only
`SELECT` and `SHOW VIEW`. It checks API health and authentication, registers
the synthetic source, and queries two seeded rows across the Docker network.
Cleanup removes the temporary API project, schemas, accounts, and local secret
files, then restores the MariaDB container to its original running/stopped
state. Existing DataSemanticHub schemas and data are not used.

This local profile covers the release image's REST and MySQL runtime path. It
does not exercise OIDC/MCP, replicas, anonymous GHCR pulls, or the independent
operator rehearsal required by [issue #446](https://github.com/RhinoLink-Tech/data-fabric/issues/446).
Keep the published-image workflow and independent rehearsal as separate
release gates.

## Prerequisites

- Docker Engine 24+ with Docker Compose plugin 2.24+ (the release and PostgreSQL overlays use Compose merge tags)
- `openssl` for the local TLS certificate
- An OIDC-compatible identity provider configured to issue access tokens for
  the Data Fabric MCP audience and required query scope
- `python3` (standard library only) for setup, diagnostics and short-lived management tokens

## Quickstart

All commands run from `deploy/mcp/`.

### 1. Initialize secrets and local TLS

```bash
python3 ../../scripts/mcp/onboard.py init
```

The initializer creates `deploy/mcp/.env` with fresh database passwords, a
32-byte credential-vault key, a management JWT secret and a localhost
self-signed certificate. It writes `.env` atomically with mode `0600`, creates
the private key with mode `0640` for the configured container group, and never
replaces non-empty secrets or existing TLS files. Re-running it fills only
missing values. It refuses malformed or ambiguous existing keyrings, keyring
active-ID mismatches, and partial TLS pairs instead of guessing or overwriting
data.

Set `MCP_OIDC_ISSUER` and `MCP_OIDC_AUDIENCE` in `.env` to the HTTPS issuer and
resource audience registered with your identity provider. Keep production
identity-provider access tokens separate from the local management API token.

### 2. TLS certificate and private-key access

The initializer creates a self-signed certificate for `localhost`,
`data-fabric.local`, and `127.0.0.1`. Use a real certificate pair for an
internet-facing deployment. When replacing the files, keep the private key
readable by the non-root MCP container through the numeric
`DATA_FABRIC_TLS_GID` supplementary group; use a restricted group and mode
`0640`. Do not make the private key world-readable. The directory must allow
that group to traverse it.

### 3. Start the stack

```bash
docker compose up -d --build
```

### 4. Verify readiness

```bash
docker compose ps                 # all four services report "healthy"
curl -s http://127.0.0.1:8080/health && echo
curl -sk https://localhost:8443/mcp -o /dev/null -w '%{http_code}\n'   # 401
```

Run the local profile doctor after the services start:

```bash
python3 ../../scripts/mcp/onboard.py doctor
```

It checks Compose configuration and service health, the API health endpoint,
the MCP TLS/auth boundary, and the packaged SQL translation parser. It does not
probe every registered data source. An unauthenticated `401` from the MCP probe
is expected and confirms that TLS and the authentication gate respond.

The `data-fabric-mcp` container's own health check is a TCP probe on its
listener — an MCP-only process serves no HTTP `/health` — and the `401` from
curl is the real end-to-end confirmation: TLS is up and the authentication
gate is rejecting an unauthenticated request before any MCP processing.

### 5. Prepare management and MCP access tokens

The local HS256 token below is for the HTTP management API only. Remote MCP
uses an access token issued by your OIDC identity provider; Data Fabric
validates it against the configured discovery metadata and signing keys. The
deployment does not run an identity provider or mint OIDC access tokens.

```bash
# Use the stable `sub` value included in your identity provider's access token.
export DF_USER_ID='<stable OIDC sub>'
export DF_TENANT_ID='quickstart'
export DF_MANAGEMENT_TOKEN="$(python3 ../../scripts/mcp/onboard.py token \
  --user "$DF_USER_ID" --tenant "$DF_TENANT_ID")"
```

The helper signs a local management API token with a 15-minute lifetime by
default (the maximum is 15 minutes). It emits only the token on stdout so the
shell can capture it; it does not print or persist the token. The token has no
admin role unless `--admin` is explicitly requested. Do not use this local
HS256 token for remote MCP.

Obtain an OIDC-signed JWT access token from your provider using its supported
OAuth flow, then set it in this shell:

```bash
export MCP_ACCESS_TOKEN='<OIDC-signed JWT access token>'
```

Check the configured issuer and compare the provider token's unverified
`sub`/tenant/audience/scope claims with the management principal before using
both paths. The gateway still validates the signed token on every MCP request;
this local claim inspection is advisory only:

```bash
python3 ../../scripts/mcp/onboard.py doctor --probe-oidc \
  --api-user "$DF_USER_ID" --tenant "$DF_TENANT_ID"
```

`--probe-oidc` fetches the discovery document and JWKS over HTTPS and checks the issuer
match and key-set response. Omit the flag when outbound
identity-provider access is not allowed.

Opaque access tokens and ID tokens are not accepted. The token issuer must
match `MCP_OIDC_ISSUER`, its audience must match `MCP_OIDC_AUDIENCE`, and its
scopes must include
every value in `MCP_OIDC_REQUIRED_SCOPES` (default: `data-fabric.query`).
Configure the provider's token claims to include a stable `sub`; optional
tenant, roles, and username claims can be mapped through the `MCP_OIDC_*`
settings. The sample source is registered in tenant `quickstart`, so the
provider token needs the configured tenant claim set to `quickstart`; alternatively,
register it with an API identity whose subject matches the MCP token's `sub`.

Use the helper's `--admin` flag only for management API admin-only endpoints
or cross-owner visibility. Re-mint the management token immediately before
registration if its 15-minute lifetime expires.

To diagnose a selected connection before saving it, give the doctor one source
configuration rather than probing every registered source. Store the JSON in
an owner-only file (`chmod 600 source.json`) or pipe it on stdin, then run
`python3 ../../scripts/mcp/onboard.py doctor --source-type mysql
--source-config source.json` with `DF_MANAGEMENT_TOKEN` set. The check posts the
configuration only to the loopback management API, never prints source
credentials, and returns a correlation ID for sanitized follow-up. A
connection check is successful only when the API reports a healthy connection;
HTTP 200 with an unhealthy connection is reported as a failure. Delete any
temporary file when finished.

### 6. Register the sample source

```bash
SAMPLE_READER_PASSWORD=$(grep -E '^SAMPLE_READER_PASSWORD=' .env | cut -d= -f2-)

curl -s http://127.0.0.1:8080/api/v1/datasources \
  -H "Authorization: Bearer $DF_MANAGEMENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"sample-mysql\",
    \"type\": \"mysql\",
    \"config\": {
      \"host\": \"sample-source\",
      \"port\": 3306,
      \"database\": \"sample\",
      \"username\": \"sample_reader\",
      \"password\": \"$SAMPLE_READER_PASSWORD\"
    }
  }"
```

Data sources are scoped by owner, tenant, and role: whoever registers a source
owns it, and this source is owned by `$DF_USER_ID` — the same principal the MCP
client presents in the next step. To serve several clients, either share one
identity, give the tokens a common `tenantId`, or register with an admin
identity (admins read every source); a token with a different user and no
shared tenant sees none of them.

Note the response `id` — that is the `datasource_id` tools take. Credentials
are encrypted at rest with the credential-vault key from step 1 and are never
returned by any API or tool.

### 7. Connect a client and run the first query
For the embedded management page's copyable MCP setup, first-query prompt, and second-application identity steps, see the [management UI guide](data-source-management-ui.md).

With Claude Code (self-signed trial certificate shown; export
`NODE_EXTRA_CA_CERTS` to the full path of `tls/tls.crt`):

```bash
export NODE_EXTRA_CA_CERTS=$PWD/tls/tls.crt
claude mcp add --transport http data-fabric https://localhost:8443/mcp \
  --header "Authorization: Bearer $MCP_ACCESS_TOKEN"
```

Then, inside a session, ask for the round trip the deployment is built for:

1. `list_data_sources` — the client discovers `sample-mysql`.
2. `list_tables` — the `orders` table.
3. `describe_table` — column names, types, and nullability of `orders`.
4. `execute_sql_query` — `SELECT customer, amount FROM orders ORDER BY amount DESC`.

The query returns the seeded rows. For continuation, ordering, and data changes between pages, see [query result semantics](query-result-semantics.md). Write statements (`INSERT`, `UPDATE`,
`DROP`, …) are rejected twice over: by the gateway's read-only validation and
by the read-only session opened against MySQL — the sample source's user also
holds only `SELECT` privileges.

## Run a published image without compiling source

The source-build quickstart above remains useful for contributors. The release path runs both gateway processes from the same published Preview tag@sha256 image and does not build Go or install Python packages on the host. Use the reference only after the tag workflow succeeds and the matching GitHub Release records the public digest.

1. Copy the `tag@sha256` image reference from the matching GitHub Release record and add it to `deploy/mcp/.env`:

   ```dotenv
   DATA_FABRIC_IMAGE=ghcr.io/rhinolink-tech/data-fabric:<tag>@sha256:<digest>
   ```

2. From `deploy/mcp/`, pull and start the release profile:

   ```bash
   docker compose -f docker-compose.yml -f docker-compose.release.yml pull
   docker compose -f docker-compose.yml -f docker-compose.release.yml up -d
   ```

The release overlay removes the Compose build definition and always pulls the selected image. Use only the version and digest recorded by the successful tag workflow and matching GitHub Release; the placeholder above is not a usable image reference. If that release record is not available, continue using the source-build quickstart.

**Published Preview (2026-11-11):** `v2.1.0-preview.1` is available from the public GHCR package at `ghcr.io/rhinolink-tech/data-fabric:v2.1.0-preview.1@sha256:7d47073fe806879968ed3c66f9d3b2b7b7a2020f419738c2cbb1b89d58f1ebbf`. The matching [GitHub Release](https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v2.1.0-preview.1) records the source revision, digest, provenance and clean-runner acceptance. Release publication and image acceptance are complete; issue #442 remains open for its remaining first-adopter evidence. The Preview scope and limitations above still apply.

## PostgreSQL sample source variant

This optional source override is outside the first MySQL Preview profile. Its `postgres:18` tag is not digest-pinned; pin and revalidate it before using the override as a reproducible release profile.

The quickstart seeds MySQL by default. To try the stack against PostgreSQL
instead, apply the override file when bringing the stack up (Compose 2.24+
for the `!override` tags; the metadata database stays MySQL, only the
queryable sample source changes flavor):

```bash
docker compose -f docker-compose.yml -f docker-compose.postgres.yml up -d --build
```

The variant reuses the same `.env` secrets and keeps its data in a separate
named volume, so switching back to the MySQL variant later preserves both
data sets. The container's `postgres` superuser initializes the database;
`sample-source-init-postgres.sh` creates the same read-only `sample_reader`
account and `orders` rows.

Register it with the PostgreSQL connection parameters — note `type` and
`port` differ from step 6:

```bash
curl -s http://127.0.0.1:8080/api/v1/datasources \
  -H "Authorization: Bearer $DF_MANAGEMENT_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"sample-postgres\",
    \"type\": \"postgresql\",
    \"config\": {
      \"host\": \"sample-source\",
      \"port\": 5432,
      \"database\": \"sample\",
      \"username\": \"sample_reader\",
      \"password\": \"$SAMPLE_READER_PASSWORD\"
    }
  }"
```

Write statements are rejected twice over, exactly as with MySQL: by the
gateway's read-only validation and by the read-only session opened against
PostgreSQL (`default_transaction_read_only=on` on every connection) — the
sample source's role also holds only `SELECT` privileges.

## Client configuration

### Claude Code

```bash
claude mcp add --transport http data-fabric https://<host>:8443/mcp \
  --header "Authorization: Bearer $MCP_ACCESS_TOKEN"
```

With a self-signed certificate, start `claude` with
`NODE_EXTRA_CA_CERTS=/path/to/tls.crt` so the Node runtime trusts it.

### Codex

Add the server to `~/.codex/config.toml`:

```toml
[mcp_servers.data-fabric]
url = "https://<host>:8443/mcp"
http_headers = { "Authorization" = "Bearer <OIDC access token>" }
```

Codex verifies TLS with the system trust store: add the certificate to it
(`security add-trusted-cert` on macOS, `update-ca-certificates` on Debian)
or use a real certificate.

### DeepSeek-backed agent host

A direct DeepSeek API integration needs a local agent client to translate
function calls into authenticated Data Fabric MCP calls. The model API alone
does not connect to MCP or execute tools.

See the runnable [DeepSeek function-calling reference client](../examples/deepseek-agent/README.md)
for prerequisites, environment variables, bounded read-only SQL, pagination,
credential handling, and checks. It uses the current MCP Python SDK v2 client
transport. Applications that already use Codex, Deep Code, or another MCP-capable
host can connect that host directly and install the shared Agent Skill instead.

## Local stdio vs remote Streamable HTTP

| | stdio (local) | Streamable HTTP (remote) |
| --- | --- | --- |
| Start | `mcp.enabled: true`, `transport: "stdio"` from the repo | The Compose stack above, or your own deployment |
| Audience | One trusted local client process | Remote authenticated clients |
| Identity | Runs with your OS user; tools act as the local system identity | Every request carries an OIDC access token; identity comes from verified token claims |
| Transport security | OS process pipes | HTTPS required; without TLS files the listener only binds loopback |
| Data-source scoping | Trusted local access | Owner, tenant, and admin rules enforced per request |

Run both against the same metadata database and the sources you registered are
available either way; only the identity and transport differ.

## TLS and network exposure

The Compose default terminates TLS directly on the gateway with `./tls` and
publishes the endpoint on all interfaces. Alternatives:

- **Reverse proxy.** Set `MCP_ADDRESS: 127.0.0.1:8081`, drop the
  `MCP_TLS_*` variables, and front the container with your HTTPS proxy. Inside
  Compose, give the proxy the gateway's network namespace
  (`network_mode: "service:data-fabric"`) so loopback stays loopback; outside
  Compose, run the proxy on the same host.
- **Personal remote access without public TLS.** Keep everything on loopback
  and reach it through an SSH tunnel:
  `ssh -L 8443:localhost:8443 user@server`.

The plain-HTTP management API is published to loopback only. Expose it through
the same proxy if it must leave the machine. The MySQL services publish
nothing.

## Authentication model

- Every MCP request — initialize, tool call, session GET — must carry
  `Authorization: Bearer <OIDC access token>` and is verified per request.
  Missing, invalid, expired, wrong-issuer, wrong-audience, or under-scoped
  tokens are rejected before any tool logic.
- Remote MCP validates the signature using the issuer's HTTPS discovery
  metadata and JWKS, then checks `iss`, `aud`, `exp`, `nbf`, and configured
  scopes. The standard `sub` is the caller identity; configured tenant, roles,
  and username claims are propagated to the shared authorization context.
- The HTTP management API continues to use the local `JWT_SECRET`; it is
  separate from the OIDC access token used by remote MCP. Local MCP
  `local-jwt` mode is intended for development; release-mode remote MCP
  requires OIDC.
- Query history is scoped to the token identity; one client never sees
  another's queries.
- Data Fabric does not provide an OAuth authorization server or dynamic
  client registration. Your identity provider issues and rotates the access
  tokens; Data Fabric validates them as a protected resource.

For the optional owner-routed multi-replica topology, bounded MCP session
routing, and private drain workflow, see [Replica routing and drain](replica-routing.md).

## Operations

```bash
docker compose ps                     # health at a glance
docker compose logs -f data-fabric-api data-fabric-mcp
docker compose restart data-fabric-mcp
docker compose down                   # stop; add -v to also drop both databases
```

For an encrypted metadata/keyring backup and a fresh-project restore, follow the [profile backup and recovery runbook](operations/mcp-profile-backup-recovery.md), also available [in Chinese](operations/mcp-profile-backup-recovery-zh.md).

Secrets live only in `.env` and the containers. Rotating `JWT_SECRET`
invalidates every token; rotating the credential-vault key means re-entering
source passwords (register the source again, or rotate using the keyring's
multiple-key form with the old key still present).

## Security notes

- No real credentials are committed to the repository: `.env` and `tls/` are
  yours alone, and startup validation rejects known placeholder secrets.
- The gateway validates queries as read-only at the application layer **and**
  opens read-only sessions on engines that support them (#20); the sample
  source additionally holds only `SELECT` grants. Defense in depth.
- Tools never return data-source credentials or management secrets; metadata
  tools (`list_tables`, `describe_table`) return schema only, never rows.
