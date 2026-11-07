# REST and MCP Task Baseline

This runner measures warmed, serial query tasks across the public REST and MCP Streamable HTTP boundaries on the disposable reference deployment. The stack uses MySQL for metadata, and pinned MySQL and PostgreSQL containers for isolated synthetic source data. It runs the existing public acceptance matrix first, so denial, malformed-query, timeout, pagination and other pass/fail durations are captured separately from timed successful tasks.

## Run

From the repository root, with Docker and Compose available:

```sh
mkdir -p docs/performance/results

go run ./deploy/mcp/reference/cmd/acceptance \
  -benchmark-samples 100 \
  -benchmark-output docs/performance/results/rest_mcp_task_$(date -u +%F).jsonl \
  > docs/performance/results/rest_mcp_acceptance_$(date -u +%F).jsonl \
  2> /tmp/data-fabric-reference-run.log
```

The runner builds the production Dockerfile, creates a uniquely named disposable reference stack, generates temporary identities and credentials, runs the public acceptance matrix, then measures the tasks below. It removes the temporary stack and generated configuration at exit. The image build is part of the reported setup time; benchmark sample durations start after deployment, policy loading and five warm-up tasks for each source/transport/scenario.

`-benchmark-samples` accepts 20–10,000 timed attempts per combination. The JSONL file has one environment record, raw warm-ups and timed attempts, and one percentile summary per combination. Acceptance failures are preserved in the acceptance JSONL and the runner still collects benchmark samples before exiting nonzero. Keep the acceptance JSONL and reference log alongside the raw benchmark file when publishing a run; the first contains denied/error outcomes and the second records startup stages, candidate checkout, image identity and container resource snapshots. These outputs contain only generated fixture identifiers and no bearer tokens or source credentials.

## Measured tasks

| Scenario | Work | What the duration includes |
| --- | --- | --- |
| `first_page` | One-row, read-only query against a synthetic fixture | One REST request or one MCP tool call, response decoding and result contract check; warm connection/session reused |
| `complete_result` | Fetch all three ordered fixture rows at page size one | Initial request plus both continuations, response decoding and ordered result validation; includes the first-page exact-total behavior |

Each combination of source (`mysql`, `postgresql`), transport (`rest`, `mcp-http`) and scenario has raw warm-up/timed attempts, successful sample counts, failures, and nearest-rank P50/P95/P99 summaries in milliseconds. A combination with any failed warm-up is not timed. The MCP initialization handshake is deliberately excluded after the warm-up. `gateway_request_count` counts public gateway calls for a complete task, not the gateway's internal SQL statements.

## Recorded synthetic run

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

These values describe one local, serial synthetic run. They are not release-wide SLOs or concurrency budgets. The run did not include 1/4-way concurrent load, mixed slow/fast contention, soak/resource-return measurements, source statement instrumentation, or production source data. Those measurements remain open under [issue #441](https://github.com/RhinoLink-Tech/data-fabric/issues/441).

## Interpretation limits

This baseline is serial and uses three fixture rows. It is suitable for comparing public-boundary overhead and checking that first-page/full-result behavior works on both connector paths. It does not establish concurrency capacity, a production latency SLO, large-result scaling, cancellation latency, or sustained resource return. P99 from 100 samples is the single slowest observation. Host CPU/memory and Docker/Compose versions plus candidate/image identities are included where the host exposes them.

Internal source statement counts and exact `COUNT(*)` cost are not instrumented by this runner. In the complete-result case, the exact total is part of the API contract; the report must not treat the three public calls as three source SQL statements because the current continuation implementation may re-execute the query. Follow-up source-work and mixed-load/soak evidence remains tracked by [issue #441](https://github.com/RhinoLink-Tech/data-fabric/issues/441).

The pre-existing in-process PostgreSQL microbenchmark is a separate lower-bound measurement; see [PostgreSQL gateway benchmark](postgres-gateway-benchmark.md). Whole-container release footprint measurements are recorded in [deployment footprint](deployment-footprint.md).
