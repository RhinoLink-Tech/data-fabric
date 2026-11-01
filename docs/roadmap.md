# Data Fabric Roadmap

**Last reviewed: 2026-11-01**

## Product direction

Data Fabric is a lightweight, self-hosted access gateway for heterogeneous enterprise data. It centralizes connection management and gives applications a bounded, read-only SQL/API surface. Single-source requests use gateway drivers; cross-source planning and execution remain with an optional external federation engine. The project does not aim to become a warehouse, ETL platform, or general-purpose AI workbench.

## Released baseline

[v2.0.0](https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v2.0.0) was published on 2026-10-21. The post-release mainline has since added identity, authorization, audit, connector, and query-lifecycle improvements. The latest reviewed mainline commit is [403fd87](https://github.com/RhinoLink-Tech/data-fabric/commit/403fd872) and its [Go CI run passed](https://github.com/RhinoLink-Tech/data-fabric/actions/runs/41280666701).

### Delivered capabilities

- Remote MCP can validate enterprise OIDC tokens and carry the verified caller into shared authorization.
- Data-source, table, and column access policies apply to query and metadata paths, including REST and MCP.
- Structured access audit events persist in the metadata database, with admin-only review, retention, and redaction.
- Read-only SQL validation is backed by database-side read-only sessions where supported.
- Query execution, pagination, cancellation, and connector support are exposed through documented contracts.
- A runtime support catalog distinguishes registered implementations, end-to-end verified versions, experimental adapters, and unsupported boundaries.

### Current support limits

- The support catalog lists **62 registered types**: 10 are classified as `end_to_end_verified`, 20 as `implementation_present`, and 32 as `experimental`. Some experimental adapters have bounded protocol-fixture evidence; that does not establish a live provider/version contract. Use the [catalog](data-source-support.md) for the exact operations, authentication notes, tested versions, and limits.
- Trino, Presto, and OpenLooKeng adapters share the query-engine protocol family. The cross-catalog gateway path is covered with a simulated coordinator; live coordinator compatibility remains deferred in [#179](https://github.com/RhinoLink-Tech/data-fabric/issues/179).
- Query continuation sessions remain process-local. A multi-batch query requires one gateway replica or query-aware routing that pins all continuation requests to the process that created the session. The limitation and token behavior are documented; shared session storage is not implemented.
- SQL v1 is a documented, read-only compatibility subset. Translation success does not guarantee equivalent type, time-zone, NULL-ordering, or function semantics on every engine.

## Prioritized evolution

### 1. Measure the lightweight deployment footprint

[P2 #291](https://github.com/RhinoLink-Tech/data-fabric/issues/291) adds a reproducible baseline for binary and image size, startup readiness, idle memory, and memory under bounded query workloads. The existing PostgreSQL report measures query latency; it does not establish deployment footprint. Decide on packaging changes only after the measurements show a concrete need.

### 2. Raise connector confidence from real demand

The initial versioned connector contract baseline in [#35](https://github.com/RhinoLink-Tech/data-fabric/issues/35) is complete. The catalog remains the source of truth: new verified claims require a pinned version and a passing contract for the operations being claimed. Select additional connectors from actual user demand rather than attempting to certify every registered type at once.

### 3. Validate the federation adapter against a real engine when available

[#179](https://github.com/RhinoLink-Tech/data-fabric/issues/179) is intentionally deferred until a real coordinator and deterministic catalogs are available. Its simulated tests verify the gateway request/result path only; they do not establish Trino, Presto, or OpenLooKeng release compatibility.

### 4. Keep operator UI optional

The source-management UI in [#175](https://github.com/RhinoLink-Tech/data-fabric/issues/175) is a convenience feature. The API remains the core management surface; start UI work only when self-hosted operator feedback justifies its maintenance cost.

### 5. Extend governance from concrete policy needs

OIDC identity, table/column authorization, and durable audit are delivered in [#217](https://github.com/RhinoLink-Tech/data-fabric/issues/217), [#218](https://github.com/RhinoLink-Tech/data-fabric/issues/218), [#219](https://github.com/RhinoLink-Tech/data-fabric/issues/219), and [#220](https://github.com/RhinoLink-Tech/data-fabric/issues/220). Further data classification, masking, or approval workflows should follow specific enterprise policies and documented use cases.

## Backlog and sequencing

GitHub issues remain the source of truth for task status. The remaining broad Phase 2–5 roadmap issues include exploratory ideas and are not an implementation commitment until they are re-triaged into bounded, testable work. Keep active sequencing focused on connector evidence, operational fit, and secure AI access.

## Release records

[The v2.0.0 candidate review](releases/v2.0.0-candidate.md) is a historical snapshot of release gates and evidence at publication time. Use the current support catalog and this roadmap for post-release status.
