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
