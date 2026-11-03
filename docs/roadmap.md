# Data Fabric Roadmap

**Last reviewed: 2026-11-02**

## Product direction

Data Fabric is a lightweight, self-hosted access gateway for heterogeneous enterprise data. It centralizes connection management and gives applications a bounded, read-only SQL/API surface. Single-source requests use gateway drivers; cross-source planning and execution remain with an optional external federation engine. The project does not aim to become a warehouse, ETL platform, or general-purpose AI workbench.

## Released baseline

[v2.0.0](https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v2.0.0) was published on 2026-10-21. The post-release mainline has since added identity, authorization, audit, connector, and query-lifecycle improvements. This roadmap reflects the current mainline; the [support catalog](data-source-support.md) and [contract report](driver-contract-tests.md) are the exact source for connector evidence.

### Delivered capabilities

- Remote MCP can validate enterprise OIDC tokens and carry the verified caller into shared authorization.
- Data-source, table, and column access policies apply to query and metadata paths, including REST and MCP.
- Structured access audit events persist in the metadata database, with admin-only review, retention, and redaction.
- Read-only SQL validation is backed by database-side read-only sessions where supported.
- Query execution, pagination, cancellation, and connector support are exposed through documented contracts.
- Query admission bounds concurrent work, applies per-tenant request-rate limits, and exposes rejection metrics; limiter state is bounded in memory.
- A runtime support catalog distinguishes registered implementations, end-to-end verified versions, experimental adapters, and unsupported boundaries.
- Versioned SQL driver contracts cover PostgreSQL 15.19, 16.15, 17.11, and 18.6, plus MySQL 8.0.46 and 8.4.13. TLS evidence is version-specific and remains narrower than query-driver evidence.

### Current support limits

