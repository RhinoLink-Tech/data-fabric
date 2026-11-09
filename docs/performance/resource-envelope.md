# Synthetic resource envelope

This opt-in profile measures the gateway result-byte budget under a real Linux container memory cap. It uses only the disposable three-row MySQL and PostgreSQL fixtures created by the reference runner.

## Run

Run from the repository root with Docker Engine configured for Linux containers:

```sh
go run ./deploy/mcp/reference/cmd/acceptance   -gateway-memory-limit-mib 512   -large-result-output /tmp/data-fabric-large-result.jsonl   -recovery-wait 1m   > /tmp/data-fabric-acceptance.jsonl   2> /tmp/data-fabric-acceptance.log
```

The command runs the full public acceptance matrix before the resource cases. Add `-smoke` to run the pre-merge acceptance subset. The large-result cases run in either profile. The runner creates a unique Compose project and removes only that project when it finishes.

The memory limit applies to the API and MCP gateway containers. Docker memory and swap limits are set to the same value. The runner checks the configured limit before, during, and after each query. A limit below 64 MiB or above 64 GiB is rejected.

## Workload and evidence

The runner cross-joins nine aliases of the three-row `orders` fixture and selects a 4096-byte string for each of 19,683 rows. The logical output exceeds 80 MB. It submits batches of 1,000 rows through REST and MCP, using both MySQL and PostgreSQL.

Each scenario must stop with the configured cumulative result-byte-limit error. The JSONL records include successful page bytes, returned row count, request count, source database connection state, per-container memory and CPU samples requested every 500 ms (actual cadence also depends on Docker stats response time), the peak gateway memory, and a final sample after the recovery wait. Query result data, credentials, and source identifiers are not recorded.

The `output_bytes` field sums the serialized columns and rows from successful pages. The page rejected by the byte budget is excluded. `rows_returned` must remain below the fixture's total row count. The API and MCP errors must state that the result is incomplete, so a partial result cannot be mistaken for completion.

The source connection snapshot reads only the disposable source database's `sample_reader` sessions. It records active, idle, and total sessions after the budget error. The expected end state has no active source query sessions; remaining connections may be idle in the database/sql pool.

## Limits

This run provides reproducible local evidence for the configured output-byte guard, gateway container memory limit, and query connection cleanup. It does not establish a production SLO, cluster-wide memory envelope, database memory limit, cloud cost, or customer workload capacity. The reference profile limits only the two gateway containers; the synthetic databases have no cgroup limit.


## Measured local run (2026-11-09)

The large-result run used clean candidate bbdb6b4be3852dda8ac50bf41660c10532561de7, Docker Server 29.8.0 and Compose 5.5.1. Each API and MCP container had a 512 MiB memory limit and an equal memory+swap limit. The configured cumulative result budget was 67,108,864 bytes.

| Transport | Source | Rows returned before budget stop | Bytes in successful pages | Active source connections after stop |
| --- | --- | ---: | ---: | ---: |
| REST | MySQL | 16,000 | 65,617,216 | 0 |
| REST | PostgreSQL | 16,000 | 65,617,200 | 0 |
| MCP Streamable HTTP | MySQL | 16,000 | 65,617,216 | 0 |
| MCP Streamable HTTP | PostgreSQL | 16,000 | 65,617,200 | 0 |

All four cases stopped with the explicit incomplete-result budget error before reaching the 19,683-row synthetic cardinality. The sampled peak was 119.3 MiB for the API process and 113.7 MiB for MCP; after the 60-second recovery wait they were 78.8 MiB and 83.7 MiB. The source pools retained 2–5 idle connections per case and no active query connection. Samples and per-case outcomes are in [raw large-result resource evidence](results/resource_envelope_2026-11-09_bbdb6b4.jsonl).

The clean 9f6670fcccbc3b59d03af154d7a8829f095d88ff candidate also completed the 2-minute paced soak with the same 512 MiB per-gateway cap: 960/960 tasks passed at 7.99 tasks/second (1,920 public calls). No task failed or was rate-limited during the soak. One of the 16 separate synchronized capacity summaries recorded 7/12 successes and 5 rate limits; the runner classified it as pass_with_rate_limits. API/MCP midpoint samples were 52.9/58.7 MiB; after 60 seconds they were 39.3/36.2 MiB. Source connections returned to zero active (8 MySQL and 15 PostgreSQL connections remained idle in their pools). Whole-stack memory was 1,090,435,152 bytes immediately before the soak and 1,109,372,434 bytes after recovery, an 18.1 MiB (1.74%) observed residual. These are measured values for this local reference profile, not production SLOs or a promise that arbitrary workloads stay below the cap. See [capacity evidence with cgroup limits](results/rest_mcp_load_2026-11-09_9f6670f_512mib.jsonl).

The smoke matrix on this candidate recorded 23 passes, one explicitly unsupported case (A25, REST schema search), and zero failures. See [smoke evidence](results/rest_mcp_acceptance_2026-11-09_9f6670f.jsonl).

### MySQL digest reconciliation

The reference runner now enables and truncates events_statements_history_long in its disposable MySQL source and records those retained statement events alongside events_statements_summary_by_digest. This event-history consumer is enabled only by the isolated measurement harness.

In the clean candidate's uncapped 2-minute run, 480 successful MySQL first-page tasks produced 480 data statements and 480 exact-count statements in raw history, each returning 480 rows. The digest summary reported 479 data calls / 480 rows and 480 count calls / 480 rows. The same values remained after 60 seconds. A separate clean run with the 512 MiB cap recorded 480 calls and 480 rows in both history and digest for both statements. The previous discrepancy is therefore localized to an intermittent one-call undercount in the concurrent digest summary; it was not a missing source execution in the raw retained history. Keep digest COUNT_STAR approximate under concurrent load. The finite history-long ring is appropriate only for these bounded synthetic runs; it is not a general production accounting mechanism. Both raw runs are preserved in [uncapped diagnostics](results/rest_mcp_load_2026-11-09_9f6670f.jsonl) and [capped soak evidence](results/rest_mcp_load_2026-11-09_9f6670f_512mib.jsonl).
