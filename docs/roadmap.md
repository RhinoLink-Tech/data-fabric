# Data Fabric Roadmap

**Last reviewed: 2026-11-05**

## Product direction

Data Fabric is a lightweight, self-hosted data access gateway for AI applications. Teams register and govern sources once and reuse a documented, read-only access contract across applications. Single-source queries use gateway drivers. An optional external federation engine owns cross-source planning and execution.

The next milestones establish repeatable deployment, public API correctness, and reliable shared operation. Real-team pilots are deferred until the maintainer schedules them. Engineering reference results and customer value evidence are tracked separately.

## Delivered foundation

- Source lifecycle, connection diagnostics, credential handling, shared query services, and REST/MCP interfaces.
- Remote MCP enterprise OIDC verification; the management HTTP API retains its documented JWT authentication contract.
- Source ownership and tenant checks, optional table/column policies, and persisted redacted access audit events (#217–#220).
- Parameter binding, schema discovery, read-only validation, query continuation, cancellation, and bounded admission controls.
- A generated connector support catalog and repeatable, version-pinned driver contracts.
- Existing Compose and remote MCP deployment guides (#180 and #186), plus deployment-footprint and upgrade-rehearsal evidence.

The [support catalog](data-source-support.md) and [driver contract report](driver-contract-tests.md) define exact connector evidence. Registered code, restricted experimental adapters, and verified operations remain distinct. Connector counts are not duplicated in this roadmap.

## Current operating limits

- Query cursors and continuation sessions are process-local. Restart loses active sessions; ordinary load balancing does not provide query ownership routing. #365 defines the next design.
- Admission limits are per process. A multi-replica continuation increment does not establish globally precise tenant quotas.
- Table/column policies are loaded at startup. Versioned updates and revocation propagation require an explicit contract in #99.
- Persisted audit is available, but the current failure policy does not guarantee durable evidence for every access when audit storage fails.
- REST and MCP run in separate service processes in the reference deployment. Shared source metadata does not make live query cursors interchangeable between them.
- SQL translation covers the documented compatibility subset and does not guarantee identical execution semantics across engines.
- Real Trino coordinator compatibility remains explicitly deferred in #179. Simulated coordinator coverage is not real-engine compatibility evidence.

## M0 Product scope and engineering acceptance

Tracking: [#402](https://github.com/RhinoLink-Tech/data-fabric/issues/402). Detailed tasks and the reference acceptance matrix are in the [gateway delivery plan](plans/gateway-delivery.md).

Reconcile the historical Phase 2–5 issues with the gateway direction. Retain their history, explain scope changes, and move current work into outcome-based milestones. Retired directions must not be described as delivered capabilities.

Define the reference profile in #81: pinned PostgreSQL and MySQL, synthetic data, two application identities, two tenants, and explicit expected results and denials. An existing MinIO format may be added for object-storage coverage. Real customer work remains deferred.

**Exit:** the issue disposition is complete, the reference scope is documented, and public-interface acceptance cases have fixed expectations.

## M1 Reproducible public access

- [#404](https://github.com/RhinoLink-Tech/data-fabric/issues/404): follow the completed #400/#401 CI split with affected reference and connector validation before merge. Preserve the fast required check and complete scheduled/manual matrix; releases require commit-matched full evidence.
- #81: extend the existing reference deployment to register both reference databases and support independent application identities.
- [#403](https://github.com/RhinoLink-Tech/data-fabric/issues/403): add public REST/MCP task acceptance by extending existing validation assets. Exercise identity, source discovery, metadata, parameter binding, results, continuation, denied access, cancellation, errors, and audit correlation.
- #365: deliver the continuation ADR and a two-replica verification matrix. Cover REST routing and MCP tool/session behavior explicitly.

**Exit:** every declared deterministic reference case passes, unauthorized access succeeds zero times, setup is repeatable, and measurements include source/configuration versions and relevant dependencies. Fixed-SQL results are not reported as model task success rates.

## M2 Reliable shared operation

Create implementation issues only after the corresponding contract or design is ready.

- Implement the #365 decision: query owner routing, owner generation, token and identity binding, bounded drain, and explicit owner-loss behavior. Keep live database cursors with their owner; do not implicitly rerun SQL after owner failure.
- [#413](https://github.com/RhinoLink-Tech/data-fabric/issues/413): enforce cumulative per-query result rows, encoded-byte budgets, and an overall deadline across initial and continuation requests. Define COUNT overhead and partial-result behavior. These controls do not represent universal scan-cost or cloud-spend limits.
- #99: select and implement a bounded strategy for validated policy snapshots, atomic publication, revocation, policy-version audit correlation, and documented cross-replica propagation limits.
- Preserve a lightweight single-replica mode without a new mandatory infrastructure service.

**Exit:** the two-replica routing/failure matrix, cumulative-budget cases, and policy revocation cases pass. Partial results, stale policy, and lost owners have explicit behavior. Transparent recovery of a lost live cursor is outside this increment.

## M3 Repeatable delivery

Use failures from the same task set to decide which task-context improvements to implement:

- #66: bounded, authorization-aware schema search when table selection or context size causes failures.
- #67: versioned business terms, confirmed relationships, examples, or approved parameterized query assets when schema alone is insufficient.
- #68: schema-change detection and affected-asset invalidation when those assets depend on persistent schema context.

Prepare a fixed release candidate and reproduce deployment, second-application reuse, upgrade, backup/restore, and draining behavior. Release evidence must match the candidate commit. Existing support and upgrade assets should be extended rather than rebuilt.

**Exit:** another operator can repeat setup and upgrade from the documentation; the second application reuses registered sources and policies; full candidate checks pass; capability claims match the support evidence.

## M4 Real-team validation

Start when the maintainer resumes real-team work. Select one actual task and source profile, then measure first integration effort, second-application reuse, task correctness, authorization outcomes, manual intervention, and ongoing support effort against the team's existing approach.

Use this evidence to choose additional supported source/version combinations and define the commercial reason to adopt the product. Reference environments alone do not establish willingness to pay or production fit.

## Demand-triggered work

- #175: add the bounded source-management UI when observed operator friction justifies it.
- #73: scope result masking when column denial and source-side restricted views do not satisfy a concrete use case.
- #83: document specific data-flow and residency boundaries when a deployment has explicit requirements.
- #179: resume real Trino verification only when the maintainer schedules it and a deterministic coordinator/catalog environment is available.

New connectors require an identified use case, an operation/identity/version boundary, and a repeatable verification plan. Existing experimental code and tests remain available under their documented status.

## Planning and release records

The [previous roadmap snapshot](https://github.com/RhinoLink-Tech/data-fabric/blob/bc0973d1557d75020ef2db252c9e0811f99ee7ca/docs/roadmap.md) preserves the earlier priorities and completed-work record. Its support counts are historical; use the generated catalog for current support.

GitHub issues track execution. The historical Phase 2–5 milestones are retired planning records, not feature commitments. Keep at most two major implementation items in progress and create follow-up issues when their dependencies are ready.

Completed connector/TLS details remain in support, contract, and release documentation. [The v2.0.0 candidate review](releases/v2.0.0-candidate.md) records the published baseline; it does not replace current mainline evidence.
