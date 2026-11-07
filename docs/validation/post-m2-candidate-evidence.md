# Post-M2 Candidate Deployment and Recovery Evidence

This runbook implements issue #416. It binds deployment, second-identity reuse, two-replica operations, and database recovery evidence to one source commit and one locally built gateway image. It uses synthetic PostgreSQL and MySQL fixtures. It does not publish an artifact or require a real-team environment.

## Freeze the candidate

Run from a clean checkout of the candidate commit after it has landed on the branch under review. Record the SHA before building:

    candidate_sha=$(git rev-parse HEAD)
    test -z "$(git status --porcelain)"
    candidate_image="data-fabric:candidate-$(git rev-parse --short=12 HEAD)"
    printf "candidate_sha=%s\n" "$candidate_sha"
    go version
    docker build --file Dockerfile --tag "$candidate_image" .
    docker image inspect --format "{{.Id}}" "$candidate_image"

The image ID is the local immutable image identity used for the run. This project does not publish a container image, so do not describe it as a registry digest. Keep the checkout and built image unchanged until both reference and recovery runs finish.

## Deploy once, reuse from a second identity, and exercise two replicas

Run the full reference matrix and the two-replica suite against that same prebuilt image:

    set -o pipefail
    go run ./deploy/mcp/reference/cmd/acceptance -image "$candidate_image" -multi-replica 2>&1 | tee "candidate-reference-$candidate_sha.log"

The command starts and removes its isolated Compose stack. It runs the public REST and MCP acceptance matrix first, then the REST/MCP two-replica continuation checks. The acceptance profile reports the candidate revision, database image versions, effective limits, and image identity. Keep the complete log, including unsupported cases and any safe failure reason.

The reference task registers synthetic sources once under app-a, then checks discovery and permitted reads under the independent same-tenant app-b identity. Treat that as the second-application reuse evidence; the outsider identity is expected to remain denied. The two-replica checks cover continuation affinity, routing changes, drain and retirement, cumulative result budgets, current policy revocation, rollback publication, and publication history. A failed case fails the command. Unsupported cases remain explicit limitations, never passes.

For pull requests, go.yml runs the premerge smoke subset; the scheduled or manually dispatched full workflow runs the complete reference matrix. Link the successful required CI run for this exact source SHA alongside the full local reference log. The two acceptance paths in CI also use a single built image for both standalone and two-replica runs.

## Upgrade and recover metadata

Run the existing disposable MySQL rehearsal from the same clean candidate checkout:

    set -o pipefail
    ./scripts/release/rehearse_mysql_upgrades.sh 2>&1 | tee "candidate-recovery-$candidate_sha.log"

The script uses a disposable container and database. It upgrades the historical v1.2.0 schema, exercises encrypted credential migration and fail-closed startup, creates a versioned managed-policy grant, revokes and rolls it back, backs up the full metadata database, drops and restores only that temporary database, and verifies:

- source metadata and tenant/name indexes;
- decryption of credentials under the retained candidate keyring;
- restored active policy revision 3 and the historical revoked revision 2;
- the publisher and activated outcome for both revocation and rollback.

It prints the exact MySQL version and image digest, Go version, and backup SHA-256. Preserve those values with the log. The keyring in this test is synthetic; a real deployment must retain the actual credential key material with its backup and restore procedure.

## Candidate evidence record

Publish the results in issue #416 and link the evidence from the release review. Include:

| Evidence | Record |
| --- | --- |
| Candidate | Full source SHA; clean checkout confirmation |
| Gateway image | Tag and local sha256 image ID |
| Toolchain and fixture | Go version; PostgreSQL/MySQL versions and image digests from the logs |
| Public access and reuse | Full acceptance log; REST/MCP pass, fail, and unsupported counts |
| Shared operation | Two-replica pass/fail cases, including budgets, revocation, rollback, and drain |
| Upgrade and restore | MySQL version/digest; baseline; migration result; restored sources, credentials, policy revisions/history; backup checksum |
| CI | Required workflow URL and conclusion for the same candidate SHA |
| Limits | Any unsupported path, unverified connector version, or environment-dependent result |

Do not make a support claim broader than the catalog and observed evidence. Keep the candidate SHA and image ID on every run; results from an older SHA or a separately rebuilt image do not satisfy this gate. The validation remains scoped to the synthetic reference profile and the pinned database versions recorded in the run.

## Completion gate

Close issue #416 only after the exact candidate SHA has passing required CI and reference acceptance, the MySQL upgrade/backup/restore rehearsal passes, evidence is recorded and linked, and the support catalog remains consistent with the tested profile. This gate does not authorize a production release, binary publication, new connector, or real-team pilot.
