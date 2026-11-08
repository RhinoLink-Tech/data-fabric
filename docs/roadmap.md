# Data Fabric Roadmap

**Last reviewed: 2026-11-08**

## Product direction

Data Fabric is a lightweight, self-hosted data access gateway for AI applications. Teams register and govern sources once and reuse a documented, read-only access contract across applications. Single-source queries use gateway drivers. An optional external federation engine owns cross-source planning and execution.

The next increment makes the existing foundation independently adoptable, useful and supportable. Product work selects a first adopter, recurring task and support profile while engineering improves installation, first use, correct results and operation. Model evaluation, actual external adoption and a commercial decision each require their own evidence; synthetic engineering acceptance cannot establish them.

## Active productization program

Tracking: [#439](https://github.com/RhinoLink-Tech/data-fabric/issues/439). The [open-source adoption and product value plan](plans/gateway-delivery.md#productization-program) records priorities, partial delivery gates and conditional proposals. The revised sequence supersedes the standalone-first plan merged in [PR #453](https://github.com/RhinoLink-Tech/data-fabric/pull/453). **The active issues describe planned work, not newly delivered capabilities or demonstrated customer value.**

| Milestone | Work | Exit evidence |
| --- | --- | --- |
| [M4 External adoption and product value](https://github.com/RhinoLink-Tech/data-fabric/milestone/10) | First adopter/task/support scope [#454](https://github.com/RhinoLink-Tech/data-fabric/issues/454); actual model evaluation [#456](https://github.com/RhinoLink-Tech/data-fabric/issues/456); external-team adoption [#458](https://github.com/RhinoLink-Tech/data-fabric/issues/458); paid-offer/support-cost decision [#459](https://github.com/RhinoLink-Tech/data-fabric/issues/459). | A fixed-task model comparison, an approved actual-team attempt and sustained-use or justified stop evidence, and a concrete commercial continue/change/stop decision. Preparation, interviews and synthetic reuse do not substitute for execution. |
| [M5 Installable release and guided first use](https://github.com/RhinoLink-Tech/data-fabric/milestone/11) | Documentation [#440](https://github.com/RhinoLink-Tech/data-fabric/issues/440); pinned runnable preview [#442](https://github.com/RhinoLink-Tech/data-fabric/issues/442); initialization/identity diagnostics [#445](https://github.com/RhinoLink-Tech/data-fabric/issues/445); independent first query and second-app reuse [#446](https://github.com/RhinoLink-Tech/data-fabric/issues/446). | A supported current-profile artifact and accurate instructions let unfamiliar operators complete an allowed task and independent second-app reuse; failed attempts, intervention, persistence and release checks are recorded. |
| [M6 Evidence-driven deployment and runtime improvements](https://github.com/RhinoLink-Tech/data-fabric/milestone/12) | Conditional P2 work: combined runtime [#443](https://github.com/RhinoLink-Tech/data-fabric/issues/443); local metadata [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444); catalog side effects [#447](https://github.com/RhinoLink-Tech/data-fabric/issues/447); smaller distributions [#448](https://github.com/RhinoLink-Tech/data-fabric/issues/448); parser optimization [#449](https://github.com/RhinoLink-Tech/data-fabric/issues/449); optional totals [#450](https://github.com/RhinoLink-Tech/data-fabric/issues/450). | Each selected change has a measured or observed trigger, a recorded trade-off and candidate-matched comparison. Untriggered proposals may remain open and do not block initial adoption. |
| [M7 Reliable task operation and recovery](https://github.com/RhinoLink-Tech/data-fabric/milestone/13) | End-to-end/whole-stack baseline [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441); result/type/pagination contracts [#455](https://github.com/RhinoLink-Tech/data-fabric/issues/455); complete-query observability [#457](https://github.com/RhinoLink-Tech/data-fabric/issues/457) (completed in [PR #470](https://github.com/RhinoLink-Tech/data-fabric/pull/470)); selected-profile resource limits [#451](https://github.com/RhinoLink-Tech/data-fabric/issues/451); current-MySQL recovery/upgrade [#452](https://github.com/RhinoLink-Tech/data-fabric/issues/452). | Reproducible task/capacity evidence, explicit complete/partial result guarantees, actionable failure signals, bounded selected-profile resources and an independently repeatable candidate recovery. |

### Recent accepted operational evidence

- [#457](https://github.com/RhinoLink-Tech/data-fabric/issues/457) completed in [PR #470](https://github.com/RhinoLink-Tech/data-fabric/pull/470), merged as a3fa5a71338f121e6848faec9fb6f52877e5a07d. Public reference acceptance run [42446712854](https://github.com/RhinoLink-Tech/data-fabric/actions/runs/42446712854) passed A27 policy-sync and audit-persistence fault injection for REST and MCP in single- and two-replica topologies. The run tested candidate source 3a38f2463184d4cb5b308e7023ac35749b18b7d4 with gateway image sha256:e3d08c571e11278e614efd083773818dbcbda7c18b75d4a0bb06fe5a98522c6d; its [acceptance artifact](https://github.com/RhinoLink-Tech/data-fabric/actions/runs/42446712854/artifacts/13604276327) records the output. This is synthetic reference fault evidence, not a production availability or durability guarantee.

Start the two major implementation slots with [#440](https://github.com/RhinoLink-Tech/data-fabric/issues/440) and [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441). Product scope and recruitment preparation in [#454](https://github.com/RhinoLink-Tech/data-fabric/issues/454), plus model/pilot preparation, proceed alongside them. Claim each implementation issue with its branch/worktree and files, inspect overlapping claims and PRs, and keep at most two major implementations active. A reproduced correctness, security or resource failure gets an explicit reprioritization.

The current MySQL metadata and separate REST/MCP topology can serve initial delivery and evaluation. Next prepare [#442](https://github.com/RhinoLink-Tech/data-fabric/issues/442)'s immutable local/RC candidate and [#445](https://github.com/RhinoLink-Tech/data-fabric/issues/445)'s setup interfaces. [#452](https://github.com/RhinoLink-Tech/data-fabric/issues/452) rehearses recovery on that same SHA/image before formal publication; the release then checks the downloaded digest and first use. Neither rehearsal nor the first release waits for SQLite, a combined runtime, Core/Full packages or every optimization.

[#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441) closes on an accurate current-profile report and reusable runner, including measured failures and linked follow-ups. Future implementations own their candidate reruns. Minimum measurements and relevant contracts can support a preview before the complete load/soak study; recorded correctness and release gates still apply. Milestone numbers group outcomes and do not impose a serial queue.

The earlier numeric hypotheses remain historical candidate targets: one application container/volume, first query within five minutes, cached-image readiness within three seconds, a Core image at most 150 MiB uncompressed, warm idle at most 128 MiB, and four small concurrent queries at most 256 MiB peak. They are not current guarantees or universal preview blockers. [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441) must measure the chosen task/profile and freeze, revise or retire each hypothesis with rationale; the [delivery plan](plans/gateway-delivery.md#historical-candidate-hypotheses-and-measurement) retains their original measurement boundaries.

Existing authorization, policy revocation, SQL validation, audit, continuation and result budgets remain the baseline. Runtime issues require implementation and observable acceptance; model, adoption and commercial issues require the actual outcomes and decisions specified in their bodies. [#439](https://github.com/RhinoLink-Tech/data-fabric/issues/439) may close on its required outcome gates while optional M6 proposals remain untriggered. This documentation update closes none of those tasks.

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

The reference profile was delivered in #81: pinned PostgreSQL and MySQL, synthetic data, two application identities, two tenants, and explicit expected results and denials. An existing MinIO format may be added for object-storage coverage. This completed engineering profile did not include real customer work; external evaluation is active planned work under M4 below.

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

## M4 External adoption and product value

Product selection, model evaluation and external-team planning are active alongside delivery. [#454](https://github.com/RhinoLink-Tech/data-fabric/issues/454) assigns an owner, selects one first-adopter hypothesis and recurring task, compares the existing approach and alternatives, and defines exact source/operation/client/identity/deployment and support boundaries. Recruitment and baseline preparation start now; a lack of participants remains visible rather than turning into an indefinite deferral.

[#456](https://github.com/RhinoLink-Tech/data-fabric/issues/456) prepares its corpus now, then runs at least one actual supported model through discovery, schema, validation, parameterized execution, continuation and answer generation. Freeze expected results, model/prompt/Skill versions, policy, retry/token/time/cost limits and the baseline before execution. Actual provider runs need configured credentials and an owner-approved run budget. Use [#455](https://github.com/RhinoLink-Tech/data-fabric/issues/455)'s relevant result oracles; fake-model bridge checks and fixed SQL remain separate engineering evidence.

[#458](https://github.com/RhinoLink-Tech/data-fabric/issues/458) schedules actual external-team attempts using the [pilot evidence protocol](validation/real-team-pilot-evidence.md). Suggested planning sizes are 2–3 teams and 2–4 weeks; record the actual cohort and agreed observation window. Actual access requires a named owner and approved environment/data. Compare first integration, correctness, allowed/denied access, second-app reuse, manual intervention and ongoing support against the existing workflow; include a permission/credential/application change or an approved recovery drill. A real attempt that reaches a pre-agreed stop rule is valid negative evidence. Recruitment refusal, a booked session, an interview or a synthetic demo alone cannot close the execution task.

[#459](https://github.com/RhinoLink-Tech/data-fabric/issues/459) prepares a buyer/problem and concrete paid-offer hypothesis alongside scope work. Its final decision uses actual adoption/support evidence, customer response, customization and operating costs. Interest, a quote, paid evaluation and recurring payment are recorded distinctly; a justified change/stop decision is valid. Outreach, commercial agreements and payment are separately executed maintainer actions.

Use these observations to choose further source/version support and product work. Initial evaluation can use the current supported profile with disclosed limits and adopt #442/#445/#446 increments as available; no M6 architecture proposal gates it. No model-quality, customer-value, production-fit or paid outcome is claimed by this plan.

## Demand-triggered work

- #175 (engineering reference delivered): the [source-management UI](data-source-management-ui.md) is available at /ui/. Its impact on operator effort remains unvalidated; measure it under M4 before making a customer-value claim.
- [#73](https://github.com/RhinoLink-Tech/data-fabric/issues/73) (scope delivered): the scoped target is a synthetic S3/MinIO CSV dispatch task. A dispatcher may directly project only the last four phone digits; a supervisor sees the full value only with an explicit column-read grant. The target denies operator use of the field in filters, joins, grouping, sorting, aggregates, and expressions. See the [scope decision](https://github.com/RhinoLink-Tech/data-fabric/issues/73#issuecomment-6708302260). Runtime masking remains demand-triggered and requires a deployed task.
- #83: document specific data-flow and residency boundaries when a deployment has explicit requirements.
- Broader federation-engine evidence (other Trino releases, Presto, OpenLooKeng, authentication modes, connectors, or production topologies) remains demand-triggered; expand only against a concrete need and a reproducible version boundary.

New connectors require an identified use case, an operation/identity/version boundary, and a repeatable verification plan. Existing experimental code and tests remain available under their documented status.

## Planning and release records

The [previous roadmap snapshot](https://github.com/RhinoLink-Tech/data-fabric/blob/bc0973d1557d75020ef2db252c9e0811f99ee7ca/docs/roadmap.md) preserves the earlier priorities and completed-work record. Its support counts are historical; use the generated catalog for current support.

GitHub issues track execution. The historical Phase 2–5 milestones are retired planning records. The plan merged in [PR #453](https://github.com/RhinoLink-Tech/data-fabric/pull/453) is retained in Git history; the revised [#439](https://github.com/RhinoLink-Tech/data-fabric/issues/439) priorities and M4–M7 partial delivery gates supersede its standalone-first sequence. Ready preparation, blocked final execution and evidence-triggered proposals have distinct issue statuses. Keep at most two major implementations in progress and update the tracker after each accepted increment.

Completed connector/TLS details remain in support, contract, and release documentation. [The v2.0.0 candidate review](releases/v2.0.0-candidate.md) records the published baseline; it does not replace current mainline evidence.
