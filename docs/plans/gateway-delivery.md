# Gateway Delivery Plan

**Reviewed: 2026-11-07**

This plan sequences the engineering work behind the [roadmap](../roadmap.md). M0–M3 delivered the public-access and shared-operation foundation. The active work is the M5–M7 productization program in [#439](https://github.com/RhinoLink-Tech/data-fabric/issues/439). Real-team value validation remains M4 and is separate from synthetic engineering acceptance.

## Productization program

**Status: proposed implementation; no new runtime capability is delivered by this plan.**

The next increment is governed by four outcomes: **simple installation, easy first use, fast deployment, and low resource use**. New capability work must improve one of these outcomes and preserve the existing access-control and query contracts. First use means an operator can initialize the instance, connect one source, execute an authorized query, and reuse it from a second application.

### Baseline and intended product profiles

The reviewed baseline is `07d3c834b4cd9abe1e40ec942125912623398a2e`. Production metadata currently uses MySQL; REST and MCP run in separate processes; published v2.0.0 artifacts contain source rather than a ready-to-run image; the UI requires a JWT supplied by the operator. All connector families are compiled into the executable. SQL connections are already opened lazily, but support-catalog construction invokes driver factories. These findings justify the tasks below; they do not mean all sources connect at startup.

The [2026-11-01 footprint report](../performance/deployment-footprint.md) records a 119.5 MiB executable, a 236.1 MiB uncompressed image and 83.61 MiB idle PID RSS. Its original scope excludes the external metadata database and whole-container parser-child memory. It is historical evidence, not the budget measurement for this program.

| Profile | Intended deployment | Capability boundary |
| --- | --- | --- |
| Standalone target | One application container, one persistent volume, shared REST/remote-MCP runtime, verified local metadata. | Single node. Local metadata is proposed in [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444); it must not be advertised before implementation and acceptance. Business sources remain external. |
| Team | Shared MySQL metadata and documented enterprise authentication, using the same gateway contracts. | Existing optional multi-replica routing retains owner-bound process-local cursors and explicit owner-loss behavior. |
| Core / Full packages | Core starts with the verified PostgreSQL/MySQL/MariaDB family; Full preserves the broader connector distribution. | Compile-time connector inclusion and runtime enablement are separate from versioned support evidence. The default changes only after measured acceptance. |

Combined REST/MCP is opt-in until accepted, and current single-transport and stdio modes remain supported. Production authentication requirements remain enforced; a guided setup must not introduce a permanent default account or a bypass.

### Work and dependency gates

| Milestone | Issue | Start after |
| --- | --- | --- |
| [M5 Simple deployment](https://github.com/RhinoLink-Tech/data-fabric/milestone/11) | [#440](https://github.com/RhinoLink-Tech/data-fabric/issues/440) Reconcile README-linked guides with current startup and authentication behavior | Ready to claim |
| [M5 Simple deployment](https://github.com/RhinoLink-Tech/data-fabric/milestone/11) | [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441) Measure the complete deployment and first-query resource baseline | Ready to claim |
| [M5 Simple deployment](https://github.com/RhinoLink-Tech/data-fabric/milestone/11) | [#442](https://github.com/RhinoLink-Tech/data-fabric/issues/442) Publish runnable version-pinned images and offline artifacts | [#440](https://github.com/RhinoLink-Tech/data-fabric/issues/440), [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441) |
| [M5 Simple deployment](https://github.com/RhinoLink-Tech/data-fabric/milestone/11) | [#443](https://github.com/RhinoLink-Tech/data-fabric/issues/443) Share one runtime across REST and remote MCP transports | [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441) |
| [M5 Simple deployment](https://github.com/RhinoLink-Tech/data-fabric/milestone/11) | [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444) Add a verified single-node metadata backend | [#443](https://github.com/RhinoLink-Tech/data-fabric/issues/443) |
| [M5 Simple deployment](https://github.com/RhinoLink-Tech/data-fabric/milestone/11) | [#445](https://github.com/RhinoLink-Tech/data-fabric/issues/445) Add idempotent initialization and actionable diagnostics | [#443](https://github.com/RhinoLink-Tech/data-fabric/issues/443), [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444) |
| [M6 Guided use and on-demand connectors](https://github.com/RhinoLink-Tech/data-fabric/milestone/12) | [#446](https://github.com/RhinoLink-Tech/data-fabric/issues/446) Guide first query and second-application reuse | [#440](https://github.com/RhinoLink-Tech/data-fabric/issues/440), [#445](https://github.com/RhinoLink-Tech/data-fabric/issues/445), [#443](https://github.com/RhinoLink-Tech/data-fabric/issues/443) |
| [M6 Guided use and on-demand connectors](https://github.com/RhinoLink-Tech/data-fabric/milestone/12) | [#447](https://github.com/RhinoLink-Tech/data-fabric/issues/447) Separate connector descriptions from lazy resource initialization | [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441) |
| [M6 Guided use and on-demand connectors](https://github.com/RhinoLink-Tech/data-fabric/milestone/12) | [#448](https://github.com/RhinoLink-Tech/data-fabric/issues/448) Ship selectable Core and Full connector distributions | [#447](https://github.com/RhinoLink-Tech/data-fabric/issues/447), [#442](https://github.com/RhinoLink-Tech/data-fabric/issues/442), [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441) |
| [M7 Efficient and recoverable operation](https://github.com/RhinoLink-Tech/data-fabric/milestone/13) | [#449](https://github.com/RhinoLink-Tech/data-fabric/issues/449) Reuse bounded parser workers without weakening authorization | [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441), [#443](https://github.com/RhinoLink-Tech/data-fabric/issues/443) |
| [M7 Efficient and recoverable operation](https://github.com/RhinoLink-Tech/data-fabric/milestone/13) | [#450](https://github.com/RhinoLink-Tech/data-fabric/issues/450) Make exact totals optional for faster first pages | [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441), [#443](https://github.com/RhinoLink-Tech/data-fabric/issues/443) |
| [M7 Efficient and recoverable operation](https://github.com/RhinoLink-Tech/data-fabric/milestone/13) | [#451](https://github.com/RhinoLink-Tech/data-fabric/issues/451) Bound aggregate connection client and file-query usage | [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441), [#443](https://github.com/RhinoLink-Tech/data-fabric/issues/443), [#447](https://github.com/RhinoLink-Tech/data-fabric/issues/447) |
| [M7 Efficient and recoverable operation](https://github.com/RhinoLink-Tech/data-fabric/milestone/13) | [#452](https://github.com/RhinoLink-Tech/data-fabric/issues/452) Productize metadata key backup restore and upgrade checks | [#444](https://github.com/RhinoLink-Tech/data-fabric/issues/444), [#445](https://github.com/RhinoLink-Tech/data-fabric/issues/445), [#442](https://github.com/RhinoLink-Tech/data-fabric/issues/442) |

The initial ready work is [#440](https://github.com/RhinoLink-Tech/data-fabric/issues/440) and [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441). The first critical path is documentation/baseline → shared runtime → local metadata → initialization → guided first query and second-application reuse. Runnable artifacts and connector resource/package work can proceed alongside that path after their own gates pass. Aggregate limits, parser/count changes and recovery use the resulting runtime and candidate evidence. Keep at most two major implementations active; prioritize measured bottlenecks and the next usable end-to-end increment.

M5 exits with a clean persistent installation from a pinned runnable artifact, an authorized first task and measured restart behavior. M6 exits with an independent guided-use rehearsal, resource-free discovery of unused connectors and verified Core/Full packages. M7 exits with the reviewed whole-container budgets, bounded failure behavior and fresh-instance restore/upgrade evidence. Link these records from [#439](https://github.com/RhinoLink-Tech/data-fabric/issues/439) before closing the corresponding milestone.

### Provisional acceptance budgets

These numbers are candidate targets, not delivered guarantees. [#441](https://github.com/RhinoLink-Tech/data-fabric/issues/441) measures the current profile and delivers a reusable harness, then freezes or explicitly revises each budget with a recorded explanation. Each downstream implementation reruns the harness for its candidate; future profiles are not prerequisites for closing the baseline issue. A missed target requires remediation or a reviewed scope/budget decision; it must not disappear from the record.

| Metric | Candidate target | Measurement boundary |
| --- | --- | --- |
| Standalone deployment | One application container and one persistent data volume; no external metadata database required. | Business source systems remain external; demo-source containers are reported separately. |
| First authorized query | Candidate target: at most 5 minutes for an independent operator. | A reachable source and its approved connection details are supplied; report image download separately. |
| Start to readiness | Candidate target: at most 3 seconds. | Cached image, standalone profile, empty metadata directory; also report restart and migration cases. |
| Core image size | Candidate target: at most 150 MiB uncompressed. | Record exact connector set, architecture, artifact digest, and embedded SQL runtime. |
| Warm idle memory | Candidate target: at most 128 MiB for the whole standalone application container. | After the first task and five idle minutes; include parser children and report cgroup memory, not only Go PID RSS. |
| Light-load peak memory | Candidate target: at most 256 MiB. | Four concurrent fixed small PostgreSQL/MySQL queries with pinned synthetic fixtures; report object/file stress separately. |
| Recovery | A verified supported upgrade, consistent backup and fresh-instance restore preserve sources, encrypted credentials, policy revisions and required audit records. | Missing keys and incompatible schemas fail clearly; no raw business records or credentials enter reports. |

Measure the actual REST/MCP authentication, SQL validation, source execution, batching and cleanup path. Record exact commit, image digest, connector set, source versions, hardware, configuration and raw samples. Include all application processes in container memory, and report external metadata/identity/demo-source cost separately and in the full-stack total. Distinguish download, initialization, readiness, first query, warm idle and concurrent load. Keep file/object stress separate from the small SQL workload so its limits remain explicit.

### Acceptance and collaboration rules

- Before coding, assign the issue and post a claim with chat, branch, isolated Data Fabric worktree and intended files. Check overlapping claims and open PRs; a claim on this tracker coordinates the plan and does not reserve every implementation task.
- Each PR must explain which of the four outcomes improves and link observable acceptance evidence. Runtime/package/UX/operations tasks require implemented behavior; a design or reference document alone does not close them.
- Reuse existing owner/tenant checks, table/column policy and three-second revocation, read-only SQL parsing, parameter binding, redacted audit, per-query result budgets and continuation contracts. Preserve the known durability and process-local limits until separately addressed.
- Catalog discovery must not create clients, connect to sources or start workers. Lazy resource initialization reduces runtime cost; Core/Full import boundaries reduce artifact size. Runtime enablement flags alone do not shrink the binary.
- Use bounded lazy parser workers and optional totals only through their issues. Authorization decisions and query results are not parser-cache entries; exact-count compatibility and fail-closed SQL behavior remain explicit.
- Use isolated synthetic fixtures in representative business flows. Do not access or modify Atlas code, repositories, databases or services. No credentials or raw business records belong in issues, examples or reports.
- Update [#439](https://github.com/RhinoLink-Tech/data-fabric/issues/439) and this roadmap after each accepted increment. Planning alone closes none of the implementation issues. Real-team/customer/model-value evidence remains separate; #67/#68 are bounded agent guidance and #73 delivered scope only, with runtime masking still unimplemented.

## M0–M3 foundation record

These completed tasks provide the reference contracts for M5–M7. They are not a new implementation queue. The acceptance sections below retain the delivered baseline and its limits.

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

The #365 ADR must specify routing keys for REST and MCP protocol/tool requests, owner identity and process generation, caller/token binding, replay/concurrent fetch, expiry, cancellation, revocation, drain, rolling updates and owner loss. Trusted routing destinations cannot be selected arbitrarily by clients.

The implementation verification matrix includes two replicas, two tenants, token rotation, concurrent requests, drain and owner death. Lost owners must produce an explicit stable failure rather than implicitly rerunning SQL. Shared metadata is not live-cursor failover. Cluster-wide precise tenant quotas and transparent cursor recovery require their own design if later needed.

[#413](https://github.com/RhinoLink-Tech/data-fabric/issues/413) accounts for cumulative returned rows, encoded bytes and total deadline across every batch. Define oversized rows, optional/helper COUNT work, cancellation and partial-result status. Reuse current admission controls; result budgets do not imply bounded scans or a universal cloud-spend limit.

Policy work in #99 uses immutable versions in the existing metadata store. Each request pins one valid policy snapshot. Compare-and-swap publication rejects stale writers; invalid updates retain the last valid version; rollback publishes a new revision. Replicas report freshness, converge within the documented three-second window, and fail closed after three seconds without synchronization. Publication outcome is committed atomically with activation; general access-audit delivery keeps its existing durability limits.

## Candidate delivery evidence

Fix the candidate commit, image, source versions, workload and configuration. A full validation result from an older commit cannot establish candidate readiness. Reuse and extend the [CI guide](../ci-workflow.md), existing upgrade/restore rehearsal and deployment material.

Repeat clean setup, source reuse by a second application, upgrade, credential-key backup/restore and bounded query draining. Preserve restricted and experimental support statuses. Choose a release version only after reviewing candidate changes and compatibility.

If real-model evaluation is run, pin model/version, prompt, fixtures, retries and an explicitly supplied execution budget. Report correctness, tool calls and intervention separately from deterministic gateway results. Optional DBHub/Toolbox comparisons use the same data, tasks and permission objective, record versions, and show unsupported differences honestly.

## Deferred and historical work

Real-team contact and environment access are deferred. When resumed, use the [M4 pilot evidence protocol](../validation/real-team-pilot-evidence.md) to compare the existing integration approach with Data Fabric on first deployment, second-application reuse, task correctness, permission outcomes, and ongoing support effort before expanding the support profile.

[#175's source-management UI](../data-source-management-ui.md) is delivered as an engineering reference in [PR #430](https://github.com/RhinoLink-Tech/data-fabric/pull/430); its operator-effort benefit still needs M4 evidence. #73 masking and #83 deployment/residency work remain gated on concrete observed needs. The [Trino 484 integration profile](../validation/trino-integration.md) is complete within its documented boundary; broader engine coverage remains demand-triggered. Existing connectors and their tests remain available under their current support contracts.

Broad Phase 2–5 issues are retired or narrowed under [#402](https://github.com/RhinoLink-Tech/data-fabric/issues/402). Their closure as `not_planned` records direction and ownership; it does not assert delivery of their original compute, ML, data-mesh or industry-platform goals. The #37 real-engine verification slice was completed for the bounded Trino 484 profile under [#179](https://github.com/RhinoLink-Tech/data-fabric/issues/179) and [PR #423](https://github.com/RhinoLink-Tech/data-fabric/pull/423); broader compatibility remains demand-triggered. #80 leaves bounded deployment work in #81, and #96 leaves only the gateway continuation slice in #365.