- The support catalog lists **62 registered types**: 10 are classified as `end_to_end_verified`, 20 as `implementation_present`, and 32 as `experimental`. Some experimental adapters have bounded protocol-fixture evidence; that does not establish a live provider/version contract. Use the [catalog](data-source-support.md) for the exact operations, authentication notes, tested versions, and limits.
- Trino, Presto, and OpenLooKeng adapters share the query-engine protocol family. The cross-catalog gateway path is covered with a simulated coordinator; live coordinator compatibility remains deferred in [#179](https://github.com/RhinoLink-Tech/data-fabric/issues/179).
- Query continuation sessions remain process-local. A multi-batch query requires one gateway replica or query-aware routing that pins all continuation requests to the process that created the session. The limitation and token behavior are documented; shared session storage is not implemented.
- SQL v1 is a documented, read-only compatibility subset. Translation success does not guarantee equivalent type, time-zone, NULL-ordering, or function semantics on every engine.

## Prioritized evolution

### Completed: establish and reduce deployment footprint

[#291](https://github.com/RhinoLink-Tech/data-fabric/issues/291) established the reproducible deployment baseline, and [#293](https://github.com/RhinoLink-Tech/data-fabric/issues/293) applies stripped Go linker output to the release Docker build. The measured release binary decreased from 164.2 MiB to 119.5 MiB, and the uncompressed image from 311.0 MiB to 236.1 MiB. Restart-to-health remained about 0.49 seconds median; the 50,000-row bounded stream measured 95.09 MiB median process high-water RSS. The report includes before/after samples and limitations. These are local measurements, not production guarantees.

### Completed: verify PostgreSQL server identity over TLS

[#296](https://github.com/RhinoLink-Tech/data-fabric/issues/296) adds explicit PostgreSQL `sslMode` and `sslRootCert` settings. The pinned PostgreSQL 17.11 contract validates `verify-full` with a trusted CA and rejects an untrusted CA or hostname mismatch. Existing `ssl: true` and `ssl: false` behavior remains compatible. TLS evidence is connector-specific; this does not claim certificate verification for other connectors.

### Completed: verify TiDB server identity over TLS

[#298](https://github.com/RhinoLink-Tech/data-fabric/issues/298) honors the TiDB `ssl` setting and supports a mounted PEM CA for private trust roots. The pinned TiDB v8.5.8 TLS contract verifies the server certificate and hostname, rejects an untrusted CA and hostname mismatch, and confirms `ssl: false` retains plaintext behavior. Other TiDB versions and TLS behavior for other connectors remain outside this evidence.

### Completed: verify MySQL server identity with private CA roots

[#300](https://github.com/RhinoLink-Tech/data-fabric/issues/300) adds `sslRootCert` support to the MySQL connector while retaining system trust roots for `ssl: true` and plaintext behavior for `ssl: false`. The pinned MySQL 8.0.46 contract validates the certificate and hostname with a trusted private CA and rejects an untrusted CA or hostname mismatch. Other MySQL versions and MariaDB TLS behavior remain outside this evidence.

### Completed: verify MariaDB TLS using the shared MySQL protocol driver

[#302](https://github.com/RhinoLink-Tech/data-fabric/issues/302) verifies the shared private-CA TLS configuration against MariaDB 11.8.9. The pinned contract validates the server certificate and hostname, rejects an untrusted CA or hostname mismatch, and confirms `ssl: false` remains plaintext. [#326](https://github.com/RhinoLink-Tech/data-fabric/issues/326) adds driver-contract coverage for the MariaDB 10.11 LTS baseline. [#330](https://github.com/RhinoLink-Tech/data-fabric/issues/330) adds TLS identity evidence for MariaDB 10.11.19, alongside the 11.8.9 TLS baseline. [#332](https://github.com/RhinoLink-Tech/data-fabric/issues/332) adds driver and TLS contract coverage for MariaDB 11.4.13.

### Completed: verify openGauss server identity over TLS

[#304](https://github.com/RhinoLink-Tech/data-fabric/issues/304) makes the openGauss connector honor sslMode and sslRootCert. The pinned openGauss 6.0.0 contract verifies a private CA and hostname, rejects an untrusted CA and hostname mismatch, and preserves legacy ssl: true/false behavior. Other openGauss versions remain outside this evidence.

### Completed: verify SQL Server server identity over TLS

[#306](https://github.com/RhinoLink-Tech/data-fabric/issues/306) adds private-CA verification for the SQL Server connector. The pinned SQL Server 16.0.4305.5 contract accepts a trusted CA and matching hostname, rejects an untrusted CA and hostname mismatch, and confirms the explicit certificate-bypass compatibility setting and plaintext behavior when TLS is disabled. Other SQL Server versions remain outside this evidence.

### Completed: verify ClickHouse server identity over TLS

[#308](https://github.com/RhinoLink-Tech/data-fabric/issues/308) makes the ClickHouse native-protocol connector honor `ssl` and `sslRootCert`. The pinned ClickHouse 26.8.20.9 contract accepts a trusted private CA and matching host, rejects an untrusted CA and hostname mismatch, confirms system roots reject the private test CA when no custom CA is configured, and keeps certificate verification enabled. Plaintext connections remain supported when TLS is disabled. Other ClickHouse versions remain outside this evidence.

### Completed: verify PostgreSQL driver versions 15 through 18

[#320](https://github.com/RhinoLink-Tech/data-fabric/issues/320) adds repeatable driver contracts against PostgreSQL 15.19, 16.15, 17.11, and 18.6. This verifies the shared query contract across those exact releases; TLS certificate verification remains separately covered against PostgreSQL 17.11 only.

### Completed: verify MySQL 8.4 LTS driver compatibility

[#322](https://github.com/RhinoLink-Tech/data-fabric/issues/322) adds the MySQL 8.4.13 driver contract alongside the existing MySQL 8.0.46 contract. Both pinned versions verify the same connection, authentication, query, pagination, read-only session, cancellation, and error-handling behaviors.

### Completed: verify MySQL 8.4 LTS TLS identity

[#325](https://github.com/RhinoLink-Tech/data-fabric/issues/325) adds private-CA and hostname-verification evidence for MySQL 8.4.13. Both MySQL 8.0.46 and 8.4.13 now reject untrusted certificates and hostname mismatches; other releases and MySQL-compatible products remain outside this evidence.

### 1. Raise connector confidence from real demand

The initial versioned connector contract baseline in [#35](https://github.com/RhinoLink-Tech/data-fabric/issues/35) is complete. The catalog remains the source of truth: new verified claims require a pinned version and a passing contract for the operations being claimed. Select additional connectors from actual user demand rather than attempting to certify every registered type at once.

### 2. Validate the federation adapter against a real engine when available

[#179](https://github.com/RhinoLink-Tech/data-fabric/issues/179) is intentionally deferred until a real coordinator and deterministic catalogs are available. Its simulated tests verify the gateway request/result path only; they do not establish Trino, Presto, or OpenLooKeng release compatibility.

### 3. Keep operator UI optional

The source-management UI in [#175](https://github.com/RhinoLink-Tech/data-fabric/issues/175) is a convenience feature. The API remains the core management surface; start UI work only when self-hosted operator feedback justifies its maintenance cost.

### 4. Extend governance from concrete policy needs

OIDC identity, table/column authorization, and durable audit are delivered in [#217](https://github.com/RhinoLink-Tech/data-fabric/issues/217), [#218](https://github.com/RhinoLink-Tech/data-fabric/issues/218), [#219](https://github.com/RhinoLink-Tech/data-fabric/issues/219), and [#220](https://github.com/RhinoLink-Tech/data-fabric/issues/220). Further data classification, masking, or approval workflows should follow specific enterprise policies and documented use cases.

## Backlog and sequencing

GitHub issues remain the source of truth for task status. The remaining broad Phase 2–5 roadmap issues include exploratory ideas and are not an implementation commitment until they are re-triaged into bounded, testable work. Keep active sequencing focused on connector evidence, operational fit, and secure AI access.

## Release records

[The v2.0.0 candidate review](releases/v2.0.0-candidate.md) is a historical snapshot of release gates and evidence at publication time. Use the current support catalog and this roadmap for post-release status.
