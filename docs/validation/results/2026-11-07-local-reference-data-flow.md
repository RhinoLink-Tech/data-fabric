# Data-flow and storage boundaries: isolated local reference profile

## Approved profile and scope

On 2026-11-07, the maintainer directed that this project must not reuse the Atlas PostgreSQL service and should start as a separate project. This report records that requirement for one isolated, local reference deployment. It uses the repository's synthetic fixtures only; it does not use Atlas or customer data.

This profile describes the local WSL/Docker Engine context. It is not a customer deployment, a geographic residency determination, a production-readiness claim, or a general compliance statement.

## Run evidence

- Compose project: `df-reference-1efa472eb3e9`
- Gateway checkout: `a732dcb9a045347a015114b5e0161493c49b586d`
- Local gateway image: `df-reference-1efa472eb3e9:local`, image ID `sha256:bf037da5edeae757465176e856adadf6b8331b64617e1bc291f0c621e16af90f`
- Runtime: Linux/amd64, Docker Engine 29.8.0, Compose 5.5.1
- Source images: MySQL 8.4.13 and the pinned PostgreSQL 17 Alpine image
- Six containers reached healthy state. API and MCP were published only on loopback: `http://127.0.0.1:58300` and `https://127.0.0.1:58302/mcp`.
- The runner registered both synthetic sources, verified same-tenant reuse over REST and MCP, and confirmed cross-tenant access, a restricted column, and an invalid MCP audience were denied.
- Startup and smoke checks took 1m11s; the reported container memory total was 1,074.67 MiB.
- The runner was invoked with `-keep`. Generated configuration is in a temporary directory with mode 0700; `compose.env` has mode 0600. The generated configuration and TLS/issuer materials are bind-mounted read-only into the relevant containers.
- Project-scoped named volumes hold metadata MySQL, sample MySQL, and sample PostgreSQL files. There is no backup container or configured backup destination.

The run used the [reference runner](../../../deploy/mcp/reference/reference.go), [reference Compose overrides](../../../deploy/mcp/docker-compose.reference.yml), and [base Compose file](../../../deploy/mcp/docker-compose.yml). The repository's [public task acceptance specification](../public-task-acceptance.md) describes the synthetic checks and their limits.

## Flow map

| Information | Route or storage in this profile | Boundary |
| --- | --- | --- |
| Registration and query requests | Local caller to the HTTP API on `127.0.0.1:58300`; REST management authentication uses generated HS256 identities. | The API port is loopback-only. |
| Remote MCP requests | Local caller to HTTPS MCP at `127.0.0.1:58302/mcp`; the generated local OIDC fixture serves discovery and JWKS on the project network. | MCP is loopback-only; no external identity provider is configured. |
| Metadata and policy | API and MCP containers connect over the project Compose network to metadata MySQL. Source profiles and audit events persist there; table and column grants come from the project's read-only configuration bind mount. | Metadata MySQL has no published host port; its files are in the project-scoped Docker volume. |
| Source queries | Gateway containers connect over the project Compose network to the synthetic MySQL and PostgreSQL services using generated reader credentials. | Neither source database has a published host port. Only these two synthetic sources are registered. |
| Source credentials | The runner generates credentials locally. Compose passes required values to containers; the gateway credential vault encrypts source passwords and sensitive additional properties in metadata; other connection fields remain configuration. | The Docker Engine administrator can inspect container configuration and therefore remains inside the trust boundary. |
| Identity and TLS keys | The runner generates test identities and keys locally. The local OIDC fixture exposes public discovery/JWKS documents; gateway and issuer key files are read-only mounts from the private temporary directory. | No external identity provider or remote key service is used. |
| Query results and intermediate state | Rows flow from a source through an API/MCP process to the local caller. Query session/result handling is in the gateway processes; this profile configures no result database or object-store destination. | Results leave a source only for the gateway and local caller in this profile. |
| Audit events | Events are persisted in the metadata database's `audit_events` table. The event model records decisions, query fingerprints, row counts, status, and duration; it excludes raw result rows, bearer tokens, and raw SQL. | Audit retention defaults to 90 days and the server prunes expired rows at startup and periodically; this profile does not forward events to a remote sink. |
| Runtime logs | Gateway and fixture output is available through the local Docker Engine; runner output stays in the local terminal. | No remote log shipper is configured. |
| Backups | No backup service or backup job is configured. Data remains in the three project volumes while the kept stack exists. | To remove this synthetic data, use a project-scoped Compose `down --volumes` with this project name and the runner's generated environment and Compose files; do not use an unscoped `down`. This profile has no external backup copy. |

The Compose services share the project-scoped default network `df-reference-1efa472eb3e9_default`. The running profile does not attach an Atlas container or register an Atlas source.

## Boundary limits and unresolved items

- The configured ports are loopback-only and the databases have no host-published ports. This establishes the observed local ingress boundary for this run.
- The default Compose network does not declare `internal: true` or an egress allowlist. Container outbound connectivity is not blocked by this profile. The instruction not to reuse Atlas was followed in source registration, but this run does not prove firewall-level isolation from every host or network endpoint.
- Docker reports the service volumes as Docker-managed named volumes. This run did not establish their physical backing path or geographic location on the host.
- No real team records, customer credentials, external model, external identity provider, remote log sink, or backup destination were configured.
- The local reference acceptance proves only the listed synthetic flows. It does not establish that customer data stays within a jurisdiction or that a production deployment satisfies a residency policy.

## Issue #83 disposition

The maintainer supplied a concrete project-level network isolation requirement: do not reuse the Atlas PostgreSQL service and use a separate Data Fabric project. This report maps the resulting local profile, its request, credential, metadata, audit, result, intermediate, and backup flows, and records the boundaries that remain unverified. It closes #83 only for this bounded local reference profile. Any future customer or production residency profile needs its own approved location, egress policy, identity, log-retention, and backup requirements.
