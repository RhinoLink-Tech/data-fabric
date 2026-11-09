# REST and MCP Task Baseline

This runner measures warmed, serial query tasks across the public REST and MCP Streamable HTTP boundaries on the disposable reference deployment. The stack uses MySQL for metadata, and pinned MySQL and PostgreSQL containers for isolated synthetic source data. It runs the existing public acceptance matrix first, so denial, malformed-query, timeout, pagination and other pass/fail durations are captured separately from timed successful tasks.

## Run

From the repository root, with Docker and Compose available:

```sh
mkdir -p docs/performance/results
run_date=$(date -u +%F)

go run ./deploy/mcp/reference/cmd/acceptance   -benchmark-samples 100   -benchmark-output "docs/performance/results/rest_mcp_task_${run_date}.jsonl"   -load-duration 2m   -load-output "docs/performance/results/rest_mcp_load_${run_date}.jsonl"   -recovery-wait 1m   > "docs/performance/results/rest_mcp_acceptance_${run_date}.jsonl"   2> /tmp/data-fabric-reference-run.log
```

The runner builds the production Dockerfile, creates a uniquely named disposable reference stack, generates temporary identities and credentials, runs the public acceptance matrix, then measures the tasks below. It removes the temporary stack and generated configuration at exit. The image build and profile startup are included in setup timing; benchmark sample durations start after deployment, policy loading and five warm-up tasks for each source/transport/scenario.

`-benchmark-samples` accepts 20–10,000 timed attempts per combination. The task JSONL file has one environment record, raw warm-ups and timed attempts, and one percentile summary per combination. `-load-duration` accepts 15 seconds to 10 minutes and requires `-load-output`; its JSONL records REST/MCP restart-to-first-authorized-result, isolated per-task source statement counters, synchronized one/four-way small-query waves, two-tenant waves, mixed first-page/full-result tasks, a paced fixed-duration soak, and component resource samples before/during/after recovery. Each soak worker starts at most one task per second. Synchronized mixed bursts are measured separately and can hit the configured request-rate ceiling. The mixed full-result task takes longer because it uses three gateway calls; it is not a deliberately slow source query or a large-data test.

The acceptance JSONL preserves denied/error outcomes. The reference log records startup stages, candidate checkout, image identities/sizes and the initial idle component resource snapshot. The load JSONL records resource snapshots as structured per-container samples and normalized source statement digests. All output contains only generated fixture identifiers and no bearer tokens or source credentials. PostgreSQL statement counters are enabled only in this disposable reference profile; they are not part of the ordinary quickstart.

For a separate large-result and gateway memory check, see the [synthetic resource envelope](resource-envelope.md). It exercises the cumulative output-byte budget under an explicit cgroup cap; the small fixture baseline above does not measure that envelope.

## Measured tasks

| Scenario | Work | What the duration includes |
| --- | --- | --- |
| `first_page` | One-row, read-only query against a synthetic fixture | One REST request or one MCP tool call, response decoding and result contract check; warm connection/session reused |
| `complete_result` | Fetch all three ordered fixture rows at page size one | Initial request plus both continuations, response decoding and ordered result validation; includes the first-page exact-total behavior |

Each combination of source (`mysql`, `postgresql`), transport (`rest`, `mcp-http`) and scenario has raw warm-up/timed attempts, successful sample counts, failures, and nearest-rank P50/P95/P99 summaries in milliseconds. A combination with any failed warm-up is not timed. The MCP initialization handshake is deliberately excluded after the warm-up. `gateway_request_count` counts public gateway calls for a complete task, not the gateway's internal SQL statements.

## Current full-profile run (2026-11-08)

**Measured code candidate:** `2693852df6bd9daf5131e33990ec2f7c1e8ded94` (clean tree; the report/evidence commit is separate). The same code SHA and gateway image digest appear in all raw records.

**Run command:** the command above with `-benchmark-samples 100`, `-load-duration 2m`, and `-recovery-wait 1m`. It produced 800 timed tasks, 40 serial warm-ups, 54 public acceptance outcomes, 16 synchronized capacity summaries, and 960 paced soak tasks.

**Host/runtime:** WSL2 Ubuntu 26.04.1, Linux 6.18.33.2-microsoft-standard-WSL2, x86_64; Intel Core Ultra 9 275HX, 24 CPUs, 16.47 GB visible memory; Go 1.26.1; Docker Server 29.8.0; Compose 5.5.1. Containers had no CPU or memory quotas.

