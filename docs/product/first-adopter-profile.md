# First Adopter and Supported Profile

**Status:** decision draft, reviewed 2026-11-08. The adopter, workflow, value and commercial path below are hypotheses. No external team has validated them. Accountable repository maintainer: [@LuckyCaptain-go](https://github.com/LuckyCaptain-go). Maintainer approval of this specific profile and approval to contact participants are still pending.

## Product statement

**English:** Data Fabric is a self-hosted, read-only data access gateway for teams that need to govern a data source once and reuse it from more than one internal application through REST and MCP.

**中文：** Data Fabric 是面向团队的自托管只读数据访问网关，让团队统一登记和治理数据源，并通过 REST 与 MCP 在多个内部应用中复用。

This is a positioning hypothesis, not a claim that every connector, client or SQL dialect is production supported. The intended value is less repeated connection, credential, permission and audit setup across applications. The value must be measured against each team's current workflow.

## First-adopter hypothesis

| Role | Hypothesis to validate |
| --- | --- |
| First team | A platform or data engineering team that operates at least two internal AI-enabled applications and owns access to a shared relational source. |
| Operator | The platform/data engineer who provisions the gateway, source credentials, tenant mapping, policy and upgrades. |
| Daily user | Internal application developers or operations analysts who need bounded, read-only answers from that source. |
| Security/data approver | The source owner or security lead who approves the allowed tables/columns and verifies denied access. |
| Budget owner | Likely the platform/AI engineering lead; identity and willingness to pay are unknown. |

This profile is a fit only when repeated per-application source configuration, access review or audit work is a real cost. A single application with one database may be better served by its existing driver. No industry, team size, cloud, model provider or procurement pattern is asserted.

## First repeatable task

**Task hypothesis:** An internal support or operations assistant retrieves the current status of approved orders from one shared relational source. The operator registers that source and its read-only scope once. One application requests the result through REST; a second application requests the same task through MCP under its own approved application identity and tenant. The second application must reuse the registered source and policy without receiving the database credential.

The current deterministic rehearsal is the documented three-row `orders` fixture and query `SELECT customer, amount FROM orders ORDER BY amount DESC`. This proves only an engineering flow. For an external rehearsal, the team's owner must select an approved task and data slice; use synthetic or explicitly approved, minimized data and store no raw customer records in this repository.

The first product profile deliberately excludes cross-source joins, write access, masking, high-availability failover, model-quality claims and arbitrary SQL compatibility. Federation and other connectors remain demand-triggered.

## Recommended technical evaluation profile

| Dimension | Proposed first profile | Current evidence and limitation |
| --- | --- | --- |
| Gateway release | Use a recorded main SHA for reference work. Label the first packaged artifact **Preview** after the current-profile #440/#441/#445/#451 engineering gates and the exact-SHA #442 publish/clean-host checks pass. The #451 admission and resource-return evidence is now recorded, but it does not establish a universal memory envelope or production-capacity guarantee. Preview makes no external-fit or SLA claim. Promote to **Stable** only after candidate-matched recovery/support/upgrade gates and the #454 external-fit rule (at least two of three qualified team evaluations meet the continue criteria) pass. | The latest published GitHub Release remains source v2.0.0. The v2.1.0-preview.1 tag and linux/amd64 GHCR image are staged at source SHA `570c2975fedbc886a37003a8b64b68caa699678c` and digest `sha256:7d47073fe806879968ed3c66f9d3b2b7b7a2020f419738c2cbb1b89d58f1ebbf`, but the package remains private and its Release is a draft. Anonymous install and independent first-use evidence remain pending. The default MySQL metadata and sample-source images are pinned to MySQL 8.4.13 digests in the Compose profile. |
| Metadata | MySQL 8.4.13; the default Compose profile pins the metadata and sample-source images by digest. | The configured immutable reference is mysql:8.4.13@sha256:a0225d1ea6631c8bc2385dc4c2faba17bbf1834ecf369e1659a9a382bd8d5dd7. Recheck the exact platform manifest at publication. The optional PostgreSQL profile is separate and is not part of this Preview. |
| First query source | MySQL 8.4.13, one source, one read-only database user with only the required `SELECT` privilege. | The support catalog records a live contract for MySQL 8.4.13. It also records MySQL 8.0.46; no other MySQL version is implied. |
| Optional second source profile | PostgreSQL 17.11 or 18.6, one source at a time, with a read-only role. | Both exact versions appear in the source support catalog. The current PostgreSQL Compose override uses the moving `postgres:18` tag and must be pinned before a release promise. |
| Operations | One Compose deployment, one API process and one MCP process sharing MySQL metadata; no replicas. API remains loopback-bound; MCP uses Streamable HTTP over TLS. | Owner-bound query cursors are process-local and are lost on owner restart. Routing does not provide failover. Persist the credential keyring across restart and backup. |
| Capacity and returned results | Each API and MCP process independently allows 4 active query/stream reservations total and 2 per validated tenant. A query has a 64 MiB returned-result budget; the default SQL connection pool permits at most 10 open connections per source and process. | Defaults match the current #441 four-way/two-tenant synthetic profile and were wired by PR #476. Limits are process-local and do not create a cluster-wide tenant quota. The three-row fixture did not test results near 64 MiB, and no CPU/memory cgroup envelope has been measured. Treat the returned-result budget as an output bound, not a process-heap or source-scan bound; see [observability guidance](../observability.md#current-query-and-cursor-capacity). |
| Identity | REST management uses the configured HS256 bearer token. Remote MCP uses an externally issued OIDC-signed JWT with configured issuer, audience and scope. Map both paths to the intended tenant/principal; do not use admin identity for normal query work. | The quickstart does not run an identity provider or mint the OIDC token. REST and MCP have distinct token setup. Opaque tokens and ID tokens are not accepted by remote MCP. |
| Client | One REST consumer and one MCP Streamable HTTP client. Use the documented Claude Code flow for the MCP rehearsal; record its exact version. | No model vendor is required for deterministic SQL acceptance. An actual AI task must separately record client, model and prompt versions. |
| Operations and support | Follow the versioned installation, upgrade, key backup and recovery procedure. Use GitHub Discussions for usage questions, issue forms for reproducible bugs, and private vulnerability reporting for security issues. | The project is community-maintained and publishes no response-time SLA. Current support and security policy do not promise older-release support. |
| SQL and source limits | Read-only operations in the listed connector contract; expect a documented SQL compatibility subset. | A registered connector is not a verified version. See [source support](../data-source-support.md), [read-only behavior](../readonly-access.md), [deployment guide](../mcp-deployment.md) and [known runtime limits](../roadmap.md#current-operating-limits). |

The package and image pin are implemented. #442 still requires a public digest/provenance check, a clean-host install from that digest, and first-adopter evidence before closure. Keep the support catalog as the authority for exact connector evidence.

## Alternatives on the same task

This is a comparison plan, not a claim that a comparative evaluation has already run.

| Path | Likely fit | Same-task setup to compare |
| --- | --- | --- |
| Existing workflow / direct driver in each app | One app, one source, or a team already satisfied with its own access and audit controls. It avoids another service. | Give both apps the same least-privilege database role. Measure repeated credential/configuration, query implementation, authorization, audit and operations effort separately for app 1 and app 2. |
| A source-specific MCP server or [DBHub v1.6.0](https://github.com/bytebase/dbhub/releases/tag/v1.6.0) (latest published release checked 2026-11-08) | A team that mainly needs one MCP connection. DBHub's [public README](https://github.com/bytebase/dbhub) describes multiple relational engines and configured databases, read-only mode, row limiting and query timeout. Its [current CLI documentation](https://github.com/bytebase/dbhub/blob/main/docs/config/command-line.mdx) describes optional HTTP bearer-token allowlists; configure them explicitly, and do not treat a shared token alone as per-user or tenant authorization. | Pin v1.6.0 and the image digest, use equivalent least-privilege credentials and task, and record where the chosen setup can or cannot meet the same identity/tenant, REST-plus-MCP, shared-policy and audit requirements. The local same-task comparison has not been run. |
| Data Fabric | Potential fit when the same governed source needs to be reused by REST and MCP clients with explicit ownership, tenant/policy checks and auditable access. | Use the exact profile above. Count both the centralized setup saved and the extra service, MySQL metadata, OIDC, key management and upgrade work introduced. |

Compare identical source version, data slice, task, expected result, allowed and denied cases, operator responsibility and observation window. Record active operator minutes, time to first correct result, per-app integration effort, second-app reuse, duplicate secret/policy work, manual interventions, policy outcomes and recurring support effort. If a gateway alternative cannot support an equivalent boundary, record the limitation instead of silently weakening the Data Fabric test.

## Proposed evaluation rules

These thresholds are recommendations for maintainer approval before any external evaluation. They are not measured results or an SLA.

**Task success for one evaluation:**

- The first operator reaches an authorized result in at most 30 active minutes from a clean supported profile; the second app reuses the same source and policy in at most 15 active minutes, without receiving or copying the source credential.
- Every expected row/result and ordering rule matches the pre-recorded baseline.
- Every predeclared unauthorized tenant, source, table/column or write attempt is denied. Any unexpected allow, leaked result or incorrect security attribution makes the evaluation fail regardless of speed.
- All configuration, versions, candidate SHA, artifact identity, interventions and restart/session-loss behavior are recorded.

**Continue the positioning beyond an initial three qualified team evaluations only if:**

- At least two of three teams report a recurring task that genuinely spans more than one application and want to continue using a shared gateway profile; and
- At least two teams complete second-application reuse without duplicating source credentials or policy setup, with zero unexpected authorization allows; and
- Median active operator effort across the two applications is at least 30% lower than the fastest equivalent alternative, or a budget owner identifies a concrete next procurement/paid-support step and the operator confirms that the saved effort is material.

The 30% threshold is a proposed decision rule, not a business fact. If a sample of three cannot be recruited, report the recruitment outcome and keep the decision inconclusive; do not infer demand from synthetic runs or interviews alone.

**Change the profile** if teams have a recurring need but do not need both REST and MCP, prioritize a different supported source/version, or cannot adopt the two-token/metadata deployment path. **Stop this positioning** if no qualified team demonstrates repeated cross-application governance effort after the agreed recruitment attempt, or if the product does not preserve the zero-unexpected-allow rule. Keep technical security defects as release blockers, not as ordinary product tradeoffs.

## Open-source adoption and commercial hypothesis

The first goal is repeatable open-source adoption: clear supported scope, a pinned runnable artifact, a first authorized query, second-app reuse, upgrade/recovery guidance and a credible community/security contact path. Do not add a paid-only feature gate or price claim before the task's value is observed.

**Commercial hypothesis:** if platform teams value the shared governance and maintenance outcome, the first paid option may be supported installation, upgrades/security response or managed operation. Test who owns that budget, the support work they want transferred, response expectations and recurring operating cost. No willingness-to-pay, buyer, price or sale is known today. Track maintainer support hours per adopter so any service model can be evaluated for sustainability.

## Decision and evidence log

| Decision | Current answer | Evidence / next gate |
| --- | --- | --- |
| First adopter | Platform/data team operating multiple internal applications over a governed relational source (hypothesis) | Maintainer approval, then external research through #454 |
| First task | One read-only order-status query reused across REST and MCP | Synthetic fixture documented in `docs/mcp-deployment.md`; replace only with an approved pilot task |
| Primary source | MySQL 8.4.13 | Exact driver contract in [source support](../data-source-support.md); pin metadata and source image digests in #442 |
| Product status | Next installable profile should be Preview, without a hard memory or production-capacity claim | #440 docs, #441 baseline, #442 artifact, #445 first-use diagnostics, #446 second-app reuse, #451 admission/resource evidence, #452 recovery |
| Architecture changes | No new storage backend or combined REST/MCP process yet | Consider #443/#444 only if measured onboarding, resource or operations evidence calls for it |
| Model/connector expansion | No model ranking or broad connector promise | #456 model evaluation and additional source versions require a defined task and owner evidence |
| External adoption | Not started; no participant evidence | Proposed recruitment action: maintainer to invite three qualified platform/data teams to a short task-baseline rehearsal after approving this profile. Accountable: @LuckyCaptain-go. Status: pending approval; no outreach has been made. |
| Commercialization | Unknown; paid support/managed operations is only a hypothesis | #458 adoption and #459 support/economics follow task evidence; no pricing decision |

### Evidence separation

- #441's local REST/MCP timing and synthetic acceptance are engineering reference evidence, not customer value, model quality, production capacity or adoption.
- The current source catalog marks exact end-to-end contracts; do not turn implementation-present or experimental entries into product promises.
- The published v2.0.0 release is a source release. A published, digest-pinned container/binary is not documented today.
- No external participant, customer outcome, support cost, budget commitment or sale is recorded in this document.

## Related work

- [#439 productization tracker](https://github.com/RhinoLink-Tech/data-fabric/issues/439)
- [#440 adoption documentation and roadmap](https://github.com/RhinoLink-Tech/data-fabric/issues/440)
- [#441 public REST/MCP engineering baseline](https://github.com/RhinoLink-Tech/data-fabric/issues/441)
- [#442 pinned runnable preview and upgrade path](https://github.com/RhinoLink-Tech/data-fabric/issues/442)
- [#445 first-use diagnostics](https://github.com/RhinoLink-Tech/data-fabric/issues/445), [#446 second-application reuse](https://github.com/RhinoLink-Tech/data-fabric/issues/446), [#451 capacity/resource evidence](https://github.com/RhinoLink-Tech/data-fabric/issues/451), [#452 recovery](https://github.com/RhinoLink-Tech/data-fabric/issues/452)
- [#456 model evaluation](https://github.com/RhinoLink-Tech/data-fabric/issues/456), [#458 external adoption](https://github.com/RhinoLink-Tech/data-fabric/issues/458), [#459 support and commercial economics](https://github.com/RhinoLink-Tech/data-fabric/issues/459)
