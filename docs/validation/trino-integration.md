# Real Trino integration profile

This opt-in profile validates the Data Fabric Trino adapter against the pinned
official `trinodb/trino:484` image. It uses two deterministic TPC-H catalogs
backed by Trino's built-in connector, plus separate synthetic MySQL and
PostgreSQL databases. The normal Go unit-test suite does not invoke this
profile. The default command needs Docker and may pull images on first use.

## Run

From the repository root, run:

    go run ./deploy/mcp/reference/cmd/trino-acceptance

To run only the real MySQL/PostgreSQL cross-source scenario:

    go run ./deploy/mcp/reference/cmd/trino-acceptance -federated-only

To reuse already-cached, digest-pinned database/Trino images and an existing
Data Fabric image without building or pulling any image:

    go run ./deploy/mcp/reference/cmd/trino-acceptance -federated-only -no-pull -image YOUR_LOCAL_GATEWAY_IMAGE

The existing image must include the reference profile's gateway and Python
OIDC fixture dependencies. A cached gateway image may be older than the checked
out source: inspect its revision label and do not treat such a run as evidence
for the current checkout. Missing images make the no-pull run fail rather than
download them. The GitHub workflow uses a freshly built candidate on its remote
runner; it does not download images to the developer's machine.

Docker Engine and Docker Compose v2 are required. The default command pulls missing images
on the first run; the image is pinned by digest in
`deploy/mcp/reference/docker.go`. The runner starts a disposable reference
stack, registers Trino through the Data Fabric API, and removes the stack when
the checks finish. The runner logs its Compose project name; if cleanup itself
fails, it additionally prints the temporary Compose environment path for recovery.

## What the profile checks

The public REST API and MCP Streamable HTTP endpoint both run the same checks:

- A read-only join from MySQL `mysql.sample.orders` to PostgreSQL
  `postgresql.public.order_items`, with exact rows, one-row continuation pages,
  and a rejected restricted-column query through both REST and MCP.
- Read-only join across `tpch.tiny.nation` and
  `tpch_copy.tiny.region`, with exact columns and three deterministic rows.
- Coordinator outage while a query is submitted, followed by coordinator
  recovery and a successful cross-catalog query.
- A one-second query deadline against `tpch.sf100.lineitem`; the profile
  observes the query become active and then terminal in the Trino coordinator
  query list after the gateway returns `QUERY_TIMEOUT`.
- A representative Trino engine error; the public response must use the
  sanitized `QUERY_ENGINE_ERROR` contract.

The pinned image supplies the Trino coordinator. The catalog property files in
`deploy/mcp/trino/reference/` define two independent catalog names using the
TPC-H connector; Trino generates deterministic data instead of requiring an
external warehouse.

## Tested scope and limits

This profile records compatibility evidence for Trino 484 and the listed
read-only query, error, timeout, and recovery paths through Data Fabric's public
REST and MCP surfaces. It does not establish a compatibility range for other
Trino releases, authentication modes, connectors, SQL features, distributed
worker topologies, or production data volumes. The `sf100` scan is used only to
exercise cancellation; its timing depends on the local Docker host.

## Cross-source architecture and credentials

The gateway registers the Trino coordinator as a single Data Fabric source.
Trino's MySQL and PostgreSQL connectors access separate disposable databases;
the gateway does not automatically federate arbitrary independently registered
sources. Each catalog uses the synthetic database's read-only account and a
fresh per-run password. Catalog files are generated in the private temporary
run directory and removed with the isolated Compose project. The example does
not configure TLS between containers and is suitable only for its private test
network; configure authenticated, encrypted upstream connections for deployment.
The gateway's table policy grants only the columns used by the example.

The GitHub `Federated MySQL and PostgreSQL acceptance` workflow runs the two
federated cases against a candidate built from the checked-out source and saves
JSONL evidence. A local `-no-pull` run provides evidence only for the actual
cached gateway image that was used, not automatically for current source.
