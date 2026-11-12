# Independent first-use rehearsal protocol

Status: preparation only; no independent operator results have been recorded.

This protocol supports issue #446. It compares the documented first-use path for a versioned baseline and candidate artifact. It does not establish sustained team adoption (#458), real-model task quality (#456), production readiness, or customer value.

## Start gate

Begin only when:

- Baseline and candidate tags, source SHAs, image digests, platforms, and matching documentation are fixed. Use published artifacts, not a local build or source checkout.
- Both artifacts and required supporting images must be retrievable anonymously. The v2.1.0-preview.1 candidate is now public at digest `sha256:7d47073fe806879968ed3c66f9d3b2b7b7a2020f419738c2cbb1b89d58f1ebbf` ([release](https://github.com/RhinoLink-Tech/data-fabric/releases/tag/v2.1.0-preview.1)); anonymous retrieval and clean-runner acceptance passed. Confirm the baseline artifact and supporting images meet this gate before scheduling operators.
- Two operators who did not implement the guided path and are unfamiliar with repository internals are available. They receive the published instructions and same task brief, without a maintainer walkthrough.
- The isolated environment uses synthetic data or data explicitly approved for this rehearsal.

If any gate is missing, record the blocker and stop. A planned or partial run is not a pass.

## Control image downloads and the environment

Before timing, record the host OS and architecture, Docker Engine and Compose versions, identity-provider setup, source version, policy, and exact image references and digests for both artifacts.

Check which exact digests are already cached. Prepare a missing digest once, outside the timed run, only after transfer size and bandwidth are agreed. Do not resolve moving tags or trigger an unplanned pull. If a missing image would cause a large transfer, pause and arrange an approved pre-stage or offline transfer. Record bytes transferred and cache state separately from operator time.

Use the same host or equivalent host limits, identity provider, synthetic source rows, tenant mapping, and policy. Start each artifact in a fresh Compose project with fresh test volumes, then reset using the documented cleanup instructions. Never share bearer tokens between operators, clients, or applications.

Counterbalance learning order: operator A runs baseline then candidate; operator B runs candidate then baseline. Record every clarification or maintainer intervention.

## Run the same task

For each operator and artifact:

1. Start elapsed-time tracking when the operator receives the artifact identity, task brief, and documentation links. Follow the published deployment and first-use guide without a repository checkout.
2. Register the synthetic read-only source under the intended owner and tenant. Complete the first authorized query and compare it with the fixture oracle. Do not copy raw rows into the report.
3. Use an unauthorized-tenant principal to repeat discovery or query. The source and rows must remain unavailable. If restricted tables or columns are configured, confirm they are hidden or denied.
4. Connect a second application with its own OIDC principal mapped to the approved tenant. It must reuse the registered source and policy without receiving source credentials, then complete the same authorized task.
5. Exercise one documented safe failure, such as a missing or expired MCP token. Record the expected next action and any correlation ID exposed by the client. Do not use an administrator identity to bypass access failures.
6. Stop the clock after the allowed task, denial, and second-application check. Record active operator minutes, elapsed time, failures, retries, and maintainer intervention separately.
7. Clean up only the rehearsal Compose project and its test volumes. Keep detailed logs in approved storage and put only redacted references in the report.

Stop for any unexpected allow, unintended write, credential disclosure, or exposure outside the approved scope. Preserve only a redacted incident reference and follow the environment owner response process.

## Per-run record

Copy one record for each operator and artifact. Use aliases such as A and B. Do not record names, tokens, source passwords, raw records, or unredacted prompts.

### Run identity

- Operator alias and unfamiliarity with repository internals:
- Artifact role: baseline / candidate
- Release tag, source SHA, image digest, platform:
- Documentation revision:
- Host OS, architecture, Docker Engine, Compose:
- MCP client and version:
- Identity-provider and synthetic-source profile:
- Image cache state and bytes transferred before timing:
- Run order and UTC start/end:

### Outcomes

| Check | Expected | Observed | Pass / fail / unsupported |
| --- | --- | --- | --- |
| First authorized query and fixture correctness | | | |
| Unauthorized-tenant denial | | | |
| Restricted table or column denial, when configured | | | |
| Second application uses its own principal and reuses source policy | | | |
| Safe failure and next action | | | |
| Credentials remain undisclosed | | | |

- Active operator time:
- Total elapsed time:
- Failed attempts and retries:
- Maintainer interventions and minutes:
- Documentation deviation or missing prerequisite:
- Redacted evidence reference:
- Unsupported cases and limitations:

## Decision rule

Close #446 only after both independent operators complete baseline and candidate runs, allowed and denied outcomes match expectations, the second application uses its own identity, and failures and interventions are recorded. Fix or file repeated blockers before claiming the guided path is complete.

The Preview candidate is public, but publication and clean-runner acceptance do not complete #446. The independent rehearsal remains pending until two operators complete the protocol. Keep this synthetic rehearsal separate from real-team adoption, model quality, sustained use, and commercial evidence.
