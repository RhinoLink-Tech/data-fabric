# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Fixed
- Validate required startup settings, ports, query execution mode, and HTTP server limits before database initialization; reject malformed MCP listener ports and document fail-closed schema migration behavior (#31).

### Security
- Query API errors now use stable public messages and keep submitted SQL, query-history SQL, and backend error details out of responses; detailed failures are recorded in structured server logs (#29).

### Added
- Root-level contribution, conduct, security, and support guides, plus structured issue and pull request templates.
- A maintainer release checklist and CI status badges across the README editions.

### Changed
- Map all service configuration paths to uppercase underscore-separated environment variables, retaining `DATABASE_NAME` and `JWT_SECRET` compatibility aliases.
- Replaced expired calendar phases with a stage-based roadmap and observable completion gates.
- Documented current release automation and the container packaging limitation.

### Fixed
- Close expired streaming SQL rows before evicting their sessions; bind cleanup and shutdown to the HTTP/MCP service lifecycle and log close failures.
- Preserve application error codes and HTTP statuses when errors are wrapped with additional context.
- Honour the HTTP status set with `ErrorBuilder.WithStatus`: `GetErrorStatus` now returns it instead of always deriving the status from the error code.

### Security

- Data sources are now access-controlled per caller (#9): records carry `ownerId`/`tenantId` stamped from the JWT, queries and management operations enforce owner/tenant/admin permissions, and the JWT gains an optional `tenantId` claim.
  - **Behavior change**: non-admin callers receive a uniform `403 DATASOURCE_ACCESS_DENIED` for missing *and* foreign data sources on every management endpoint and `/fetch` (admins keep `404`), so data source existence can no longer be probed.
  - **Behavior change**: pagination/streaming continuation is bound to the session's creator; another user's token gets `404 QUERY_SESSION_UNAVAILABLE`.
  - **Behavior change**: Bearer scheme matching is case-insensitive; JWT identity uses the standard `sub` claim (userId-only tokens remain supported), and only HS256 tokens are accepted.
  - **Behavior change**: data source names are unique per tenant instead of globally; the old global unique index is dropped at startup. Legacy rows without ownership metadata are accessible only to administrators; regular users cannot claim them.
  - `/api/v1/database/health` and `/api/v1/database/connections/stats` require the `admin` role when authentication is enabled.
  - Fixed `RequireRole`/`RequireAnyRole`: the composed role check previously ran *after* the route handler (the inner `RequireAuth` advanced the chain), so any valid token reached the handler and the 403 body was appended to the handler's response. Role checks now run before the handler.
  - MCP retains administrator-equivalent access for trusted local stdio clients. Issue #185 adds remote Streamable HTTP with per-request Bearer JWT verification, owner/tenant authorization, and HTTPS deployment requirements; deployments issue short-lived JWTs from a trusted identity source. Anyone holding the signing secret can assert user, tenant, and role claims.
- Data-source sessions are now opened read-only where the engine supports it (#20), adding database-side defense-in-depth on top of application-layer SELECT validation.
  - MySQL-protocol engines (mysql, mariadb, tidb, oceanbase_mysql, tdsql, gbase_8s/8t) run `SET SESSION TRANSACTION READ ONLY` on every pooled connection; PostgreSQL-protocol engines (postgresql, gaussdb_*, kingbasees, opengauss, oscar) receive `default_transaction_read_only=on`.
  - **Behavior change**: engines that reject the init statement or parameter fail connection creation instead of silently continuing (fail-closed). If an engine version rejects it, opt out per data source with `additionalProps: {enforce_read_only: false}`.
  - `security.enforce_read_only` (default `true`) cannot be disabled in `release` mode. Doris, StarRocks, Hologres, and Redshift are excluded pending live verification; they keep SQL validation only — see `docs/readonly-access.md` for the matrix and a least-privilege account cookbook.
  - `POST /api/v1/database/test-connection` now reports `readOnlyEnforced` and warns when the account holds write or administrative privileges; `validate-config` warns for types without database-side enforcement.
  - The SQL validator now also rejects filesystem/administrative functions reachable from a SELECT (`pg_read_file`, `lo_import`, `dblink`, `pg_sleep`, ...).
- `configs/config.yaml` is no longer tracked by git: it contained real credentials in the repository history. Copy `configs/config.yaml.example` (placeholders only) for local setup, and keep real secrets out of the file.
  - Back up your local `configs/config.yaml` before pulling this change (`cp configs/config.yaml ~/config.yaml.bak`); git will otherwise delete or conflict with it.
  - `DATABASE_USERNAME`, `DATABASE_PASSWORD`, and `JWT_SECRET` now actually override the config file, matching the documentation.
  - The published image no longer contains a config file: the container now defaults to port 8080 with authentication enabled. Inject `DATABASE_*` and `JWT_SECRET` explicitly when deploying.
- The server now fails closed on insecure authentication settings: `release` mode refuses to start with authentication disabled, and enabled authentication requires a `jwt_secret` that is set, not a known placeholder, and at least 32 bytes long. The built-in default JWT secret is removed.
  - **Breaking**: any deployment relying on the removed default secret — in any mode — must set `JWT_SECRET` (or `jwt_secret` in the config) before upgrading; startup then fails with instructions instead of accepting a forgeable secret.
- `/api/v1/fetch` (POST and the subsequent-batch GET) now always requires a valid Bearer token, regardless of `security.enable_auth`. The endpoints were previously mounted outside the auth middleware group and accepted anonymous requests even with authentication enabled.
  - **Breaking**: fetch clients that send no Bearer token now receive 401. `security.enable_auth` now only controls the data-source management API.
  - **Breaking**: `security.jwt_secret` is now validated whenever the HTTP server runs — previously only with `security.enable_auth: true` — because the fetch API always authenticates. Deployments without a valid secret, including `enable_auth: false`, refuse to start instead of authenticating with a forgeable one.
  - Route-level regression tests cover fetch exposure under both auth configurations; `test_fetch_api.sh` reads its Bearer token from `FETCH_TOKEN`.

### Streaming Query Updates

#### Changed
- `ExecuteQuery` now prefers establishing a backend streaming cursor when supported. Returns the first batch of rows and a `nextUri` to continue the stream.
- Added `/fetch` POST and `/fetch/{query_id}/{slug}/{token}` GET endpoints for explicit batch/streaming retrieval.
- `QueryResponse` and `FetchQueryResponse` now include `nextUri` to indicate continuation URIs for streaming sessions.
- Added server-side `QuerySession` management to hold streaming cursors (`Rows`) and cancellation handles.
- OpenAPI spec updated: `specs/001-phase1-enhanced-single-source/contracts/openapi.yaml` now documents `/fetch` endpoints and `FetchQueryRequest`/`FetchQueryResponse` schemas.
- README updated with streaming usage examples and new `query.prefer_streaming` configuration documentation.
- `FETCH_API_USAGE.md` updated: example requests now use `batch_size` (snake_case) to match API model.
- `test_fetch_api.sh` updated to use `batch_size` in JSON payloads.

#### Added
- `CHANGELOG.md` (this file).

#### Notes
- The streaming behavior is configurable via `query.prefer_streaming` (default `true`). When `prefer_streaming` is `true` and the backend does not support streaming, queries will fail; set it to `false` to allow automatic fallback to LIMIT/OFFSET continuation.
- Sessions have a default TTL and are subject to server-side cleanup; clients should exhaust or explicitly complete streams when possible.

## [v1.0.0] - 2025-12-23 - Initial Release

### Added
- **Core Framework**: Initial multi-database proxy gateway implementation
- **Basic Database Support**: MySQL, MariaDB, PostgreSQL, Oracle support
- **Query Routing**: UUID-based data source identification and routing
- **Security Features**: SQL injection prevention, JWT authentication, rate limiting
- **API Endpoints**: Basic query and data source management APIs
- **Documentation**: Initial README and project structure

## [v1.1.0] - 2025-12-24 - Phase 1: Enhanced Single-Source Capabilities

### Added
- **Data Lake & Warehouse Support**: Added Apache Iceberg, Delta Lake, Apache Hudi table formats
- **Cloud Data Warehouses**: Added Snowflake, Databricks, Redshift, BigQuery connectors
- **OLAP Engines**: Added ClickHouse, Doris, StarRocks, Druid drivers
- **Object Storage Integration**: Added S3, MinIO, OSS, COS, Azure Blob clients
- **Distributed Storage**: Added HDFS, Apache Ozone support
- **File Formats**: Added Parquet, ORC, Avro, CSV, JSON file readers
- **Domestic Database Support**: Added OceanBase, TiDB, TDSQL, GaussDB drivers
- **Chinese Databases**: Added DaMeng, KingbaseES, GBase, Oscar, OpenGauss support
- **Streaming Query Capability**: Added streaming query functionality with configurable fallback

## [2026-01-05 to 2026-01-12] - Core Framework Enhancements

### Added
- **Additional Data Sources**: Added support for Hive, SQL Server, Hologres, MongoDB, MaxCompute
- **OLAP Framework Support**: Enhanced OLAP engine integration
- **Query Strategies**: Implemented streaming query and pagination query strategies with configuration options

### Fixed
- **Code Framework**: Resolved multiple framework issues and compilation failures
- **Oracle Pagination**: Adjusted Oracle pagination SQL
- **URL Handling**: Improved connectUrl acquisition and connection string handling
- **Streaming Query**: Fixed streaming query exceptions and token refresh issues

## [2026-01-16] - Documentation Structure Optimization

### Changed
- Updated license information in README to reflect actual Apache 2.0 license instead of incorrect MIT license
- Modified license badge in README to show correct Apache 2.0 license

### Added
- Created comprehensive documentation structure with dedicated files:
  - `docs/api_usage.md` - API usage examples and guidelines
  - `docs/architecture.md` - System architecture overview
  - `docs/configuration.md` - Configuration options and parameters
  - `docs/contributing.md` - Contribution guidelines
  - `docs/development.md` - Development practices and driver framework details
  - `docs/roadmap.md` - Project roadmap and phase information
  - `docs/refactoring_guide.md` - Refactoring recommendations and implementation steps

### Removed
- Deleted redundant documentation files:
  - `CLAUDE.md` - Autogenerated template with no substantial content
  - `DRIVER_FRAMEWORK.md` - Content migrated to `docs/development.md`
  - `FETCH_API_USAGE.md` - Content migrated to `docs/api_usage.md`
  - `PROJECT_STRUCTURE_ANALYSIS.md` - Content migrated to `docs/refactoring_guide.md`
