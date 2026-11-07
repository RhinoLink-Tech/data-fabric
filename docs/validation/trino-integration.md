# Real Trino integration profile

This opt-in profile validates the Data Fabric Trino adapter against the pinned
official `trinodb/trino:484` image. It uses two deterministic TPC-H catalogs
backed by Trino's built-in connector. The normal Go unit-test suite does not
invoke this profile; only this command needs Docker and an image pull on first
use.

## Run

From the repository root, run:

    go run ./deploy/mcp/reference/cmd/trino-acceptance

Docker Engine and Docker Compose v2 are required. Docker pulls the Trino image
on the first run; the image is pinned by digest in
`deploy/mcp/reference/docker.go`. The runner starts a disposable reference
stack, registers Trino through the Data Fabric API, and removes the stack when
the checks finish. The runner logs its Compose project name; if cleanup itself
fails, it additionally prints the temporary Compose environment path for recovery.

## What the profile checks

The public REST API and MCP Streamable HTTP endpoint both run the same checks:

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