**Client/identity versions:** REST used Go `net/http`; the MCP client was `github.com/mark3labs/mcp-go v0.43.2`; source drivers were `github.com/go-sql-driver/mysql v1.10.0` and `github.com/lib/pq v1.12.0`. REST bearer identities and the local OIDC issuer for MCP were synthetic fixtures from this candidate; the issuer ran from the same gateway image digest below. No external identity provider or model was involved.

**Profile:** MySQL 8.4.13 metadata, MySQL 8.4.13 and PostgreSQL 17 synthetic sources, separate API and MCP gateway containers, session TTL 8 seconds, two admitted concurrent queries per tenant, 5-second query timeout, three-row `orders` fixture, five warm-ups per serial cell, one reference replica.

| Image | Immutable identity | Local expanded image size |
| --- | --- | ---: |
| Gateway and OIDC fixture | `sha256:a08e045965f2e416f174d2e90281f30a68c93518a8be69f71e94ad5b78f1dfbe` | 260,026,583 bytes (248.0 MiB) |
| MySQL metadata/source | `mysql:8.4.13@sha256:a0225d1ea6631c8bc2385dc4c2faba17bbf1834ecf369e1659a9a382bd8d5dd7` | 1,102,592,139 bytes |
| PostgreSQL source | `postgres:17-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24` | 423,189,986 bytes |

The run built the local gateway image in 30.135 seconds, brought up the databases and API in 23.867 seconds, loaded policy and readied MCP in 12.896 seconds, and completed setup plus the public smoke sequence in 1m7.976s. The Go `run` compile time and any registry pull were outside those stage timers. The post-setup whole-stack Docker snapshot was 1002.37 MiB.

The full public matrix produced **48 pass, 6 explicitly unsupported, 0 fail**. Unsupported REST cases were A03 table listing, A04 table description, A14–A16 REST authentication cases using the unsupported HS256 profile, and A25 REST schema search. These remain explicit surface gaps rather than successful REST behavior.

Each cell below is nearest-rank P50 / P95 / P99 in milliseconds from 100 successful timed samples. MCP initialization is excluded after warm-up.

| Transport | Source | First page P50 / P95 / P99 (ms) | Complete result P50 / P95 / P99 (ms) |
| --- | --- | ---: | ---: |
| REST | MySQL | 110.26 / 120.81 / 130.18 | 332.17 / 367.21 / 382.75 |
| REST | PostgreSQL | 108.12 / 115.19 / 121.82 | 324.68 / 340.72 / 349.89 |
| MCP Streamable HTTP | MySQL | 119.83 / 139.36 / 196.91 | 351.97 / 366.51 / 380.80 |
| MCP Streamable HTTP | PostgreSQL | 116.31 / 124.88 / 128.16 | 351.88 / 365.84 / 371.02 |

The restart wave measured 7.454 seconds from stopping/restarting the API through Compose, followed by a successful first authorized result at 7.574 seconds. MCP restart took 7.982 seconds and its first authorized result arrived at 8.119 seconds. Both succeeded without a failed retry.

The synchronized task groups below report attempted/successful tasks, rate-limited tasks, and P95 among successful tasks. One-way uses five tasks; same-tenant four-way uses 20; the two-tenant groups use 12. These small synthetic samples describe this exact profile, not a general capacity guarantee.

| Transport | Source | Same tenant, 1-way | Same tenant, 4-way | Two tenants, 4-way | Mixed first-page/full-result |
| --- | --- | ---: | ---: | ---: | ---: |
| REST | MySQL | 5/5, 113.35 ms | 20/20, 130.88 ms | 12/12, 134.70 ms | 12/12, 358.82 ms |
| REST | PostgreSQL | 5/5, 117.95 ms | 20/20, 217.72 ms | 12/12, 129.87 ms | 12/12, 346.75 ms |
| MCP Streamable HTTP | MySQL | 5/5, 121.93 ms | 20/20, 226.07 ms | 12/12, 139.72 ms | 12/12, 394.78 ms |
| MCP Streamable HTTP | PostgreSQL | 5/5, 117.13 ms | 20/20, 230.56 ms | 12/12, 141.09 ms | **6/12, 6 rate-limited; 386.92 ms P95 of the six successes** |

There were no query-admission rejections or generic execution failures in these waves. The MCP/PostgreSQL mixed burst was explicitly summarized as `pass_with_rate_limits`; a rate-limited task is not counted as a successful result. The mixed full-result task is a three-page consumer journey and takes roughly three times as long as first page, but it is not a deliberately slow SQL statement or a large-result stress test.

The per-task source counters were reset after warm-up and recorded separately from gateway calls and returned rows. Every isolated first-page and complete-result measurement observed one data `SELECT` plus one exact `COUNT(*)`. Complete result used three public gateway calls but only one source data statement and one exact count on each tested source/transport combination; this three-row case therefore observed a held continuation cursor, not source SQL re-execution. It must not be generalized to other drivers, query shapes, cursor expiry or larger results.

