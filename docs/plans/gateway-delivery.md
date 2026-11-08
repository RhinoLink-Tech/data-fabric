# Gateway Delivery Plan

**Reviewed: 2026-11-07**

This plan connects the [roadmap](../roadmap.md)'s delivered M0–M3 foundation with the active M4–M7 open-source adoption and product value program in [#439](https://github.com/RhinoLink-Tech/data-fabric/issues/439). The 2026-11-07 issue revision supersedes the standalone-first sequence merged in [PR #453](https://github.com/RhinoLink-Tech/data-fabric/pull/453). Dated engineering evidence retains its recorded scope; real-model, actual adoption and commercial evidence require separate observed outcomes.

## Productization program

**Status: planned delivery and evaluation; this plan ships no new runtime capability and establishes no customer-value claim.**

Make the gateway independently adoptable, useful and supportable. Select a first adopter, repeatable task and support promise; ship one usable current profile; verify correct complete tasks; measure reuse and operating effort; and decide whether teams continue using it and accept a repeatable paid offer. Simple installation, easy first use, fast deployment and bounded resource use support those outcomes.

### Existing baseline and profile decisions

The engineering baseline reviewed for the original program is `07d3c834b4cd9abe1e40ec942125912623398a2e`. At that baseline, production metadata uses MySQL; REST and MCP run in separate processes; published v2.0.0 artifacts contain source rather than a ready-to-run image; and the UI requires an operator-supplied JWT. All connector families are compiled into the executable. SQL connections already open on use, while support-catalog construction invokes factories. A factory call is not evidence that all sources connect at startup. Current release claims must follow the candidate and support catalog, rather than treating this dated baseline as a new measurement.

The [2026-11-01 footprint report](../performance/deployment-footprint.md) records a 119.5 MiB executable, a 236.1 MiB uncompressed image and 83.61 MiB idle PID RSS. Its original scope excludes the external metadata database and whole-container parser-child memory. Preserve it as historical evidence; [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441) supplies the current task/stack baseline.

