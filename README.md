# Data Fabric — Lightweight AI Data Access Gateway

<div align="center">
  <img src="assets/data-fabric-icon.png" alt="Data Fabric logo" width="160">
  <p><strong>Connect AI applications to many data sources through one lightweight gateway and a shared SQL interface.</strong></p>
  <p><a href="README_zh.md">简体中文</a></p>
  <p>
    <img src="https://img.shields.io/badge/Go-1.26.1+-00ADD8?logo=go" alt="Go 1.26.1+">
    <img src="https://img.shields.io/badge/License-Apache--2.0-blue" alt="Apache 2.0">
    <a href="https://github.com/RhinoLink-Tech/data-fabric/actions/workflows/go.yml"><img src="https://github.com/RhinoLink-Tech/data-fabric/actions/workflows/go.yml/badge.svg" alt="Go CI"></a>
  </p>
</div>

⭐ [Star Data Fabric](https://github.com/RhinoLink-Tech/data-fabric) · 🚀 [Quick Start](#quick-start) · 🤝 [Contribute](CONTRIBUTING.md)

Are you building an AI agent, RAG workflow, or AI analytics feature that needs context from several enterprise data systems? Every separate connector, credential model, SQL dialect, and result format adds integration work to your AI application.

Data Fabric gives AI applications a lightweight, self-hosted data-access layer for heterogeneous sources. Teams manage connections in one place, while applications query registered sources through a shared API and SQL interface. It connects AI workloads to existing data systems without taking over model inference or AI orchestration.

## AI Application Scenarios

- **AI agents:** Optional MCP tools support local stdio and authenticated Streamable HTTP clients to discover sources, validate read-only SQL, execute queries, and fetch result batches. See the [MCP guide](MCP_SERVER.md).
- **RAG retrieval:** Retrieve structured and semi-structured records from databases, warehouses, object storage, and supported file sources. The RAG application remains responsible for chunking, embeddings, vector storage, and ranking.
- **Text-to-SQL:** Route model-generated read-only queries through a common API and translate supported SQL syntax for the selected source.
- **AI analytics:** Give assistants a unified way to access registered sources; connect a federation engine when a use case needs cross-source SQL.

Data Fabric provides the data-access layer for AI applications. Models, prompts, embedding pipelines, and vector search remain in the AI stack.

## Why Data Fabric

- **One place to manage sources:** Register and maintain data-source connections, inspect their configuration, and diagnose connectivity through a shared gateway.
- **One query experience for applications:** Submit queries and retrieve results through a consistent API. SQL dialect adaptation follows the compatibility scope documented in the [SQL v1 contract](SQL_TRANSLATION_FEATURES.md).
- **Lightweight by default:** Access an individual source through a gateway driver without deploying a separate federation engine. Connect one only when cross-source SQL is needed.
- **Keep data where it belongs:** Data Fabric provides an access layer rather than a data-copy or ETL platform. Federated query traffic and intermediate results still travel between systems.
- **Built for mixed environments:** Use one application-facing entry point across the databases, warehouses, lakehouses, object stores, and files used by an enterprise or cluster.

## SQL Standard and Compatibility

The latest published international SQL standard is [ISO/IEC 9075:2023 (SQL:2023)](https://www.iso.org/standard/76584.html). In practice, database products implement different feature sets and add dialect-specific syntax; a standard alone does not make every query portable. See the SQL conformance notes from [PostgreSQL](https://www.postgresql.org/docs/current/features.html) and [MySQL](https://dev.mysql.com/doc/refman/8.4/en/compatibility.html).

Data Fabric's SQL v1 is a shared, read-only query profile for common application workloads, not a new SQL dialect or a claim of full SQL:2023 conformance. It covers SELECT and UNION queries, projections and basic expressions, WHERE, INNER/LEFT JOIN, GROUP BY and aggregates, HAVING, ORDER BY, and LIMIT/OFFSET. Common scalar and date/time constructs are covered by the documented examples.

For dialect translation, provide sourceDialect to identify the input SQL dialect; Data Fabric translates it to the target source dialect within the [documented compatibility matrix](SQL_TRANSLATION_FEATURES.md). If sourceDialect is omitted, SQL is sent to the target as native SQL without dialect translation. Engine-specific functions, CTEs, window functions, complex types, and time-travel syntax have no shared translation guarantee. Type coercion, NULL ordering, time zones, precision, and other execution semantics remain engine-dependent.

## How It Fits

| Component | Responsibility |
| --- | --- |
| Data Fabric | Application-facing API, source connection management, diagnostics, query routing, and result retrieval |
| Federation engine (optional) | Access multiple systems through catalogs and connectors, then plan and execute cross-source SQL |
| Source systems | Store and manage the actual data |

Data Fabric is a gateway for applications. It does not replace source databases, become a warehouse, or reimplement a distributed query engine.

~~~mermaid
flowchart LR
    App[AI / RAG / BI / Business Applications] --> Gateway[Data Fabric<br/>Unified API and Source Management]
    Gateway --> Drivers[Single-Source Drivers]
    Drivers --> Sources[Databases / Warehouses / Lakehouses / Object Storage / Files]
    Gateway --> Adapter[Query Engine Adapter]
    Adapter --> Engine[External Federation Engine]
    Engine --> Catalogs[Engine Catalogs and Connectors]
    Catalogs --> Sources
~~~

Applications can use a gateway driver for an individual source or send SQL through an external federation engine for cross-catalog queries.

## Capabilities

- **Data-source lifecycle:** Create, inspect, update, activate, deactivate, and delete data sources.
- **Driver registry:** Extend source types through a common driver interface.
- **Connection diagnostics:** Test connections, validate configuration, and inspect connection health and pool statistics.
- **Unified query API:** Submit SQL and retrieve results in batches; streaming-capable drivers can reduce peak memory use for large results.
- **SQL dialect adaptation:** Translate supported query syntax for a target dialect. See the [SQL v1 compatibility matrix](SQL_TRANSLATION_FEATURES.md).
- **Security and audit:** JWT-authenticated APIs, OIDC validation for remote MCP, owner/tenant and table/column access policies, redacted audit events, read-only SQL validation, database-side read-only sessions where supported, PostgreSQL, openGauss, MySQL, MariaDB, and TiDB server certificate verification, rate limiting, configuration validation, and health checks.
- **Prometheus monitoring:** Scrape HTTP and query metrics at `/metrics`; an admin Bearer JWT is always required. See the [observability guide](docs/observability.md).

## Data Sources

Data Fabric publishes a shared connector support catalog through the database type and compatibility APIs. The catalog separates registered implementations from end-to-end verified support, declared version ranges from exact tested versions, and protocol fixtures from live-service evidence. An empty version range means compatibility has not been established; a successful connection test alone is not an end-to-end support claim. Some registered implementations remain experimental. Consult the [Data Source Support Catalog](docs/data-source-support.md) and [Ozone support status](docs/ozone-support.md) before choosing a connector.

### Build and Runtime Footprint

The standard server build compiles the registered driver implementations and their reachable dependencies into one executable. Drivers are available without installing per-source runtime plugins; connections are opened only for data sources that are configured and used. This single-binary model simplifies deployment, while the executable and build dependency footprint grow with the registered driver set.

## Quick Start

The recommended clean install and first authorized task is the [self-hosted remote MCP quickstart](docs/mcp-deployment.md). It builds from source and walks through persistent secrets, readiness checks, the loopback-only REST management API, TLS-protected remote MCP, a read-only MySQL 8.4.13 sample source, OIDC access, and a complete query. Both MySQL services are pinned in Compose; the Data Fabric image is built from the checkout because a published container image is not available yet.

The sample profile uses MySQL metadata and separate REST and MCP processes. Its seeded data is synthetic. It demonstrates the documented path; it does not establish external adoption, production capacity, or an SLA. See the [first-adopter profile](docs/product/first-adopter-profile.md), [connector support catalog](docs/data-source-support.md), and [current operating limits](docs/roadmap.md#current-operating-limits) for the scope and open evidence.

Prerequisites for this Compose path are Docker Engine 24 or later, Docker Compose 2.24 or later, OpenSSL, Python 3 with its standard library, and an OIDC provider that can issue an access token for the configured audience and query scope. The container build includes the SQLGlot parser; no Go or Python package installation is needed on the host.

### Run the gateway from source for development

A direct source run requires Go 1.26.1 or later, Python 3, a reachable MySQL metadata database, and network access to the data sources. The default SQLGlot runtime is used for read-only SQL analysis and dialect translation. From the repository root, install the pinned parser dependency in a virtual environment:

~~~bash
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r requirements-sql-translation.txt
go mod download
cp configs/config.yaml.example configs/config.yaml
~~~

Set the metadata database fields in configs/config.yaml. Supply a stable JWT secret and credential keyring through a secret manager or environment variables; preserve the same keyring across restarts so saved source credentials remain decryptable.

~~~bash
export SECURITY_JWT_SECRET="<stable secret with at least 32 bytes>"
export DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=dev-v1
export DATA_FABRIC_CREDENTIAL_KEYS='{"dev-v1":"<base64-encoded 32-byte key>"}'
go run ./cmd/server
~~~

Generate values with openssl rand -hex 32 and openssl rand -base64 32, then store them securely instead of regenerating on restart. The management API quickstart and query request shapes are in the [API usage guide](docs/api_usage.md). For a protected remote MCP deployment, use the OIDC profile above; the REST management JWT and MCP identity-provider access token are different credentials.

## Performance Expectations

Data Fabric is a query gateway, not a query accelerator. Compared with calling an engine directly, a request adds the application-to-gateway network hop and gateway-side authentication, validation, routing, result normalization, and JSON encoding. Dialect conversion adds work when requested. The extra layer is more visible for short queries; engine execution and data transfer matter more for longer or larger queries.

In a local WSL benchmark using PostgreSQL as the simulated data source, 500 generated rows and 14 query shapes, the in-process Data Fabric query service measured P95 latencies of 0.682–2.159 ms. The P95 percentile-value gap versus direct PostgreSQL was 0.446–0.694 ms; the P50 gap was 0.246–0.450 ms. A separate 5,000-row scale run, including joins, aggregation, a wide projection, and an unindexed sort, is documented with CPU, allocation, and raw sample data. These local serial measurements exclude HTTP, authentication middleware, concurrent traffic, and the client-to-gateway network hop; they are not production guarantees. The custom suite is not a TPC result. See the [reproducible benchmark report](docs/performance/postgres-gateway-benchmark.md) for the full results and limits.

Deployment resource measurements are documented separately: a local WSL2 run recorded a 0.488 s median restart-to-health time and 83.6 MiB median idle RSS. The report includes the bounded 50,000-row stream workload, artifact sizes, raw samples, and reproduction steps. These are single-machine results, not production guarantees. See the [deployment footprint report](docs/performance/deployment-footprint.md).
## API Overview

| Method | Path | Purpose | Authentication |
| --- | --- | --- | --- |
| GET | /health | Service health check | None |
| GET | /api/v1/database/types | List source types recognized by the service | None |
| GET | /api/v1/database/compatibility | Inspect database compatibility information | None |
| GET / POST | /api/v1/datasources | List or create data sources | Bearer when authentication is enabled; scoped by owner/tenant |
| GET / PUT / DELETE | /api/v1/datasources/:id | Inspect, update, or delete a data source | Same; non-admins get a uniform 403 for missing and foreign sources |
| POST | /api/v1/datasources/:id/activate | Activate a data source | Same |
| POST | /api/v1/datasources/:id/deactivate | Deactivate a data source | Same |
| POST | /api/v1/database/test-connection | Test a data-source connection | Bearer when authentication is enabled |
| POST | /api/v1/database/validate-config | Validate data-source configuration | Bearer when authentication is enabled |
| GET | /api/v1/database/health | Probe the health of every data source | Admin role when authentication is enabled |
| GET | /api/v1/database/connections/stats | Connection pool statistics | Admin role when authentication is enabled |
| POST | /api/v1/fetch | Run a query and retrieve its first result batch | Bearer (always); requires read access to the data source |
| GET | /api/v1/fetch/:query_id/:slug/:token | Retrieve a subsequent result batch | Bearer (always); only the session's creator may continue |

See the [API usage guide](docs/api_usage.md) for request fields, response structures, and authentication behavior.
The embedded [data-source management UI](docs/data-source-management-ui.md) is available at /ui/.

## Documentation

- [API Usage Guide](docs/api_usage.md)
- [Data-source Management UI](docs/data-source-management-ui.md)
- [Data Fabric Agent Skill](docs/agent-skill.md)
- [DeepSeek Function-Calling Example](examples/deepseek-agent/README.md)
- [Architecture Guide](docs/architecture.md)
- [Roadmap](docs/roadmap.md)
- [Adoption and Product Value Delivery Plan](docs/plans/gateway-delivery.md)
- [First-adopter profile and supported preview hypothesis](docs/product/first-adopter-profile.md) (evaluation remains pending)
- [Open-source adoption and product value program](docs/plans/gateway-delivery.md#productization-program) (planned delivery and evaluation)
- [Configuration Guide](docs/configuration.md)
- [Development Guide](docs/development.md)
- [Contributing Guide](CONTRIBUTING.md)
- [Security Policy](SECURITY.md)
- [Support](SUPPORT.md)

## Cross-Source Query Engine Integrations

When cross-source SQL is needed, Data Fabric can connect to compatible federation engines through a shared adapter. Trino, Presto, and OpenLooKeng adapters are implemented and can be registered as data sources. The generated support catalog records versioned driver-contract evidence and currently keeps these adapters at implementation-present. Separately, the opt-in [Trino 484 integration profile](docs/validation/trino-integration.md) verifies the listed read-only query, timeout, coordinator-recovery, and engine-error paths through public REST and MCP. This bounded profile does not establish compatibility for other Trino versions, authentication modes, connectors, SQL features, worker topologies, or production workloads.

See the [API usage guide](docs/api_usage.md) for connection setup and the [SQL v1 contract](SQL_TRANSLATION_FEATURES.md) for the supported SQL compatibility scope.

## Contribute

Issues, pull requests, and documentation improvements are welcome. See the [contribution guide](CONTRIBUTING.md) to get involved.

## License

Apache License 2.0. See [LICENSE](LICENSE).
