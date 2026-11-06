# Heterogeneous data access reference

The reference command starts a private synthetic deployment with PostgreSQL and
MySQL query sources. An application registers the sources through the HTTP
API. A second application from the same tenant discovers and queries both
through the HTTP and remote MCP entrances. A third application exercises the
cross-tenant denial. A restricted column and an invalid MCP audience also
exercise the gateway's access controls.

## Run the reference

Run these commands from the repository root on a machine with the repository's
Go toolchain, Docker Engine or Docker Desktop, and Docker Compose 2.24.4 or
later. The Docker engine must be able to bind mount local files and publish
ports to the machine running the command.

```sh
go run ./deploy/mcp/reference/cmd
```

The first run may pull the pinned source images and build the gateway. To use
an already built local gateway image, pass its tag or ID. The caller controls
which revision that image represents.

```sh
go run ./deploy/mcp/reference/cmd -image data-fabric:local
```

Each run gets a separate Compose project, volumes, test CA, and random
loopback ports. It records the checkout, image, preparation and startup
times, source registration, and second-application reuse. The resource
snapshot reports each container's memory and the combined total at the end of
the smoke checks. These timings and measurements describe that run and depend
on the machine and image cache.
The scripted clients share a loopback peer, so the reference profile uses
bounded request limits of 600 per minute with a burst of 120. It retains
independent limits for concurrent queries.

## What runs

| Container | Role |
| --- | --- |
| `metadata-db` | Shared MySQL 8.4.13 metadata store |
| `sample-source` | MySQL 8.4.13 query source |
| `sample-postgres` | PostgreSQL 17.11 query source |
| `data-fabric-api` | HTTP registration, discovery and read queries using synthetic HS256 identities |
| `data-fabric-mcp` | Streamable HTTP over TLS using synthetic OIDC access tokens |
| `oidc-fixture` | Private HTTPS discovery and JWKS fixture |

The source image digests match the existing driver contract checks. Both
databases use the synthetic sample tables and SELECT-only accounts. The two
gateway processes use one set of source, table and column rules. The runner
first starts the API with a deny-by-default policy, registers the source UUIDs,
and loads the corresponding grants before testing both entrances.

The test issuer publishes only its discovery and public key documents. The
runner generates the signing key and short-lived access tokens in memory.
Database credentials and ephemeral TLS keys are held in a temporary local
directory or container configuration. Keep that material private and avoid
copying resolved Compose configuration or raw logs into reports. The standard
[self-hosted MCP guide](mcp-deployment.md) covers connecting a deployment to
its own OIDC provider.

## Inspect and clean up

The runner removes its containers, network, volumes and temporary files on
exit. It removes its own gateway image tag when it built the image. Pass
`-keep` to retain the synthetic stack for local diagnostics. The command
prints the project name and path to the generated `compose.env`. Use these
values in the following commands from the repository root.

```sh
docker compose -p PROJECT --env-file ENV_PATH -f deploy/mcp/docker-compose.yml -f deploy/mcp/docker-compose.reference.yml ps
docker compose -p PROJECT --env-file ENV_PATH -f deploy/mcp/docker-compose.yml -f deploy/mcp/docker-compose.reference.yml down --volumes --remove-orphans
```

After taking down a retained stack, remove its temporary directory and any
gateway image tagged `PROJECT:local` that the run built. Leave a prebuilt
image supplied through `-image` available for future runs.
