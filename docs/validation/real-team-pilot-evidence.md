# Real-Team Pilot Evidence Protocol

Use this protocol only after the maintainer schedules a real-team pilot. It captures whether Data Fabric improves one real task and source profile against the team's current approach. The pilot is separate from synthetic engineering acceptance and does not establish production readiness or general product fit.

## Guardrails

- Use an environment and data the team has approved for the pilot. Keep all Data Fabric query operations read-only; do not include writes unless a separate supported write path exists and is explicitly authorized.
- Never commit credentials, raw records, customer names, tenant identifiers, or unredacted prompts. Keep detailed logs in the team's approved storage and link only to a redacted artifact or opaque reference.
- Record the source, model, deployment, and permission boundaries. Do not convert a single pilot into a general connector, compliance, or production claim.
- Pause immediately if the run exposes data outside the approved identity or scope, performs an unintended write, or produces an unexplained authorization result.

## Prepare before access

1. Name the pilot owner and operator, record the approval and data-handling location, and choose one representative task with the person who will judge its result.
2. Write the expected result, allowed and denied information, acceptance rule, and any required human review before the first run. Use an opaque task ID if the task description is sensitive.
3. Record the team's current workflow for the same task, source, data slice, and user role. Include active operator time, elapsed time, manual steps, correctness review, and ongoing support.
4. Freeze the comparison profile: source type and version, relevant schema objects, identity and policy, gateway source commit and image ID, configuration, and model/prompt versions when a model is used.
5. Agree in advance on what would justify continuing, expanding, or stopping. Do not set thresholds after seeing the result.
6. Confirm how results and logs will be redacted, retained, and removed. Do not copy pilot data into this repository.

## Run the comparison

1. Run the task through the existing workflow and record the baseline. Keep the task, data slice, identity, expected outcome, and evaluation method.
2. Configure Data Fabric for the same source profile and permission boundary. Measure active integration effort separately from waiting time; record failed attempts and operator interventions.
3. Run the same task through Data Fabric. Keep model and prompt settings fixed when applicable. Have the task owner assess correctness against the prewritten acceptance rule.
4. Check one expected allowed operation and the relevant denied access boundary. Record expected and observed outcomes. Any unexpected allow is a stop condition.
5. If second-application reuse is part of the task, use an independent application identity and measure setup effort, repeated configuration, and whether the registered source and policy can be reused as intended.
6. Record manual corrections, retries, tool calls, fallback steps, failures, and support time. Keep unsupported behavior explicit; do not count it as a pass.
7. Review results with the task owner and operator. Decide whether to continue, change the profile, or stop, using the thresholds agreed before the run.

## Measures

| Measure | Record | Compare |
| --- | --- | --- |
| First integration effort | Active person-minutes from approved access to the first correct, authorized result; list setup and troubleshooting steps | Existing workflow |
| Task correctness | Expected outcome, observed outcome, reviewer, evaluation rule, and any human correction | Same task and data slice in the baseline |
| Authorization | Expected allow/deny cases, identity, policy version, observed result, and any unexpected allow | Same user role and data boundary |
| Second-application reuse | Setup time, repeated configuration, source and policy reuse, and any extra operator actions | Baseline reuse procedure |
| Manual intervention | Retries, prompt or query edits, manual mapping, escalation, and fallback work | Existing workflow |
| Ongoing support effort | Operator time, recurring failures, credential or policy maintenance, and unresolved gaps over the agreed observation period | Existing workflow |
| Environment | Source/server versions, candidate commit and image ID, configuration profile, model/prompt version if used | Exact profile used by both paths |

Treat correctness, authorization, effort, and production readiness as separate outcomes. A faster run with incorrect or unauthorized results is not a success. One task can show whether this team has a useful fit; it cannot establish broad willingness to pay or performance across other teams.

## Report template

Copy this section into a new report after the pilot has been scheduled. Replace placeholders with redacted values; leave unavailable fields marked as not collected.

### Pilot identity and approval

- Pilot ID:
- Team reference (pseudonymous):
- Pilot owner and operator roles:
- Approval reference and date:
- Data-handling and retention location:
- Status: planned / running / completed / stopped

### Task and baseline

- Task ID and business purpose:
- Source profile and data slice reference:
- Expected result and acceptance rule:
- Expected allowed operations:
- Expected denied operations:
- Existing workflow:
- Baseline active effort and elapsed time:
- Baseline correctness review and outcome:
- Pre-agreed continue / expand / stop criteria:

### Data Fabric candidate

- Source type and exact server version:
- Relevant catalog/schema/table identifiers (redacted as needed):
- Gateway commit SHA and image ID:
- Deployment and configuration profile:
- Identity and policy version:
- Model, prompt, and client versions (if used):
- First integration active effort and elapsed time:
- Second application identity and reuse effort (if applicable):

### Outcomes

| Measure | Expected | Observed | Evidence reference |
| --- | --- | --- | --- |
| Task correctness | | | |
| Authorized access | | | |
| Denied access | | | |
| Unexpected allows | 0 | | |
| Second-application reuse | | | |
| Manual interventions and retries | | | |
| Ongoing support effort | | | |

- Unsupported cases and limitations:
- Failures, fallback, and human corrections:
- Redacted evidence references:
- Task owner assessment:
- Operator assessment:
- Decision: continue / expand / change profile / stop
- Reason against the pre-agreed criteria:
- Follow-up issues and owner:
- Claims this report does not support:

Do not attach raw prompts, customer records, credentials, or unredacted logs to the repository. Store any detailed evidence under the pilot owner's approved retention policy and reference it from the redacted report only.