| Transport | Source | Task | Gateway calls | Data statement calls / rows / DB time (ms) | Exact COUNT calls / DB time (ms) |
| --- | --- | --- | ---: | ---: | ---: |
| REST | MySQL | First page | 1 | 1 / 1 / 0.168 | 1 / 0.099 |
| REST | MySQL | Complete result | 3 | 1 / 3 / 0.200 | 1 / 0.419 |
| REST | PostgreSQL | First page | 1 | 1 / 1 / 0.031 | 1 / 0.043 |
| REST | PostgreSQL | Complete result | 3 | 1 / 3 / 0.029 | 1 / 0.011 |
| MCP Streamable HTTP | MySQL | First page | 1 | 1 / 1 / 0.175 | 1 / 0.095 |
| MCP Streamable HTTP | MySQL | Complete result | 3 | 1 / 3 / 0.171 | 1 / 0.368 |
| MCP Streamable HTTP | PostgreSQL | First page | 1 | 1 / 1 / 0.031 | 1 / 0.019 |
| MCP Streamable HTTP | PostgreSQL | Complete result | 3 | 1 / 3 / 0.029 | 1 / 0.006 |

MySQL Performance Schema exposed rows examined (1 on first-page data/count and 3 on complete-result data); the full-result count digest reported 0 examined rows. PostgreSQL `pg_stat_statements` does not provide rows examined. Its execution-time values and the MySQL values are database digest totals, not end-to-end task budgets.

The paced two-minute soak completed **960/960 tasks** at 7.99 tasks/second (1,920 public gateway calls); P50/P95/P99 were 180.03 / 429.52 / 442.90 ms. It included 480 MySQL first-page tasks and 480 PostgreSQL full-result tasks across both transports and tenants, with no task failures, admission rejections or rate limits. PostgreSQL counters reconciled to 480 data calls/1,440 rows and 480 exact counts/480 rows. MySQL aggregate counters reported 478 data calls/480 rows and 479 exact counts/480 rows for 480 successful first-page tasks. That 1–2 call discrepancy is unresolved; use the isolated per-task counters for exact cost and treat concurrent MySQL digest totals as approximate until instrumentation is reconciled.

| Whole-stack sample | Total container memory |
| --- | ---: |
| Post-setup smoke snapshot (run log) | 1002.37 MiB |
| Before capacity waves | 1006.42 MiB |
| After capacity waves | 1031.04 MiB |
| Before soak | 1029.07 MiB |
| Soak midpoint | 1112.86 MiB |
| After soak | 1048.43 MiB |
| After 60-second recovery wait | 1045.68 MiB |

After recovery, API and MCP containers used 36.10 and 37.35 MiB respectively. Whole-stack memory remained 39.26 MiB above the pre-capacity snapshot after 60 seconds; this is an observed residual, not evidence of a leak or proof of eventual return. No CPU/memory cgroup limits were set, so this run did not enforce a hard resource ceiling.

### Historical budget hypotheses after this measurement

| Hypothesis | Current evidence | Decision |
| --- | --- | --- |
| First authorized task within five minutes | Local image-build/reference setup plus smoke completed in 1m7.976s; `go run` compile and registry pulls were not timed. | Retain as a preview-evaluation goal, not an external SLO until independent onboarding validates it. |
| Three-second readiness | Databases/API took 23.867s; policy/MCP readiness 12.896s; restart-to-first-result was 7.574–8.119s. | Retire as a current-profile readiness promise; keep startup/restart stages separately measured. |
| 150 MiB Core image | Current expanded gateway/OIDC image was 248.0 MiB. | Not met by this distribution; keep a smaller Core build as evidence-gated #448 work, not a release claim. |
| 128 MiB warm idle | API+MCP totaled 59.34 MiB before capacity; the full reference stack was 1002.37 MiB after smoke. | Keep the value only as an app-process diagnostic; do not present it as whole-deployment idle cost. |
| 256 MiB four-query peak | API+MCP totaled 66.63 MiB after capacity and 141.20 MiB at soak midpoint, while the complete stack reached 1112.86 MiB. | Observed values fit the hypothesis for the two gateway processes in this run, but are not enforced limits or a general envelope; #451 must define and enforce selected-profile aggregate bounds. |

No production latency SLO, maximum sustainable capacity, cloud monthly price, registry-transfer size, large-result bound or customer workload claim is made. The 60-second residual, MCP/PostgreSQL burst rate limits and MySQL soak counter discrepancy are concrete evidence for #451's resource/capacity envelope and instrumentation reconciliation. These measurements do not justify speculative parser, combined-runtime or local-store optimization issues.