| Profile or proposal | Role in the revised plan | Capability boundary |
| --- | --- | --- |
| Current supported topology | First runnable preview, guided use and evaluation can use MySQL metadata with separate REST and remote-MCP processes. [#454](https://github.com/RhinoLink-Tech/data-fabric/issues/454) selects exact source/client/identity/operation support. | REST management retains HS256 JWT; production remote MCP uses OIDC and protected transport. Principal/tenant mapping must be explicit. Existing optional owner routing retains process-local cursors and documented owner-loss behavior. |
| Combined runtime | P2 [#443](https://github.com/RhinoLink-Tech/data-fabric/issues/443), only after duplicated cost or setup friction is demonstrated. | Compare against improving the current topology. Preserve separate transport/stdio modes, identity boundaries, continuation and lifecycle semantics. |
| Local single-node metadata | P2 [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444), with SQLite evaluated only after a recorded adopter/deployment need. | An accepted profile needs migration, CAS/policy/audit, persistent-key and recovery evidence. It is not shared-network-disk storage or live-query failover; it does not technically depend on #443. |
| Core / Full distributions | P2 [#448](https://github.com/RhinoLink-Tech/data-fabric/issues/448), when measured download/build/support costs justify another package matrix. | Record exact connector contents and verified support. Runtime flags alone do not remove linked SDKs. Existing distribution can serve first adopters. |

One application container and a persistent volume remain a historical design hypothesis. No new profile is advertised before it is implemented and verified. Business sources remain external. Initialization must preserve operator-managed secrets and never add a permanent default account or authentication bypass.

### Work, priorities and delivery gates

Milestones group outcomes rather than a serial implementation queue. `ready` allows scoped preparation/implementation; final execution or publication may still require the relevant interface or evidence below. `blocked` identifies an upstream decision/contract, while `needs-evidence` is an untriggered implementation hypothesis. Partial measurements do not require closing every related issue first. Native blocked-by relationships track whole-issue prerequisites; each issue body is authoritative for its exact acceptance.

| Milestone | Issue and priority | Start condition and relevant evidence |
| --- | --- | --- |
| [M4 External adoption and product value](https://github.com/RhinoLink-Tech/data-fabric/milestone/10) | P1 [#454](https://github.com/RhinoLink-Tech/data-fabric/issues/454): first adopter, recurring task and supported release profile | Ready now. Assign owner, select task and alternatives, define support/continue/change/stop criteria, and record participants or recruitment actions alongside delivery. |
| M4 | P1 [#456](https://github.com/RhinoLink-Tech/data-fabric/issues/456): real-model complete-task comparison | Prepare corpus/harness now. Final comparison uses #454 scope and relevant #455 oracles; actual runs need configured credentials and an approved run budget. |
| M4 | P1 [#458](https://github.com/RhinoLink-Tech/data-fabric/issues/458): actual external first use and sustained adoption | Prepare recruitment/baseline now. #454 selects team/task; actual access needs a named owner and approved environment. Current supported profile is sufficient. |
| M4 | P2 [#459](https://github.com/RhinoLink-Tech/data-fabric/issues/459): paid offer and support-cost decision | Prepare buyer/problem and offer alongside #454; final decision uses actual #458 evidence and decision-maker feedback. |
| [M5 Installable release and guided first use](https://github.com/RhinoLink-Tech/data-fabric/milestone/11) | P1 [#440](https://github.com/RhinoLink-Tech/data-fabric/issues/440): accurate adoption path, roadmap and references | Ready now. Fix factual drift and incorporate the selected profile; first usable instructions do not wait for every historical reference cleanup. |
| M5 | P1 [#442](https://github.com/RhinoLink-Tech/data-fabric/issues/442): pinned runnable preview and release contract | Packaging can start now. Publication uses relevant #440 fixes, #441 minimum measurements, #455 selected contract cases and same-candidate #452 recovery. Existing topology is sufficient; offline variants/extra architectures need their own demand and executable evidence. |
| M5 | P1 [#445](https://github.com/RhinoLink-Tech/data-fabric/issues/445): idempotent setup, identity alignment and diagnostics | Ready on current MySQL/JWT/OIDC contracts. Coordinate #440/#442; no #443/#444 prerequisite. Preserve keys and remove handwritten JWT signing from the recommended path. |
| M5 | P1 [#446](https://github.com/RhinoLink-Tech/data-fabric/issues/446): independent first query and second-app reuse | Observe/prepare now. Implementation uses relevant #445 setup contracts; final unfamiliar-operator rehearsal uses #442 and #454. Preserve public REST metadata limitations unless a required policy-checked endpoint is delivered. |
| [M7 Reliable task operation and recovery](https://github.com/RhinoLink-Tech/data-fabric/milestone/13) | P1 [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441): end-to-end tasks, capacity and whole-stack cost | Ready now on the existing profile. Publish minimum measurements early, then staged load/soak evidence. Close on an accurate report/reusable runner with failures and follow-ups; future profiles own their reruns. |
| M7 | P1 [#455](https://github.com/RhinoLink-Tech/data-fabric/issues/455): result/type/pagination/partial-answer contracts | Ready on existing source fixtures; align final scope with #454. Compare real REST/MCP values and mutable-page oracles; define unknown totals before #450. |
| M7 | P1 [#457](https://github.com/RhinoLink-Tech/data-fabric/issues/457): complete-query and operational-failure visibility | Design/reproduce now alongside #441. Instrument agreed initial lifecycle boundaries; audit failure, policy freshness and logical-query outcomes do not wait for full soak or a new runtime. |
| M7 | P1 [#451](https://github.com/RhinoLink-Tech/data-fabric/issues/451): selected-profile aggregate bounds and cleanup | Use initial #441 measurements or an earlier reproduced resource fault. Fix the selected SQL profile first; extend object/file limits only where that profile needs them. #443/#447 are coordination points, not universal gates. |
| M7 | P1 [#452](https://github.com/RhinoLink-Tech/data-fabric/issues/452): usable current-MySQL backup/restore/upgrade | Ready using existing contracts. Final unfamiliar-operator rehearsal uses #442's immutable local/RC candidate and relevant #445 output, before formal publication. A later local backend owns separate evidence. |
| [M6 Evidence-driven deployment and runtime improvements](https://github.com/RhinoLink-Tech/data-fabric/milestone/12) | P2 [#443](https://github.com/RhinoLink-Tech/data-fabric/issues/443): combined runtime | Needs measured duplication/adopter friction and a design comparison. Relevant initial #441 evidence is enough; no dependent universal refactor chain. |
| M6 | P2 [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444): local metadata | Needs #454/adoption friction and relevant cost evidence. No technical #443 dependency; current-profile delivery/evaluation/recovery continue independently. |
| M6 | P2 [#447](https://github.com/RhinoLink-Tech/data-fabric/issues/447): minimal catalog/factory side-effect fix | Needs a reproduced side effect, typed-setup limitation or relevant #441 cost. Aggregate enforcement stays in #451 and package trimming in #448. |
| M6 | P2 [#448](https://github.com/RhinoLink-Tech/data-fabric/issues/448): smaller distributions | Needs measured artifact/adoption benefit, distribution-cost decision and reusable #442 publication path; #447 coordination only where code boundaries overlap. |
| M6 | P2 [#449](https://github.com/RhinoLink-Tech/data-fabric/issues/449): measured parser optimization | Needs a task bottleneck/target and relevant #441 comparison. Worker pools/caches are options; #443 and full-baseline completion are not prerequisites. |
| M6 | P2 [#450](https://github.com/RhinoLink-Tech/data-fabric/issues/450): optional exact totals | Wait for relevant #441 count-cost evidence and #455 unknown-total/completion contract. Preserve v1 defaults and compare source work/first/full-result latency. |

### First execution window and release flow

1. Product owner starts #454 scope and recruitment preparation while #440 and #441 occupy the two initial major implementation slots. Model/pilot corpus and baseline preparation can proceed alongside. A reproduced wrong result, authorization failure or uncontrolled resource growth takes precedence through an explicit issue decision.
2. Use the next available slots for #442 packaging and #445 safe setup/identity diagnostics. Prepare an immutable local/RC image from one exact candidate SHA. Run #452 upgrade/restore against that same image and relevant setup output before formal publication. This is a candidate gate, not a dependency on an already public image. Once published, #442 verifies downloaded digest and first use; recovery need not be rerun merely because identical bytes were uploaded.
3. #446 improves the selected current path and observes unfamiliar operators; #455 provides task-specific correctness/completeness boundaries; #456 executes the actual model comparison when scope, oracles and approved provider access are ready. Relevant interfaces are sufficient; closing all related issues first is not required.
4. Schedule #458 actual attempts on the current supported profile, adopt new release/setup increments as available, and observe agreed sustained use. Feed measured operating gaps into #457/#451/#452 and commercial/support evidence into #459.
5. Promote M6 proposals only after their recorded triggers. Each changed implementation reruns applicable acceptance and #441's runner on its own candidate; untriggered architecture work does not block independent use or actual learning.

M4 exits on observed model/adoption outcomes and product/commercial decisions, including justified negative outcomes from actual attempts. M5 exits on a runnable supported profile and independently completed first-task/second-app path. M7 exits on measured task contracts, actionable operating signals, bounded selected-profile behavior and fresh-instance recovery. M6 accepts only selected, justified improvements with comparisons; optional proposals may remain untriggered. Link evidence from #439 and the milestone before recording completion. This planning change completes none of these outcomes.

### Historical candidate hypotheses and measurement

The following numbers and topology preserve the original program's candidate hypotheses and boundaries. They are not delivered guarantees, a selected support profile or universal prerequisites for an honest preview. #441 measures the current profile and records whether to freeze, revise or retire each hypothesis with rationale; a missed or retired target stays visible. Chosen task SLOs/resource limits must be justified by observed work. Correctness, isolation and the advertised recovery contract remain release requirements.

| Metric | Historical candidate hypothesis | Original measurement boundary and decision needed |
| --- | --- | --- |
| Standalone deployment | One application container and one persistent data volume; no external metadata database. | Business sources remain external; demo sources counted separately. Evaluate #443/#444 only if adopter/cost evidence justifies this topology. |
| First authorized query | At most 5 minutes for an independent operator. | Approved reachable source details supplied; report image download separately. Select task/profile and count intervention before choosing a promise. |
| Start to readiness | At most 3 seconds. | Cached image, standalone profile, empty metadata directory; report restart/migration separately. Measure current topology first. |
| Core image size | At most 150 MiB uncompressed. | Record connector set, architecture, digest and packaged SQL runtime. Assess #448 benefit and distribution cost rather than assuming a Core build is required. |
| Warm idle memory | At most 128 MiB for the whole standalone application container. | After first task and five idle minutes; include parser children/cgroup memory. Report current required stack as well. |
| Light-load peak memory | At most 256 MiB. | Four concurrent fixed small PostgreSQL/MySQL queries; object/file stress remains separate. Set a selected-profile envelope from measurements. |
| Recovery | Supported upgrade, consistent backup and fresh-instance restore preserve sources, encrypted credentials, policy revisions and required audit. | Missing keys/incompatible schemas fail clearly. #452 verifies current MySQL candidate first; later local profiles require their own record. |

Measure actual REST/MCP authentication, authorization/parsing, source execution, first and complete multi-batch results, rejection, cancellation and cleanup. Record exact SHA, image identity, source/client/identity/model versions where applicable, hardware/configuration, workload and raw sample count. Separate download, setup, readiness, active operator effort and elapsed time. Include parser descendants and whole-container/cgroup memory; report gateway, required metadata/identity and source components individually and the required-stack total. Record COUNT/source calls separately from output budgets and held-cursor behavior separately from pagination re-execution.

Start with one/four concurrent small queries and startup/restart/denial/cancellation/warm-idle samples, then bounded slow/fast work, tenant contention and sustained-load observations. Publish P50/P95/P99 only when sample counts support them. #441 can close on the accurate current-profile report and runner, retaining measured failures and follow-up work; future combined/local-store/package candidates rerun it within their own issues. A partial initial measurement can inform a release or targeted change without treating the entire baseline study as a universal blocker.

### Acceptance and collaboration rules

- Claim the specific implementation issue before coding, naming branch, isolated Data Fabric worktree and intended files; inspect overlapping claims and PRs. Keep at most two major implementations active. A tracker/planning claim does not reserve every child.
- Each change explains the user/task or operating outcome and links observable evidence. A plan, mock or reference alone cannot close runtime/package/UX/operations delivery. Model/adoption/commercial tasks require their specified actual runs and decisions.
- Preserve owner/tenant checks, table/column policy and bounded revocation, read-only parsing, parameter binding, redacted audit, result budgets and continuation contracts. Keep process-local quotas/cursors, lost-owner restart and current audit durability limits explicit.
- #455 distinguishes ordering, mutable-page, exact-value and complete/partial result behavior; fix silent wrong-result violations before advertising an affected profile. SQL translation success is not semantic equivalence or cross-page snapshot consistency.
- #457 covers logical-query outcomes and operational visibility; #451 owns selected-profile enforcement. Neither promises cluster-wide quota accounting or durable audit for every access without separate implementation/evidence.
- Catalog side-effect fixes, smaller packages and parser/count optimizations require the relevant M6 trigger. Lazy resources do not reduce linked binary size; authorization decisions/results are not parser-cache entries. Preserve exact-count compatibility and fail-closed SQL behavior.
- Use isolated synthetic fixtures for engineering; do not access Atlas repositories/services/data. Actual team work uses a scheduled approved owner/environment; provider runs use approved credentials/budget. Keep credentials, raw business data and unredacted prompts out of repository reports. External outreach/agreements/payment are separately executed maintainer actions.
- Update #439 and the roadmap after acceptance. Keep synthetic gateway evidence, real-model outcomes, actual adoption and commercial response distinct. #67/#68 are bounded agent guidance; #73 delivered scope only, with runtime masking unimplemented. Optional untriggered M6 items need not be implemented to close the parent's required outcome gates.

## M0–M3 foundation record

These completed tasks provide the reference contracts for M4–M7. They are not a new implementation queue. The acceptance sections below retain the delivered baseline and its limits.

| Work | Tracking | Delivered outcome |
| --- | --- | --- |
| Roadmap reconciliation | [#402](https://github.com/RhinoLink-Tech/data-fabric/issues/402) | Historical scope and issue disposition reconciled. |
| Pre-merge source evidence | [#404](https://github.com/RhinoLink-Tech/data-fabric/issues/404) | Pinned reference contracts and conservative changed-path selection. |
| Heterogeneous reference access | [#81](https://github.com/RhinoLink-Tech/data-fabric/issues/81) | PostgreSQL/MySQL fixtures with isolated application identities and synthetic data. |
| Public task acceptance | [#403](https://github.com/RhinoLink-Tech/data-fabric/issues/403) | Deterministic REST/MCP cases with explicit pass/fail/skip/unsupported outcomes. |
| Owner routing and drain | [#365](https://github.com/RhinoLink-Tech/data-fabric/issues/365), [#409](https://github.com/RhinoLink-Tech/data-fabric/issues/409), [#411](https://github.com/RhinoLink-Tech/data-fabric/issues/411) | ADR, implementation and two-replica evidence; live cursors stay with their owner. |
| Cumulative query results | [#413](https://github.com/RhinoLink-Tech/data-fabric/issues/413) | Rows, encoded bytes and overall deadline across logical query batches. |
| Policy publication/revocation | [#99](https://github.com/RhinoLink-Tech/data-fabric/issues/99) | CAS publication, immutable request snapshots, outcome history and bounded synchronization; see [ADR-004](../decisions/adr-004-versioned-table-policy-publication.md). |
| Task context | [#66](https://github.com/RhinoLink-Tech/data-fabric/issues/66), [#67](https://github.com/RhinoLink-Tech/data-fabric/issues/67), [#68](https://github.com/RhinoLink-Tech/data-fabric/issues/68) | Bounded permitted-schema search and an agent-guided warehouse context/schema-drift reference; no gateway schema-monitoring service or real-team success claim. |
| Candidate delivery evidence | [#416](https://github.com/RhinoLink-Tech/data-fabric/issues/416) | Source SHA 53d0d662ac24273d8a5877dd4190188c843342c8; see the [candidate runbook](../validation/post-m2-candidate-evidence.md) and [2026-11-07 report](../validation/results/2026-11-07-post-m2-candidate.md). |

## Engineering reference profile

Use existing verified PostgreSQL and MySQL versions with pinned artifacts, synthetic data and least-privilege source accounts. Record exact images and configuration in the runnable profile. Optionally add one existing MinIO format when a database/file task needs it; preserve its restricted adapter contract.

Reuse the existing deployment structure: HTTP API and remote MCP run in separate processes and share source metadata. The HTTP management path retains its HS256 JWT contract; remote MCP validates OIDC. A local, controlled test issuer/JWKS must be isolated from production identity settings.

Provide two independent application identities with permitted reuse of the registered reference source, plus another tenant for denied-access cases. Bind parameter values through existing driver support. Source credentials must not be exposed in tool responses or committed to fixtures.

The setup must provide documented startup, registration, readiness/identity checks, diagnostics and cleanup. Record environment, prepared prerequisites, first setup, source registration, second-application steps and actual times. Measure gateway, metadata and identity components separately and together.

## Public task acceptance matrix

[#403](https://github.com/RhinoLink-Tech/data-fabric/issues/403) owns the runnable fixtures and exact expected values. The cases below define the initial contract; any unavailable or unsupported operation must be recorded explicitly rather than counted as a pass. Run applicable cases through both public interfaces using their documented authentication. Fixed-input checks must not be reported as real-model task success.

| Case | Task | Expected behavior |
| --- | --- | --- |
| A01 | Discover sources | Only permitted sources appear, with stable source identity. |
| A02 | Inspect source information | Useful non-secret fields appear; credentials do not. |
| A03 | Enumerate tables | Unauthorized tables are hidden. |
| A04 | Describe a permitted table | Permitted schema is correct; denied fields are hidden. |
| A05 | Bind ordinary and injection-shaped parameters | Values remain data and match the fixture oracle. |
| A06 | Read NULL, decimal and integer values | Documented values and normalized types match the oracle. |
| A07 | Execute supported filters, aggregation and a same-source join | Exact expected results; no implicit cross-source execution promise. |
| A08 | Retrieve all batches | All expected rows are accounted for, with no mixed sessions or silent early completion. |
| A09 | Reuse an old continuation token | The documented token-rotation/replay rule is enforced. |
| A10 | Fetch concurrently | Defined progress/conflict behavior; no double consumption or cross-session data. |
| A11 | Fetch after session expiry | Stable expiry behavior and resource cleanup. |
| A12 | Call without valid authentication | Rejected before data access. |
| A13 | Use an expired or invalid-signature token | Rejected; secrets are not logged. |
| A14 | Use the wrong OIDC issuer | Remote MCP rejects before tools run. |
| A15 | Use the wrong OIDC audience | Remote MCP rejects before tools run. |
| A16 | Omit a required OIDC scope | Remote MCP rejects before tools run. |
| A17 | Query from another tenant | Rejected without exposing source metadata/results. |
| A18 | Reference a denied table | Query and metadata paths agree on denial. |
| A19 | Reference a denied column in projection/filter/join/order | All supported references are checked before execution. |
| A20 | Submit writes or disallowed side-effecting constructs | Read-only validation rejects; engine protection follows its documented connector scope. |
| A21 | Submit malformed SQL | Stable sanitized error; no DSN/internal credential detail. |
| A22 | Lose a source or use a bad source credential | Actionable safe error; other sources/identities remain isolated. |
| A23 | Cancel or time out a query | Documented cancellation/timeout, followed by usable connections and released capacity. |
| A24 | Review successful, denied and failed accesses | Identity/request correlation is available under the current audit contract, without tokens or result rows. |

REST/MCP consistency means equivalent policy and result behavior for applicable tasks. It does not mean a process-local REST cursor can be continued by a different MCP process. The expiry case should use controlled test time/configuration rather than require a long wall-clock wait.

The existing [real-source validation](../validation/real-source-matrix.md) ends at the in-process query service. Keep it as lower-level evidence while extending public transport, identity and audit coverage. Reuse existing driver, protocol and security tests.

## Shared-operation acceptance

The #365 ADR and delivered #409 routing implementation define REST/MCP routing keys, owner identity/process generation, caller/token binding, replay/concurrent fetch, expiry, cancellation, revocation, drain, rolling updates and owner loss. The [routing and drain guide](../replica-routing.md) documents current behavior; trusted destinations cannot be selected arbitrarily by clients.

The delivered two-replica verification matrix includes two tenants, token rotation, concurrent requests, drain and owner death; the original candidate evidence is retained in [#412](https://github.com/RhinoLink-Tech/data-fabric/pull/412) and [#416](https://github.com/RhinoLink-Tech/data-fabric/issues/416). Lost owners must produce an explicit stable failure rather than implicitly rerunning SQL. Shared metadata is not live-cursor failover. Cluster-wide precise tenant quotas and transparent cursor recovery require their own design if later needed.

[#413](https://github.com/RhinoLink-Tech/data-fabric/issues/413) accounts for cumulative returned rows, encoded bytes and total deadline across every batch. Define oversized rows, optional/helper COUNT work, cancellation and partial-result status. Reuse current admission controls; result budgets do not imply bounded scans or a universal cloud-spend limit.

Policy work in #99 uses immutable versions in the existing metadata store. Each request pins one valid policy snapshot. Compare-and-swap publication rejects stale writers; invalid updates retain the last valid version; rollback publishes a new revision. Replicas report freshness, converge within the documented three-second window, and fail closed after three seconds without synchronization. Publication outcome is committed atomically with activation; general access-audit delivery keeps its existing durability limits.

## Candidate delivery evidence

Fix the candidate commit, image, source versions, workload and configuration. A full validation result from an older commit cannot establish candidate readiness. Reuse and extend the [CI guide](../ci-workflow.md), existing upgrade/restore rehearsal and deployment material.

Repeat clean setup, source reuse by a second application, upgrade, credential-key backup/restore and bounded query draining. Preserve restricted and experimental support statuses. Choose a release version only after reviewing candidate changes and compatibility.

Real-model evaluation is active planned work in [#456](https://github.com/RhinoLink-Tech/data-fabric/issues/456), with scope from [#454](https://github.com/RhinoLink-Tech/data-fabric/issues/454) and relevant result oracles from [#455](https://github.com/RhinoLink-Tech/data-fabric/issues/455). Prepare the corpus now and execute only with configured credentials and an approved budget. Pin model/version, prompt/Skill/context, fixtures, permissions, retries and tool/token/time/cost limits; report correctness and intervention separately from deterministic gateway checks. The selected existing-workflow/direct-driver/source-specific MCP and optional DBHub/Toolbox comparisons must use equivalent task/data/permission objectives, record versions and show unsupported differences honestly.

## Active external evaluation and historical work

The earlier indefinite real-team deferral is superseded by [#454](https://github.com/RhinoLink-Tech/data-fabric/issues/454), [#456](https://github.com/RhinoLink-Tech/data-fabric/issues/456), [#458](https://github.com/RhinoLink-Tech/data-fabric/issues/458) and [#459](https://github.com/RhinoLink-Tech/data-fabric/issues/459). Owners arrange scope, recruitment, corpus and baseline preparation alongside delivery. Actual team access still requires a scheduled approved environment; model execution requires approved provider access/budget. Follow the [M4 pilot evidence protocol](../validation/real-team-pilot-evidence.md) for the real attempt and agreed sustained-use window, including first integration, correctness, permission outcomes, independent second-app reuse, interventions and support effort. Record observed continue/change/stop outcomes, including negative evidence from actual attempts; interviews, protocols and booked future sessions alone do not establish adoption. Current supported topology can be evaluated without M6 refactors.

[#175's source-management UI](../data-source-management-ui.md) is delivered as an engineering reference in [PR #430](https://github.com/RhinoLink-Tech/data-fabric/pull/430); its operator-effort benefit still needs M4 evidence. #73 masking and #83 deployment/residency work remain gated on concrete observed needs. The [Trino 484 integration profile](../validation/trino-integration.md) is complete within its documented boundary; broader engine coverage remains demand-triggered. Existing connectors and their tests remain available under their current support contracts.

Broad Phase 2–5 issues are retired or narrowed under [#402](https://github.com/RhinoLink-Tech/data-fabric/issues/402). Their closure as `not_planned` records direction and ownership; it does not assert delivery of their original compute, ML, data-mesh or industry-platform goals. The #37 real-engine verification slice was completed for the bounded Trino 484 profile under [#179](https://github.com/RhinoLink-Tech/data-fabric/issues/179) and [PR #423](https://github.com/RhinoLink-Tech/data-fabric/pull/423); broader compatibility remains demand-triggered. #80 leaves bounded deployment work in #81, and #96 leaves only the gateway continuation slice in #365.
