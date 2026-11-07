# Data Fabric Roadmap

**Last reviewed: 2026-11-07**

## Product direction

Data Fabric is a lightweight, self-hosted data access gateway for AI applications. Teams register and govern sources once and reuse a documented, read-only access contract across applications. Single-source queries use gateway drivers. An optional external federation engine owns cross-source planning and execution.

The next milestones turn the delivered gateway foundation into a simpler product to install, use and operate. Real-team pilots remain a separate M4 activity; isolated synthetic business fixtures are sufficient for this engineering program. Engineering reference results and customer value evidence are tracked separately.

## Active productization program

Tracking: [#439](https://github.com/RhinoLink-Tech/data-fabric/issues/439). Implementation order, dependencies and provisional budgets are in the [productization delivery plan](plans/gateway-delivery.md#productization-program). **The issues below are planned work, not delivered features.**

The next increment is governed by four outcomes: **simple installation, easy first use, fast deployment, and low resource use**. New capability work must improve one of these outcomes and preserve the existing access-control and query contracts. First use means an operator can initialize the instance, connect one source, execute an authorized query, and reuse it from a second application.

| Milestone | Work | Exit evidence |
| --- | --- | --- |
| [M5 Simple deployment](https://github.com/RhinoLink-Tech/data-fabric/milestone/11) | Documentation [#440](https://github.com/RhinoLink-Tech/data-fabric/issues/440); full-stack baseline [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441); runnable artifacts [#442](https://github.com/RhinoLink-Tech/data-fabric/issues/442); shared REST/MCP runtime [#443](https://github.com/RhinoLink-Tech/data-fabric/issues/443); local metadata [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444); initialization/diagnostics [#445](https://github.com/RhinoLink-Tech/data-fabric/issues/445). | A clean, persistent standalone installation from a pinned artifact, a successful authorized first task, and a repeatable restart with measured time and total resource use. |
| [M6 Guided use and on-demand connectors](https://github.com/RhinoLink-Tech/data-fabric/milestone/12) | Guided first query and application reuse [#446](https://github.com/RhinoLink-Tech/data-fabric/issues/446); lazy connector resources [#447](https://github.com/RhinoLink-Tech/data-fabric/issues/447); Core/Full builds [#448](https://github.com/RhinoLink-Tech/data-fabric/issues/448). | An independent operator completes the guided path; unused sources create no resource-bearing clients; both package profiles state exactly which connectors are included and verified. |
| [M7 Efficient and recoverable operation](https://github.com/RhinoLink-Tech/data-fabric/milestone/13) | Bounded parser reuse [#449](https://github.com/RhinoLink-Tech/data-fabric/issues/449); optional exact totals [#450](https://github.com/RhinoLink-Tech/data-fabric/issues/450); aggregate resource controls [#451](https://github.com/RhinoLink-Tech/data-fabric/issues/451); backup/restore/upgrade [#452](https://github.com/RhinoLink-Tech/data-fabric/issues/452). | Candidate-matched workload and failure evidence meet the reviewed resource budgets, and a fresh-instance recovery preserves the required metadata and keys. |

Start with [#440](https://github.com/RhinoLink-Tech/data-fabric/issues/440) and [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441). Later tasks may proceed when their explicit dependencies pass; milestones group outcomes rather than imposing a mandatory serial queue. Claim an issue before coding, name the branch/worktree and intended files, and check other claims and PRs. Keep at most two major implementations active.

The standalone target is one application container plus a persistent volume. Candidate budgets are first authorized query within five minutes, cached-image readiness within three seconds, Core image at most 150 MiB uncompressed, whole-container warm idle at most 128 MiB, and four fixed small concurrent queries at most 256 MiB peak. These are **provisional targets**, not current performance guarantees. [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441) must measure and freeze or explicitly revise them with evidence. See the delivery plan for prerequisites and measurement boundaries.

MySQL-backed team deployments and existing optional owner routing remain supported. Local metadata and combined REST/MCP are proposed changes; current limits below still apply. Existing authorization, policy revocation, SQL validation, audit and result budgets must remain intact. Runtime issues close only after implementation and acceptance evidence; this planning update does not complete them.

## Delivered foundation

- Source lifecycle, connection diagnostics, credential handling, shared query services, and REST/MCP interfaces.
- Remote MCP enterprise OIDC verification; the management HTTP API retains its documented JWT authentication contract.
- Source ownership and tenant checks, optional table/column policies, and persisted redacted access audit events (#217–#220).
- Managed table-policy versions with compare-and-swap publication, request-pinned snapshots, publisher/outcome history, and bounded cross-replica revocation (#99).
- Parameter binding, schema discovery, read-only validation, query continuation, cancellation, and bounded admission controls.
- A generated connector support catalog and repeatable, version-pinned driver contracts.
- Existing Compose and remote MCP deployment guides (#180 and #186), plus deployment-footprint and upgrade-rehearsal evidence.
- A bounded, version-pinned Trino 484 public REST/MCP integration profile (#179; [validation report](validation/trino-integration.md)).

The [support catalog](data-source-support.md) and [driver contract report](driver-contract-tests.md) define exact connector evidence. Registered code, restricted experimental adapters, and verified operations remain distinct. Connector counts are not duplicated in this roadmap.

## Current operating limits

- Query cursors and continuation sessions remain process-local, so owner restart or loss invalidates active sessions. When opt-in routing is enabled, the reference ingress sends REST and remote MCP continuations to their creating owner using signed markers. Routing does not replicate query state or fail over to another owner; without the ingress, ordinary load balancing does not preserve continuations. See [Replica routing and drain](replica-routing.md).
- Admission limits are per process. A multi-replica continuation increment does not establish globally precise tenant quotas.
- Managed table/column policies use the shared metadata database. Healthy replicas converge within the documented three-second revocation window; an owner that cannot synchronize for three seconds fails closed.
- Persisted audit is available, but the current failure policy does not guarantee durable evidence for every access when audit storage fails.
- REST and MCP run in separate service processes in the reference deployment. Shared source metadata does not make live query cursors interchangeable between them.
- SQL translation covers the documented compatibility subset and does not guarantee identical execution semantics across engines.
- The Trino 484 opt-in [integration profile](validation/trino-integration.md) covers only its listed read-only query, timeout, coordinator-recovery, and engine-error paths through REST and MCP. Other releases, authentication modes, connectors, SQL features, worker topologies, and production workloads remain outside this evidence.

## M0 Product scope and engineering acceptance

Tracking: [#402](https://github.com/RhinoLink-Tech/data-fabric/issues/402). Detailed tasks and the reference acceptance matrix are in the [gateway delivery plan](plans/gateway-delivery.md).

Reconcile the historical Phase 2–5 issues with the gateway direction. Retain their history, explain scope changes, and move current work into outcome-based milestones. Retired directions must not be described as delivered capabilities.

The reference profile was delivered in #81: pinned PostgreSQL and MySQL, synthetic data, two application identities, two tenants, and explicit expected results and denials. An existing MinIO format may be added for object-storage coverage. Real customer work remains deferred.

**Exit:** the issue disposition is complete, the reference scope is documented, and public-interface acceptance cases have fixed expectations.

## M1 Reproducible public access

- [#404](https://github.com/RhinoLink-Tech/data-fabric/issues/404) (completed): added affected reference and connector validation before merge while retaining the fast required check and scheduled/manual full matrix.
- [#81](https://github.com/RhinoLink-Tech/data-fabric/issues/81) (completed): registered the pinned reference databases and validated reuse across independent application identities and tenants.
- [#403](https://github.com/RhinoLink-Tech/data-fabric/issues/403) (completed): added deterministic public REST/MCP acceptance for identity, discovery, metadata, parameters, results, continuation, denials, cancellation, errors, and audit correlation.
- [#365](https://github.com/RhinoLink-Tech/data-fabric/issues/365) (completed): delivered ADR-003 and a two-replica verification matrix in [PR #407](https://github.com/RhinoLink-Tech/data-fabric/pull/407). Owner-routing implementation and containerized evidence followed in [#409](https://github.com/RhinoLink-Tech/data-fabric/issues/409), [#410](https://github.com/RhinoLink-Tech/data-fabric/pull/410), [#411](https://github.com/RhinoLink-Tech/data-fabric/issues/411), and [#412](https://github.com/RhinoLink-Tech/data-fabric/pull/412).

**Exit (met):** every declared deterministic reference case passed, unauthorized access succeeded zero times, setup is repeatable, and measurements include source/configuration versions and relevant dependencies. Fixed-SQL results are not reported as model task success rates.

## M2 Reliable shared operation

Create implementation issues only after the corresponding contract or design is ready.

- [#409](https://github.com/RhinoLink-Tech/data-fabric/issues/409) (completed): implemented the #365 decision for owner-bound REST/MCP continuations, signed owner generations, identity binding, bounded drain, and explicit owner-loss behavior. Live cursors stay with their owner; failed work is not rerun implicitly. See the [#410 routing implementation](https://github.com/RhinoLink-Tech/data-fabric/pull/410) and [#412 two-replica evidence](https://github.com/RhinoLink-Tech/data-fabric/pull/412).
- [#413](https://github.com/RhinoLink-Tech/data-fabric/issues/413) (completed): enforced cumulative per-query result rows, encoded-byte budgets, and an overall deadline across initial and continuation requests. COUNT overhead and incomplete-result behavior are explicit; these controls do not represent universal scan-cost or cloud-spend limits. See [PR #414](https://github.com/RhinoLink-Tech/data-fabric/pull/414).
- [#99](https://github.com/RhinoLink-Tech/data-fabric/issues/99) (completed): implemented validated policy snapshots, atomic publication, revocation, policy-version audit correlation, and the three-second cross-replica contract; see [ADR-004](decisions/adr-004-versioned-table-policy-publication.md) and [PR #415](https://github.com/RhinoLink-Tech/data-fabric/pull/415).
- Routing remains opt-in, preserving lightweight single-replica REST, MCP, and stdio operation without a new mandatory infrastructure service.

**Exit (met):** the two-replica routing/failure matrix, cumulative-budget cases, and policy revocation cases passed. Partial results, stale policy, and lost owners have explicit behavior. Transparent recovery of a lost live cursor remains outside this increment.

## M3 Repeatable delivery

Use the same representative task set to scope context improvements. The agent-side context and schema-drift reference is delivered for a maintainer-directed warehouse batch-route scenario using synthetic records. This is engineering evidence, not a customer deployment; live source and model fit remain in M4.

- [#66](https://github.com/RhinoLink-Tech/data-fabric/issues/66) (completed): Added a conditional MCP search across permitted catalog, schema, table, and visible-column names/comments. It applies existing authorization before searching, returns no comment text, caps results at 20, scans at most 100 permitted tables, returns at most five matching column names per result, and reports ambiguity and truncation. See [PR #422](https://github.com/RhinoLink-Tech/data-fabric/pull/422).
- [#67](https://github.com/RhinoLink-Tech/data-fabric/issues/67) (engineering reference delivered): added one versioned context asset and a batch-scoped parameterized query. The Skill compares required columns and types, validates and binds the query, and current gateway policy still applies. See [PR #436](https://github.com/RhinoLink-Tech/data-fabric/pull/436).
- [#68](https://github.com/RhinoLink-Tech/data-fabric/issues/68) (agent-guided invalidation delivered): the Skill compares the live schema with the asset, stops before execution on a mismatch, and documents owner review and version refresh. This reference does not add gateway-side schema monitoring. See [PR #436](https://github.com/RhinoLink-Tech/data-fabric/pull/436).

- [#416](https://github.com/RhinoLink-Tech/data-fabric/issues/416) (completed): Recorded commit-matched synthetic deployment, second-application reuse, two-replica acceptance, and MySQL upgrade/recovery in the [candidate report](validation/results/2026-11-07-post-m2-candidate.md), using the [post-M2 candidate evidence runbook](validation/post-m2-candidate-evidence.md). This record does not establish production readiness or real-team fit.

Extend the existing reference acceptance and upgrade rehearsal only for candidate-specific changes; do not repeat completed work without such a change.

**Exit:** another operator can repeat setup and upgrade from the documentation; the second application reuses registered sources and policies; full candidate checks pass; capability claims match the support evidence.

## M4 Real-team validation

Start when the maintainer resumes real-team work. Use the [pilot evidence protocol](validation/real-team-pilot-evidence.md) to select one actual task and source profile, record the team's baseline, and measure first integration effort, second-application reuse, task correctness, authorization outcomes, manual intervention, and ongoing support effort.

Use this evidence to choose additional supported source/version combinations and define the commercial reason to adopt the product. Reference environments alone do not establish willingness to pay or production fit.

## Demand-triggered work

- #175 (engineering reference delivered): the [source-management UI](data-source-management-ui.md) is available at /ui/. Its impact on operator effort remains unvalidated; measure it under M4 before making a customer-value claim.
- [#73](https://github.com/RhinoLink-Tech/data-fabric/issues/73) (scope delivered): the scoped target is a synthetic S3/MinIO CSV dispatch task. A dispatcher may directly project only the last four phone digits; a supervisor sees the full value only with an explicit column-read grant. The target denies operator use of the field in filters, joins, grouping, sorting, aggregates, and expressions. See the [scope decision](https://github.com/RhinoLink-Tech/data-fabric/issues/73#issuecomment-6708302260). Runtime masking remains demand-triggered and requires a deployed task.
- #83: document specific data-flow and residency boundaries when a deployment has explicit requirements.
- Broader federation-engine evidence (other Trino releases, Presto, OpenLooKeng, authentication modes, connectors, or production topologies) remains demand-triggered; expand only against a concrete need and a reproducible version boundary.

New connectors require an identified use case, an operation/identity/version boundary, and a repeatable verification plan. Existing experimental code and tests remain available under their documented status.

## Planning and release records

The [previous roadmap snapshot](https://github.com/RhinoLink-Tech/data-fabric/blob/bc0973d1557d75020ef2db252c9e0811f99ee7ca/docs/roadmap.md) preserves the earlier priorities and completed-work record. Its support counts are historical; use the generated catalog for current support.

GitHub issues track execution. The historical Phase 2–5 milestones are retired planning records, not feature commitments. M5–M7 issues have explicit acceptance dependencies; their existence does not make them all ready to implement. Keep at most two major implementation items in progress and update the active tracker after each accepted increment.

Completed connector/TLS details remain in support, contract, and release documentation. [The v2.0.0 candidate review](releases/v2.0.0-candidate.md) records the published baseline; it does not replace current mainline evidence.
