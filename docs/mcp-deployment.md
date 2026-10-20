# Self-Hosted Remote MCP Deployment

This guide takes a clean environment to a healthy, authenticated remote MCP
endpoint for Data Fabric: a metadata database, a seeded sample source, and the
gateway serving MCP over Streamable HTTP with TLS (#186). Everything runs on
your own infrastructure — no external cloud accounts, no production data, and
no credentials committed to the repository. **Deployment and source
credentials belong to you.**

For using MCP locally next to your editor instead, read
[Local stdio vs remote Streamable HTTP](#local-stdio-vs-remote-streamable-http)
first — the quickstart below is the remote path.

## What gets deployed

The Compose stack in [`deploy/mcp/`](../deploy/mcp/) builds one image from the
repository `Dockerfile` and runs four services on a private network. The
gateway runs either the HTTP API or MCP in a given process — never both — so
the stack runs one container per mode against the shared metadata database:

```
MCP client ──HTTPS + Bearer JWT──▶ data-fabric-mcp :8081 (MCP, direct TLS)
operator  ──HTTP, loopback────────▶ data-fabric-api :8080 (mgmt API)
                                       ├──▶ metadata-db   (MySQL, internal)
                                       └──▶ sample-source (MySQL, internal)
```

| Service | Purpose |
| --- | --- |
| `metadata-db` | Stores the configured data sources; schema is migrated automatically at startup. Not published. |
| `sample-source` | MySQL with a read-only user and three seeded `orders` rows for the first query. Not published. |
| `data-fabric-api` | The gateway's HTTP management API on loopback: registration, health, readiness. |
| `data-fabric-mcp` | The gateway's remote MCP endpoint: Streamable HTTP with direct TLS. Starts after `data-fabric-api` is healthy so schema initialization is serialized. |

## Prerequisites

- Docker Engine 24+ with the Compose plugin
- `openssl` for secrets and a self-signed certificate
- `python3` with [PyJWT](https://pypi.org/project/PyJWT/) (`pip install pyjwt`)
  to mint Bearer tokens

## Quickstart

All commands run from `deploy/mcp/`.

### 1. Create the deployment secrets

```bash
cp .env.example .env
```

Fill in every value in `.env`. Generate each secret with
`openssl rand -base64 32`; for the credential vault, wrap one generated key in
the JSON object shown in the file:

```bash
KEY=$(openssl rand -base64 32)
# DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=key-1
# DATA_FABRIC_CREDENTIAL_KEYS={"key-1":"<value of $KEY>"}
```

The gateway fails fast at startup when a secret is missing, a known
placeholder, or too short — placeholder-only secrets are a hard requirement
here, not a suggestion.

### 2. Create the TLS certificate

The MCP listener refuses to bind a non-loopback address without TLS, so the
stack serves HTTPS directly. For a private trial, generate a self-signed
certificate covering the name clients will use:

```bash
mkdir -p tls
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout tls/tls.key -out tls/tls.crt \
  -subj "/CN=data-fabric.local" \
  -addext "subjectAltName=DNS:data-fabric.local,DNS:localhost,IP:127.0.0.1"
```

For internet-facing deployments, use a real certificate for your domain in
place of both files — nothing else changes.

The gateway containers run as a non-root user, so make sure that user can read
the mounted key. For the quickstart:

```bash
chmod 644 tls/tls.crt tls/tls.key
```

For a longer-lived deployment, prefer tightening ownership to the container's
UID over leaving the private key world-readable.

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

The `data-fabric-mcp` container's own health check is a TCP probe on its
listener — an MCP-only process serves no HTTP `/health` — and the `401` from
curl is the real end-to-end confirmation: TLS is up and the authentication
gate is rejecting an unauthenticated request before any MCP processing.

### 5. Mint a client token

Tokens are HS256 JWTs signed with your `JWT_SECRET`; you issue them yourself
— the deployment includes token *verification*, not an OAuth server.

```bash
export JWT_SECRET=$(grep -E '^JWT_SECRET=' .env | cut -d= -f2-)

# One non-admin identity both registers the sample source and queries it, so
# the quickstart needs no role or tenant plumbing.
export AGENT_JWT=$(python3 -c "import jwt,time; print(jwt.encode({'userId':'agent-1','username':'agent-1','iat':int(time.time()),'exp':int(time.time())+86400}, '$JWT_SECRET', algorithm='HS256'))")
```

Mint a token with `'roles':['admin']` only when you need the admin-only
endpoints (fleet health, connections stats) or cross-owner visibility.

### 6. Register the sample source

```bash
SAMPLE_READER_PASSWORD=$(grep -E '^SAMPLE_READER_PASSWORD=' .env | cut -d= -f2-)

curl -s http://127.0.0.1:8080/api/v1/datasources \
  -H "Authorization: Bearer $AGENT_JWT" \
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
owns it, and this source is owned by `agent-1` — the same identity the MCP
client presents in the next step. To serve several clients, either share one
identity, give the tokens a common `tenantId`, or register with an admin
identity (admins read every source); a token with a different user and no
shared tenant sees none of them.

Note the response `id` — that is the `datasource_id` tools take. Credentials
are encrypted at rest with the credential-vault key from step 1 and are never
returned by any API or tool.

### 7. Connect a client and run the first query

With Claude Code (self-signed trial certificate shown; export
`NODE_EXTRA_CA_CERTS` to the full path of `tls/tls.crt`):

```bash
export NODE_EXTRA_CA_CERTS=$PWD/tls/tls.crt
claude mcp add --transport http data-fabric https://localhost:8443/mcp \
  --header "Authorization: Bearer $AGENT_JWT"
```

Then, inside a session, ask for the round trip the deployment is built for:

1. `list_data_sources` — the client discovers `sample-mysql`.
2. `list_tables` — the `orders` table.
3. `describe_table` — column names, types, and nullability of `orders`.
4. `execute_sql_query` — `SELECT customer, amount FROM orders ORDER BY amount DESC`.

The query returns the seeded rows. Write statements (`INSERT`, `UPDATE`,
`DROP`, …) are rejected twice over: by the gateway's read-only validation and
by the read-only session opened against MySQL — the sample source's user also
holds only `SELECT` privileges.

## Client configuration

### Claude Code

```bash
claude mcp add --transport http data-fabric https://<host>:8443/mcp \
  --header "Authorization: Bearer $AGENT_JWT"
```

With a self-signed certificate, start `claude` with
`NODE_EXTRA_CA_CERTS=/path/to/tls.crt` so the Node runtime trusts it.

### Codex

Add the server to `~/.codex/config.toml`:

```toml
[mcp_servers.data-fabric]
url = "https://<host>:8443/mcp"
http_headers = { "Authorization" = "Bearer <agent token>" }
```

Codex verifies TLS with the system trust store: add the certificate to it
(`security add-trusted-cert` on macOS, `update-ca-certificates` on Debian)
or use a real certificate.

### DeepSeek-backed agent host

Any MCP client SDK works; the snippet below is the Python `mcp` package
(`pip install mcp`) — point the model side at DeepSeek with your own API key,
which never touches this deployment:

```python
import asyncio
from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client

URL = "https://localhost:8443/mcp"
TOKEN = "<agent token>"

async def main():
    async with streamablehttp_client(URL, headers={"Authorization": f"Bearer {TOKEN}"}) as (read, write, _):
        async with ClientSession(read, write) as session:
            await session.initialize()
            tools = await session.list_tools()
            print("tools:", [t.name for t in tools.tools])
            result = await session.call_tool("list_data_sources", {})
            print(result.content[0].text)

asyncio.run(main())
```

With a self-signed certificate, import it into the system trust store so the
client's default verification accepts it.

## Local stdio vs remote Streamable HTTP

| | stdio (local) | Streamable HTTP (remote) |
| --- | --- | --- |
| Start | `mcp.enabled: true`, `transport: "stdio"` from the repo | The Compose stack above, or your own deployment |
| Audience | One trusted local client process | Remote authenticated clients |
| Identity | Runs with your OS user; tools act as the local system identity | Every request carries a Bearer JWT; identity comes from token claims |
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
  `Authorization: Bearer <JWT>` and is re-verified per request; missing,
  invalid, or expired tokens are rejected before any tool logic.
- Tokens are HS256 JWTs signed with your `JWT_SECRET`, with `exp` required.
  Identity is the standard `sub` claim (legacy `userId` also accepted);
  optional `tenantId` and `roles` (`admin`) drive data-source visibility.
- Query history is scoped to the token identity; one client never sees
  another's queries.
- There is no OAuth authorization server or dynamic client registration:
  rotating a client means minting a new token with your own tooling.

## Operations

```bash
docker compose ps                     # health at a glance
docker compose logs -f data-fabric-api data-fabric-mcp
docker compose restart data-fabric-mcp
docker compose down                   # stop; add -v to also drop both databases
```

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
