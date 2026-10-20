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
- **Gateway controls:** JWT authentication, rate limiting, configuration validation, and health checks.
- **Prometheus monitoring:** Scrape HTTP and query metrics at `/metrics`; an admin Bearer JWT is always required. See the [observability guide](docs/observability.md).

## Data Sources

The repository defines representative source types across these categories. Available operations and authentication options depend on the source and its driver.

| Category | Examples |
| --- | --- |
| Relational databases | MySQL, MariaDB, PostgreSQL, Oracle, SQL Server |
| Domestic and compatible databases | OceanBase, TiDB, TDSQL, GaussDB, DaMeng, KingbaseES, GBase, Oscar, OpenGauss |
| Analytical databases | ClickHouse, Doris, StarRocks, Druid, Hologres |
| Cloud warehouses and big-data platforms | Snowflake, Databricks, Redshift, BigQuery, Hive, MaxCompute |
| Lakehouse table formats | Apache Iceberg, Delta Lake, Apache Hudi |
| Object storage and file systems | S3, MinIO, Alibaba OSS, Tencent COS, Azure Blob, HDFS |
| File formats | Parquet, ORC, Avro, CSV, JSON, XML, text |

Apache Ozone is not currently advertised as a supported source because its object operations are incomplete. The registry rejects Ozone source configuration until those operations are implemented and verified. See the [Ozone support status](docs/ozone-support.md).

### Build and Runtime Footprint

The standard server build compiles the registered driver implementations and their reachable dependencies into one executable. Drivers are available without installing per-source runtime plugins; connections are opened only for data sources that are configured and used. This single-binary model simplifies deployment, while the executable and build dependency footprint grow with the registered driver set.

## Quick Start

### Requirements

- Go 1.26.1 or newer for running from source.
- Docker Engine for building or running the container.
- A configured and reachable metadata database.
- Network access to the data sources you plan to register.
- A JWT secret and a credential-encryption keyring supplied through environment variables.

### Run Locally

~~~bash
git clone https://github.com/RhinoLink-Tech/data-fabric.git
cd data-fabric
cp configs/config.yaml.example configs/config.yaml
go mod download
~~~

Configure the metadata database in configs/config.yaml. Generate secrets once and store them securely; reuse the same values across restarts.

~~~bash
export JWT_SECRET="<stable secret with at least 32 bytes>"
export DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=dev-v1
export DATA_FABRIC_CREDENTIAL_KEYS='{"dev-v1":"<base64-encoded 32-byte key>"}'
go run ./cmd/server
~~~

Generate the JWT secret with openssl rand -hex 32 and the credential key with openssl rand -base64 32. Store both securely and reuse them across restarts. The service health-check endpoint is /health. See the [configuration guide](docs/configuration.md) for deployment settings, secret handling, and key rotation.

### Build and Run with Docker

The image uses Go 1.26.1 to match `go.mod`, listens on port 8080 by default, and runs as a non-root user. Create the MySQL metadata database and account before starting the container.

~~~bash
docker build -t data-fabric:local .
docker run --rm --name data-fabric \
  --add-host=host.docker.internal:host-gateway \
  -p 8080:8080 \
  -e DATABASE_HOST=host.docker.internal \
  -e DATABASE_PORT=3306 \
  -e DATABASE_DATABASE=data_fabric \
  -e DATABASE_USERNAME=data_fabric \
  -e DATABASE_PASSWORD="<metadata database password>" \
  -e SECURITY_JWT_SECRET="<stable secret with at least 32 bytes>" \
  -e DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=dev-v1 \
  -e DATA_FABRIC_CREDENTIAL_KEYS='{"dev-v1":"<base64-encoded 32-byte key>"}' \
  data-fabric:local
~~~

The container has no live configuration file; it reads environment variables and defaults to port 8080. If you override SERVER_PORT or server.port, update the container port and port mapping to match. Inject real secrets through your deployment platform and reuse the same credential keyring across restarts.

## Performance Expectations

Data Fabric is a query gateway, not a query accelerator. Compared with calling an engine directly, a request passes through an additional application-to-gateway network hop and gateway-side authentication, validation, routing, result normalization, and JSON encoding. Dialect conversion adds work when requested. The extra layer is proportionally more visible for short queries; engine execution and data transfer matter more for longer or larger queries.

Using a local PostgreSQL instance as the simulated data source, we benchmarked 500 synthetic rows across 14 query shapes (1,000 serial samples per path, 20 warmups); the in-process Data Fabric query service measured P95 latencies from 0.466 to 1.929 ms. The differences between the direct PostgreSQL and gateway P95 values were 0.281 to 0.436 ms; the P50 value gaps were 0.192 to 0.311 ms. This run excludes HTTP, authentication middleware, concurrent traffic, and the client-to-gateway network hop. These are local measurements, not latency limits or production guarantees. The report includes every tested case, a 5,000-row scale run, method details, and standard benchmark references; this custom suite is not a TPC result. See the [reproducible benchmark report](docs/performance/postgres-gateway-benchmark.md).

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

## Documentation

- [API Usage Guide](docs/api_usage.md)
- [Architecture Guide](docs/architecture.md)
- [Configuration Guide](docs/configuration.md)
- [Development Guide](docs/development.md)
- [Contributing Guide](CONTRIBUTING.md)
- [Security Policy](SECURITY.md)
- [Support](SUPPORT.md)

## Cross-Source Query Engine Integrations

When cross-source SQL is needed, Data Fabric can connect to compatible federation engines through a shared adapter. Trino, Presto, and OpenLooKeng can be registered as data sources; their coordinators use configured catalogs and connectors to plan and execute cross-catalog queries. Applications continue to submit SQL through the Data Fabric query API.

See the [API usage guide](docs/api_usage.md) for connection setup and the [SQL v1 contract](SQL_TRANSLATION_FEATURES.md) for the supported SQL compatibility scope.

## Contribute

Issues, pull requests, and documentation improvements are welcome. See the [contribution guide](CONTRIBUTING.md) to get involved.

## License

Apache License 2.0. See [LICENSE](LICENSE).
