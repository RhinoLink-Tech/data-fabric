# CI workflow

Pull requests and pushes to master run the required **Build and checks** status.
The unit and build job still runs formatting, static analysis, deterministic
protocol fixtures, package builds, SQL translation checks, and API and skill
validation. The final required status also waits for every selected live
contract. A selected job that fails, is cancelled, is skipped, or has no result
blocks that status. The required status name stays the same.

## Selecting live contracts before merge

The selection job uses the complete Git diff against the PR base or the
previous push commit. Detected renames and copies include both paths, and deletions
retain the deleted path. Missing or unreadable commit ranges select all known
jobs. A documentation-only change must match the explicit documentation rules
to skip live services.

| Change | Live coverage |
| --- | --- |
| Documentation only | No live services; unit and build checks still run. |
| Shared connection, query, validation, security, REST/MCP, or runtime changes | Reference PostgreSQL **17.11** and MySQL **8.4.13** driver contracts plus public REST and remote MCP smoke acceptance, using pinned images and synthetic accounts. |
| Direct changes to a connector or its contract runner | The reference pair, public smoke acceptance, and applicable existing driver, metadata, or TLS jobs for that connector. Changes to shared connector code also select the families that use it. |
| ClickHouse or TiDB driver changes | The applicable TLS job and existing full build/checks job, which holds their versioned driver contracts. This adds more work than the two-database reference profile. |
| Changes to dependency versions or global CI/validation infrastructure; unknown driver impact | All known live jobs. |
| A provider without a versioned live baseline | Existing fixture coverage and the reference profile. This does not change the provider's documented support tier. |

The [selection code](../scripts/ci/contract_checks.py) is the source of truth
for family mappings. Add a mapping and a verifiable contract result when
introducing a new live profile. Use a conservative selection if the affected
family cannot be determined.

Each selected provider job checks for an actual, uncached pass from its
expected top-level tests. A successful `go test` exit alone is insufficient:
an unconfigured service can cause the live test to skip. For the existing full
job, the configured baseline summary still requires all six live driver
contracts to run and pass; its PostgreSQL TLS script also requires explicit pass
evidence. Selection and the final gate publish summaries
showing selected, unselected, passed, failed, and unavailable or skipped jobs.

## Full and manual validation

The complete provider/version contracts, TLS checks, race tests, container
image build, and image smoke tests still run daily at 02:17 China Standard
Time and through **Run workflow** in Actions. The full schedule covers the
original matrix; the PR-only reference and standalone PostgreSQL TLS jobs are
not duplicated there. Scheduled and manual runs execute the full [public acceptance matrix](validation/public-task-acceptance.md). A newer commit to the same PR cancels the superseded run.

When automatic provider execution is unavailable or an investigation needs
the complete matrix, run **Go CI** against a branch or tag pointing to the
candidate commit. For a PR, verify that the manual run's `headSha` matches the
current PR head before recording evidence:

```bash
gh workflow run go.yml --ref <branch-or-tag>
gh run view <run-id> --json headSha,event,conclusion,url
```

Record the run URL, exact SHA, tested version and contract result. For an
external service, use the existing [driver contract instructions](driver-contract-tests.md)
on the exact commit and record passed, failed and unavailable cases. Keep
service credentials out of PRs and logs. An older green run, an unsupported
provider's fixture, or a manual note cannot turn a failed required check green.
Rerun the selected job after restoring its environment.
