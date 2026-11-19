# Feishu Bitable read-only source: candidate evaluation

[中文](feishu-bitable-evaluation-zh.md)

Status: **design candidate, not a shipped connector**. Tracking: [#755](https://github.com/RhinoLink-Tech/data-fabric/issues/755). This is not an ELT or ingestion proposal. No customer request, real Feishu credentials, live API test, WorkBuddy acceptance or cross-source execution has been established for this candidate.

## One practical task

A synthetic sales-operations operator wants a weekly lead-status summary and the first two pages of leads needing follow-up. The illustrative baseline is manually filtering a Bitable view, counting statuses and copying the result. The permitted synthetic fields are `record_id`, `status`, and `created_at`; no names, phone numbers or notes. The business owner, actual view and filter, time zone, baseline time, and acceptance thresholds must be confirmed for a real team. This is an engineering hypothesis, not an observed customer workflow.

## Decision

Start with a small **direct, read-only HTTP adapter prototype** against a local fake provider. It avoids making Trino a prerequisite for a single-source task and tests identity, permission and pagination contracts first. It does not establish SQL or cross-source support. After a real task and authorized test environment exist, compare a real API run against the fixture and decide whether to proceed.

| Choice | Benefits | Cost and unresolved questions |
| --- | --- | --- |
| Direct adapter | Small deployment for one source; can reuse gateway identity, authorization, audit and budgets only after explicitly integrating and testing them. | Must implement source authentication, field mapping, bounded pagination and query semantics. Existing native API cursor reissues SQL with offsets and a count query; an opaque provider cursor cannot simply be substituted. A first proof should expose a bounded record-read contract, not pretend to implement general SQL. |
| Trino connector | A verified Trino connector could eventually make Bitable data available to an existing federation engine and enable joins under its tested constraints. | Trino 484's [connector list](https://trino.io/docs/current/connector.html) does not list a built-in Feishu Bitable connector; third-party availability has not been assessed. Building a plugin needs [metadata, splits and page source](https://trino.io/docs/current/develop/connectors.html), plugin deployment and a running engine. Policy, identity, freshness, limits, read-only behavior and cross-source joins still require independent acceptance. |

Authentication and consent are implementation gates for both choices: establish which Feishu identity reads which table/view and fields, obtain only the scopes needed by the selected API, revoke access cleanly, and keep secrets out of Skills, model messages, logs and results. [Feishu's permission guidance](https://open.feishu.cn/document/base-extensions/base-table-view-extension-development-guide) lists a read-only Bitable scope for its extension context; the exact server API permissions and user/app identity must be verified for the eventual implementation. Its [token refresh documentation](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/authentication-management/access-token/refresh-user-access-token-v3) describes a refresh flow; do not assume every deployment has refresh permission or that a user token persists indefinitely. No keys or tokens belong in the repository.

Feishu's [Bitable FAQ](https://open.feishu.cn/document/docs/bitable-v1/faq?lang=zh-CN) explains that advanced permissions affect returned data and empty fields may be omitted. An empty page is not sufficient evidence of an unrestricted table or of zero business records. A real test must establish the identity and permissions independently and report ambiguous visibility.

## Small proof and acceptance

1. A fake HTTP provider supplies a fixed synthetic table and opaque continuation tokens. Verify exact fields, two or more pages, empty values, invalid/expired continuation, a permission error, rate-limit response, timeout, cancellation and a changed record between pages. All responses and logs must be secret-free.
2. Bind continuation state to the caller, tenant, source, query and expiry; enforce explicit page, row, byte and duration limits. Report errors, truncation and partial results as such. Do not claim snapshot consistency when the source can change between pages. No write API is exposed.
3. Confirm a safe mapping for scalar fields and timestamps; reject unsupported or ambiguous values instead of silently losing data. The summary is valid only when all relevant pages are read within budget. Do not advertise SQL aggregates, predicate pushdown or joins until implemented and checked end to end through the existing authorization and audit pipeline.
4. Before live implementation, agree the real task and allowed table/fields, test identity and consent, source account owner, network egress, approved test data, and cleanup. Record the Feishu API, permissions and observed quota/reset behavior. Avoid guessing rate limits or plan pricing: the operator checks its actual Feishu plan and API documentation before committing to cost or freshness. On-demand reads can reflect later changes but have no verified staleness or snapshot guarantee.
5. For an actual MCP release, repeat permission-denial, source revocation, multi-page read and client-visible results using the shipping service and intended WorkBuddy version. Publish precise evidence and limitations before listing the source as supported.

The fake provider can run in-process without downloading container images. No real API or client acceptance is claimed by this evaluation. The linked external documentation may change; recheck the relevant version during implementation.
