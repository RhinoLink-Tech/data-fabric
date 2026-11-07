# Warehouse batch route: candidate task profile

## Status and scope

This report records a redacted business-task candidate for the Data Fabric pilot. The business rule is based on a maintainer-described route carry-over problem. No production source, record, credential, application identifier, or unredacted prompt was accessed or copied.

A separate PostgreSQL 17 profile is represented by the synthetic files in [warehouse-batch-route](../../../examples/warehouse-batch-route/README.md). The profile has not been connected to the Data Fabric gateway in this report. It is not a completed pilot, customer-value result, production-readiness result, or residency claim.

## Candidate task

- Task ID: `WH-ROUTE-RESET-01`
- Business purpose: determine the route for a newly queued batch before that batch reports its own pallet phase.
- Known process terms: one task has a material phase and an empty-container return phase; both phases are associated with a batch number.
- Expected result for the bounded initial-route case: origin `WAIT`, destination `FULL_PALLET`.
- Failure to prevent: using a successful empty-phase event from an earlier batch as the origin of the new batch.
- Synthetic data slice: previous batch `9001` has a completed empty phase; new batch `9002` is pending and has no phase event.
- Model and prompt: not used; this is a deterministic SQL reference. No real-model comparison was run.
- Baseline workflow, owner review, active effort, and elapsed time: not collected.
- Source type/version, actual schema identity, row policy, and approval reference: not collected. The synthetic profile is not a substitute.

## Proposed context asset

The draft [asset v1](../../../examples/warehouse-batch-route/context-v1.yaml) records its task scope, terms, join relationship, required columns, permission boundary, and invalidation rule. The asset is a review artifact only; Data Fabric does not load or enforce it at runtime yet.

For a live pilot, the asset must be approved by the task owner, bound to the actual source and schema identity, and executed through the gateway using the approved read-only caller. Every execution must still pass current Data Fabric authorization and read-only SQL validation. A source-side view or role is required if different callers may see different batch rows.

## Issue-gate triage

| Issue | Trigger status | Evidence and remaining work |
|---|---|---|
| #67 business context | Candidate identified; trigger not yet demonstrated in Data Fabric. | A real route carry-over task is described, and the expected rule is explicit. The approved live source/schema, evidence of correct schema resolution, task-owner confirmation, and a Data Fabric replay are not collected. Keep `status: needs-evidence`. |
| #68 schema drift | Not met. | The schema and asset are synthetic and not adopted by a pilot team. No before/after live schema change or stale-schema failure has been recorded. Keep `status: needs-evidence`. |
| #73 result masking | Not met. | No approved identity, sensitive-field category, or requirement that column denial/restricted views cannot satisfy has been supplied. Keep `status: needs-evidence`. |

## Next pilot actions

Before using a live source, the pilot owner must identify a separate approved source and version, provide only the relevant redacted schema/view definition, choose the operating identity and source-side row boundary, and record where redacted results and detailed logs will be retained. Freeze the existing workflow and acceptance rule before the first comparison. If a model is used, pin its model and prompt versions.

Do not copy raw rows, credentials, tenant identifiers, or unredacted prompts into this repository. This report does not claim that any remaining issue is complete.
