# Data Fabric Roadmap

**Last reviewed: 2026-11-01**

## Product direction

Data Fabric is a lightweight, self-hosted access gateway for heterogeneous enterprise data. It centralizes connection management and gives applications a bounded, read-only SQL/API surface. Single-source requests use gateway drivers; cross-source planning and execution remain with an optional external federation engine. The project does not aim to become a warehouse, ETL platform, or general-purpose AI workbench.

## Released baseline

[v2.0.0](https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v2.0.0) was published on 2026-10-21. The post-release mainline has since added identity, authorization, audit, connector, and query-lifecycle improvements. This roadmap update was prepared against mainline commit [11a03ce](https://github.com/RhinoLink-Tech/data-fabric/commit/11a03cec5b2f932e6ae4ffaac11f47f5742915ff).

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

### Completed: establish and reduce deployment footprint

[#291](https://github.com/RhinoLink-Tech/data-fabric/issues/291) established the reproducible deployment baseline, and [#293](https://github.com/RhinoLink-Tech/data-fabric/issues/293) applies stripped Go linker output to the release Docker build. The measured release binary decreased from 164.2 MiB to 119.5 MiB, and the uncompressed image from 311.0 MiB to 236.1 MiB. Restart-to-health remained about 0.49 seconds median; the 50,000-row bounded stream measured 95.09 MiB median process high-water RSS. The report includes before/after samples and limitations. These are local measurements, not production guarantees.

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
