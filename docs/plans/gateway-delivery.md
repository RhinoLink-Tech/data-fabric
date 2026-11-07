# Gateway Delivery Plan

**Reviewed: 2026-11-07**

This plan sequences the engineering work behind the [roadmap](../roadmap.md). Build repeatable public-interface evidence first, then improve shared operation and delivery. Real-team pilots remain deferred until the maintainer resumes them.

## Current work and dependencies

| Work | Tracking | Readiness and outcome |
| --- | --- | --- |
| Reconcile roadmap and backlog | [#402](https://github.com/RhinoLink-Tech/data-fabric/issues/402) | Complete the issue/milestone alignment and preserve retired scope as history. |
| Preserve pre-merge source evidence | [#404](https://github.com/RhinoLink-Tech/data-fabric/issues/404) | Follow #400/#401; use existing pinned reference contracts and conservative changed-path selection. |
| Reproduce heterogeneous access | [#81](https://github.com/RhinoLink-Tech/data-fabric/issues/81) | Extend #180/#186 and `deploy/mcp` with PostgreSQL and MySQL together, isolated test identities, setup checks and measurements. |
| Verify complete public tasks | [#403](https://github.com/RhinoLink-Tech/data-fabric/issues/403) | Depends on the reference environment. Extend existing tests through actual REST/MCP endpoints and record pass/fail/skip/unsupported evidence. |
| Design multi-replica continuation | [#365](https://github.com/RhinoLink-Tech/data-fabric/issues/365) | ADR and verification matrix only. Research may proceed alongside the reference work; use its public contracts. |
| Implement owner routing and drain | Create after #365 is accepted | Follow the chosen ADR using the public task harness. Keep live cursors with their owner. |
| Bound cumulative query results | [#413](https://github.com/RhinoLink-Tech/data-fabric/issues/413) | One logical query has cumulative rows, encoded-byte and overall-deadline limits across paginated, native API, and streaming paths; define COUNT and partial-result behavior. |
| Update and revoke policies | [#99](https://github.com/RhinoLink-Tech/data-fabric/issues/99) | Publish validated immutable versions through the shared metadata database with CAS, request-pinned snapshots, publisher/outcome history and a documented three-second revocation/fail-closed contract. See [ADR-004](../decisions/adr-004-versioned-table-policy-publication.md). |
| Add targeted task context | [#66](https://github.com/RhinoLink-Tech/data-fabric/issues/66), [#67](https://github.com/RhinoLink-Tech/data-fabric/issues/67), [#68](https://github.com/RhinoLink-Tech/data-fabric/issues/68) | Defined scope, no current implementation commitment. Start only from recorded schema-selection, business-context or schema-drift failures. |
| Candidate delivery evidence | [#416](https://github.com/RhinoLink-Tech/data-fabric/issues/416) | Completed against source SHA 53d0d662ac24273d8a5877dd4190188c843342c8; see the [candidate evidence runbook](../validation/post-m2-candidate-evidence.md) and [2026-11-07 evidence report](../validation/results/2026-11-07-post-m2-candidate.md). |
| Validate real-team value | Create when the maintainer resumes pilots | Measure actual integration/reuse/support effort and task outcomes. |

Do not create every future implementation issue at once. Keep at most two major implementation items in progress. #365 does not implement the selected design, and #99 does not reopen the identity/authorization/audit foundation in #217–#220.

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

Real-team contact and environment access are deferred. When resumed, measure the actual existing integration approach, first deployment, second-application reuse, correct task results, permission outcomes and ongoing support effort before expanding the support profile.

#175 UI, #73 masking and #83 deployment/residency evidence require observed needs. The [Trino 484 integration profile](../validation/trino-integration.md) is complete within its documented boundary; broader engine coverage remains demand-triggered. Existing connectors and their tests remain available under their current support contracts.

Broad Phase 2–5 issues are retired or narrowed under [#402](https://github.com/RhinoLink-Tech/data-fabric/issues/402). Their closure as `not_planned` records direction and ownership; it does not assert delivery of their original compute, ML, data-mesh or industry-platform goals. The #37 real-engine verification slice was completed for the bounded Trino 484 profile under [#179](https://github.com/RhinoLink-Tech/data-fabric/issues/179) and [PR #423](https://github.com/RhinoLink-Tech/data-fabric/pull/423); broader compatibility remains demand-triggered. #80 leaves bounded deployment work in #81, and #96 leaves only the gateway continuation slice in #365.