**Raw evidence:** [public acceptance JSONL](results/rest_mcp_acceptance_2026-11-08_2693852.jsonl), [800 serial samples and summaries](results/rest_mcp_task_2026-11-08_2693852.jsonl), [restart/capacity/SQL/resource JSONL](results/rest_mcp_load_2026-11-08_2693852.jsonl), and [reference build/startup log](results/rest_mcp_reference_run_2026-11-08_2693852.log). The runner's code and command are reproducible from the candidate SHA above.

## Previous serial-only run (historical; 2026-11-07)

**Run time:** 2026-11-07 16:22 UTC<br>
**Candidate:** `f594b0e9246c8b836a2c858f7c9362c69c3301a9`, clean working tree<br>
**Gateway image:** `sha256:5101c4e27528dcf42a1433fc4fd5cebe6c88fdb495016a39182bcbf3c1ddde26`<br>
**Host:** WSL2 Ubuntu, linux/amd64, Intel Core Ultra 9 275HX, 24 CPUs, 16.47 GB visible memory; Go 1.26.1, Docker 29.8.0, Compose 5.5.1<br>
**Profile:** MySQL 8.4.13 metadata, pinned MySQL 8.4.13 and PostgreSQL 17 sources, separate REST API and MCP containers, synthetic bearer/OIDC identities, session TTL 8 seconds, per-tenant query capacity 2, 5-second request timeout, no container CPU/memory limits, 3-row orders fixture.<br>
Each cell reports nearest-rank P50 / P95 / P99 in milliseconds from 100 successful timed samples after five warm-ups.

| Transport | Source | First-page latency P50 / P95 / P99 (ms) |
| --- | --- | ---: |
| REST | MySQL | 109.482 / 120.992 / 125.172 |
| REST | PostgreSQL | 108.622 / 118.112 / 122.311 |
| MCP Streamable HTTP | MySQL | 117.556 / 129.325 / 134.456 |
| MCP Streamable HTTP | PostgreSQL | 116.123 / 138.132 / 143.806 |

| Transport | Source | Complete-result latency P50 / P95 / P99 (ms) |
| --- | --- | ---: |
| REST | MySQL | 328.668 / 341.757 / 367.081 |
| REST | PostgreSQL | 333.058 / 370.676 / 393.570 |
| MCP Streamable HTTP | MySQL | 352.024 / 390.071 / 462.731 |
| MCP Streamable HTTP | PostgreSQL | 364.853 / 438.458 / 462.762 |

The complete-result task returns the three ordered fixture rows in three public gateway requests. The environment and image IDs, all 800 timed samples, 40 warm-up samples and eight summaries are in [raw task measurements](results/rest_mcp_task_2026-11-08.jsonl). The companion [public acceptance results](results/rest_mcp_acceptance_2026-11-08.jsonl) contain 44 passes, six explicitly unsupported REST cases and zero failures. The [reference run log](results/rest_mcp_reference_run_2026-11-08.log) records startup stage times and an idle whole-stack Docker memory snapshot of 993.54 MiB after setup/reuse checks and before the full acceptance matrix and timed sampling.

These values describe an earlier local serial synthetic run and remain useful dated history. They are not release-wide SLOs or concurrency budgets. The current-profile run above adds synchronized load, soak/resource-return measurements and per-task source statement instrumentation; neither run uses production source data or establishes a production task SLO.

## Interpretation limits

This baseline is serial and uses three fixture rows. It is suitable for comparing public-boundary overhead and checking that first-page/full-result behavior works on both connector paths. It does not establish concurrency capacity, a production latency SLO, large-result scaling, cancellation latency, or sustained resource return. P99 from 100 samples is the single slowest observation. Host CPU/memory and Docker/Compose versions plus candidate/image identities are included where the host exposes them.

The current run instruments per-task source statement counts and exact `COUNT(*)` cost, separately from the three public calls needed to finish the sample result. The concurrent MySQL soak digest counters did not reconcile exactly to 480 successful tasks (478 data calls and 479 COUNT calls), so use isolated per-task counters for exact cost and retain the aggregate mismatch as an instrumentation follow-up. Pagination behavior is specific to these MySQL/PostgreSQL fixtures and this three-row task.

The pre-existing in-process PostgreSQL microbenchmark is a separate lower-bound measurement; see [PostgreSQL gateway benchmark](postgres-gateway-benchmark.md). Whole-container release footprint measurements are recorded in [deployment footprint](deployment-footprint.md).
