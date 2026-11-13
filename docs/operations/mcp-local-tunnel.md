# Local MCP origin behind a tunnel

This profile runs the existing remote MCP stack on one machine with a seeded
PostgreSQL source. It persists the metadata and sample database in named Docker
volumes and binds both gateway ports to loopback. It requires a real OIDC
provider and a separately managed tunnel; it does not provide either service.
The machine, Docker engine, tunnel connector and identity provider must all be
running when a remote client connects. It is a single-machine demonstration,
not a high-availability deployment.

## Prerequisites

- Follow [the MCP deployment guide](../mcp-deployment.md) to initialize the
  private `.env`, credential keyring, local management secret and TLS files.
  Keep them stable across restarts and back them up securely; losing the vault
  key can make stored source credentials unreadable.
- Configure a real OIDC provider that issues signed JWT access tokens with the
  configured issuer, audience and query scope. For source management, grant
  only authorized operators the `data-fabric.sources.manage` scope and the
  corresponding Data Fabric resource permissions. The reference test issuer
  and its temporary tokens are not a persistent identity service.
- Confirm the client can obtain and renew its access token. A token pasted into
  a static client configuration expires according to its issuer; restarting
  Data Fabric does not renew it. Check the client and provider's supported
  renewal flow before relying on unattended access.
- Install Docker Compose 2.24 or newer. Keep the tunnel connector on the same
  host/network namespace as the loopback listener. If the connector is in a
  separate container or on Windows while the gateway runs in WSL, loopback may
  refer to a different network namespace; explicitly configure and verify
  reachability without exposing the gateway to the public network.

## Start

From `deploy/mcp` after configuring the prerequisites:

```sh
docker compose -p data-fabric-demo \
  -f docker-compose.yml -f docker-compose.postgres.yml \
  -f docker-compose.tunnel.yml up -d --build
```

The API binds to `127.0.0.1:${API_PORT}` (default 8080), and MCP to
`127.0.0.1:${MCP_HTTPS_PORT}` (default 8443). Choose unused fixed ports in
`.env`. The PostgreSQL example currently uses an unpinned `postgres:18` tag;
pin and validate a digest when reproducibility is required. The metadata
database remains MySQL; both databases and the API are private to the host.

Register the sample source and configure policy as described in the
[PostgreSQL section](../mcp-deployment.md#postgresql-sample-source-variant).
Use a stable subject and tenant shared between the appropriate management and
MCP identities. Check the local origin before connecting a tunnel, including
an authorized query and a denied unauthorized request.

Configure the tunnel to forward the public MCP hostname to the local HTTPS
origin and preserve certificate verification: use a certificate trusted by the
connector and an origin name matching its certificate. The generated local
certificate and the public tunnel hostname are distinct; configure the
connector's origin server name and CA accordingly. Never disable origin
certificate verification as a shortcut. The public path must reach `/mcp`.
Test the public endpoint with a valid token and confirm an unauthenticated
request is rejected. Store tunnel and identity credentials outside the repo.

## Restart and recovery

Services use `restart: unless-stopped`; Docker and the tunnel connector must
also start after host reboot. To restart after checking that they are available:

```sh
docker compose -p data-fabric-demo \
  -f docker-compose.yml -f docker-compose.postgres.yml \
  -f docker-compose.tunnel.yml up -d
```

Recheck container health, OIDC token validity, tunnel routing, a query and the
intended management permissions. `docker compose down -v` deletes the named
database volumes: do not use it for a deployment whose data must survive.
For encrypted backups and recovery of metadata and key material, follow the
[backup and recovery runbook](mcp-profile-backup-recovery.md). Test restores
separately before depending on the demonstration.
