# PostgreSQL Gateway Query Benchmark

**Run date:** 2026-10-21<br>
**Environment:** local WSL, PostgreSQL 17.11, Go 1.26.1, linux/amd64, 24 visible CPUs<br>
**Source data:** generated synthetic tables in a local PostgreSQL database

## Summary

This custom benchmark compares direct PostgreSQL access with Data Fabric's in-process QueryService over a real local PostgreSQL driver. Before timing each workload, it checks column names, returned row values and JSON types, and total counts for equivalence.

The 500-row run covers 14 workload shapes with 20 warmups and 1,000 serial latency samples per path. Data Fabric P95 latency ranges from **0.682 to 2.159 ms**. The difference between the independently measured direct and Data Fabric P95 values ranges from **0.446 to 0.694 ms**; the P50 value gap ranges from **0.246 to 0.450 ms**.

The 5,000-row run uses 5 warmups and 100 serial samples per path as a scale check. Data Fabric P95 ranges from **0.643 to 17.122 ms**. P95 gaps range from **0.418 to 1.373 ms**. The largest latency is an unindexed text sort; the wide projection returns 5,000 rows with a 512-character payload and measures 8.201 ms at P95. This run is directional and is not a capacity guarantee.

Resource sampling runs separately from the latency samples. At 500 rows, the measured per-request CPU time gap is **0.314 to 0.810 ms**, and the Go allocation gap is **204.5 to 440.5 KiB**. At 5,000 rows, those ranges are **0.305 to 1.584 ms** and **98.2 to 1,217.2 KiB**. Peak RSS is **70.47 MiB** for the 500-row suite and **91.92 MiB** for the 5,000-row suite. RSS is the shared peak of the benchmark process, which includes the harness and in-process service; it is not an isolated server footprint.

These measurements are a local, serial, in-process lower bound. They exclude HTTP, authentication middleware, concurrent client traffic, and the application-to-gateway network hop. CPU figures measure the Go benchmark process and do not include PostgreSQL server CPU. Allocation figures are Go runtime allocations, not retained heap. Percentile gaps are differences between two independently measured percentiles, not per-request latency guarantees. Hardware, PostgreSQL settings, data shape, connection locality, and concurrent load affect results.

The 14-case suite is a custom microbenchmark, not a TPC benchmark or an official TPC result. This completes the Phase 1 small-workload baseline scope in issue #44; cross-engine comparisons are outside this benchmark.

## Environment and Method

- PostgreSQL 17.11 runs locally in WSL; the benchmark uses Data Fabric's registered PostgreSQL driver for both paths. The database is real; fact and dimension records are generated synthetic data.
- The runner creates the dedicated `data_fabric_perf` database if needed, then creates isolated temporary fact and dimension tables. The fact table has an **id** primary key and an unindexed text payload; the dimension table has about half as many rows. Tables are dropped and their removal is verified after the run. The dedicated database remains available.
- The direct and Data Fabric paths execute the same workload SQL. Both retrieve a result batch, scan rows, and JSON-encode a response. The service path also performs validation, source lookup, routing, normalization, and its connection-pool work.
- The full-page case deliberately fills the first page and requests an exact total, so both paths perform a count query.
- Latency samples are serial; direct and service order alternates. There is no concurrent load.
- CPU time and Go allocations are collected in a separate serial batch for each workload and path (250 samples per path for the 500-row run; 100 for the 5,000-row run). RSS is the Go process's `VmHWM` peak over the complete suite.

## Workload Matrix

| Workload | Query behavior |
| --- | --- |
| small_constant | Small constant SELECT |
| point_lookup | Primary-key equality lookup |
| selective_filter | Selective range filter, ordered by primary key |
| broad_filter | Broad range filter, ordered by primary key |
| ordered_scan | Full scan ordered by primary key |
| sort_unindexed_text | Full scan sorted on an unindexed text column |
| offset_page | Ordered LIMIT and OFFSET page |
| wide_projection | Fourfold payload projection to exercise wider results |
| aggregate_statistics | COUNT, SUM, and AVG |
| grouped_aggregate | Grouped COUNT and SUM |
| distinct_values | DISTINCT over a low-cardinality expression |
| inner_join | Fact-to-dimension inner join |
| left_join | Fact-to-dimension left join with unmatched fact rows |
| full_page_count | Exactly full first page, requiring an exact count |

## Latency Results: 500 Rows

All values are milliseconds, shown as P50 / P95. “Gap” is the Data Fabric percentile value minus the direct PostgreSQL percentile value at the same percentile.

| Workload | Direct P50 / P95 | Data Fabric P50 / P95 | Gap P50 / P95 |
| --- | ---: | ---: | ---: |
| small_constant | 0.119 / 0.183 | 0.365 / 0.682 | 0.246 / 0.499 |
| point_lookup | 0.165 / 0.283 | 0.459 / 0.759 | 0.294 / 0.476 |
| selective_filter | 0.191 / 0.355 | 0.509 / 0.852 | 0.318 / 0.497 |
| broad_filter | 0.321 / 0.571 | 0.645 / 1.030 | 0.324 / 0.459 |
| ordered_scan | 0.326 / 0.604 | 0.618 / 1.050 | 0.292 / 0.446 |
| sort_unindexed_text | 1.327 / 1.655 | 1.695 / 2.159 | 0.368 / 0.504 |
| offset_page | 0.277 / 0.522 | 0.593 / 1.006 | 0.316 / 0.484 |
| wide_projection | 0.611 / 1.132 | 1.061 / 1.826 | 0.450 / 0.694 |
| aggregate_statistics | 0.169 / 0.296 | 0.495 / 0.803 | 0.326 / 0.507 |
| grouped_aggregate | 0.220 / 0.363 | 0.590 / 0.892 | 0.370 / 0.529 |
| distinct_values | 0.176 / 0.243 | 0.488 / 0.774 | 0.312 / 0.531 |
| inner_join | 0.444 / 0.737 | 0.851 / 1.338 | 0.407 / 0.601 |
| left_join | 0.439 / 0.893 | 0.826 / 1.467 | 0.387 / 0.574 |
| full_page_count | 0.382 / 0.617 | 0.694 / 1.154 | 0.312 / 0.537 |

### CPU and Allocation Gaps: 500 Rows

Values are Data Fabric minus direct PostgreSQL per request. CPU is process CPU time; allocation is Go bytes allocated during a request, not retained memory.

| Workload | CPU gap (ms/request) | Go allocation gap (KiB/request) |
| --- | ---: | ---: |
| small_constant | 0.314 | 216.4 |
| point_lookup | 0.379 | 205.0 |
| selective_filter | 0.385 | 210.0 |
| broad_filter | 0.440 | 239.4 |
| ordered_scan | 0.447 | 258.8 |
| sort_unindexed_text | 0.443 | 263.0 |
| offset_page | 0.463 | 235.3 |
| wide_projection | 0.547 | 440.5 |
| aggregate_statistics | 0.418 | 205.5 |
| grouped_aggregate | 0.453 | 205.5 |
| distinct_values | 0.387 | 204.5 |
| inner_join | 0.810 | 224.8 |
| left_join | 0.524 | 242.0 |
| full_page_count | 0.427 | 222.3 |

## Latency Results: 5,000 Rows

This run uses 100 samples per path, so treat it as a directional scale check. All values are milliseconds, shown as P50 / P95.

| Workload | Direct P50 / P95 | Data Fabric P50 / P95 | Gap P50 / P95 |
| --- | ---: | ---: | ---: |
| small_constant | 0.122 / 0.187 | 0.391 / 0.643 | 0.269 / 0.456 |
| point_lookup | 0.167 / 0.217 | 0.458 / 0.728 | 0.291 / 0.511 |
| selective_filter | 0.425 / 0.688 | 0.747 / 1.106 | 0.322 / 0.418 |
| broad_filter | 1.904 / 2.641 | 2.511 / 3.430 | 0.607 / 0.789 |
| ordered_scan | 1.746 / 2.464 | 2.454 / 3.219 | 0.708 / 0.755 |
| sort_unindexed_text | 14.981 / 16.475 | 15.622 / 17.122 | 0.641 / 0.647 |
| offset_page | 1.093 / 1.552 | 1.600 / 2.187 | 0.507 / 0.635 |
| wide_projection | 5.119 / 6.828 | 6.361 / 8.201 | 1.242 / 1.373 |
| aggregate_statistics | 0.299 / 0.404 | 0.621 / 0.895 | 0.322 / 0.491 |
| grouped_aggregate | 0.807 / 1.067 | 1.201 / 1.519 | 0.394 / 0.452 |
| distinct_values | 0.586 / 0.683 | 0.938 / 1.209 | 0.352 / 0.526 |
| inner_join | 1.482 / 2.094 | 2.014 / 2.612 | 0.532 / 0.518 |
| left_join | 2.410 / 3.123 | 3.085 / 3.863 | 0.675 / 0.740 |
| full_page_count | 0.721 / 1.052 | 1.041 / 1.493 | 0.320 / 0.441 |

### CPU and Allocation Gaps: 5,000 Rows

Values are Data Fabric minus direct PostgreSQL per request. CPU is process CPU time; allocation is Go bytes allocated during a request, not retained memory.

| Workload | CPU gap (ms/request) | Go allocation gap (KiB/request) |
| --- | ---: | ---: |
| small_constant | 0.334 | 218.0 |
| point_lookup | 0.305 | 99.1 |
| selective_filter | 0.427 | 156.8 |
| broad_filter | 0.863 | 1,065.5 |
| ordered_scan | 1.058 | 1,217.2 |
| sort_unindexed_text | 1.006 | 981.5 |
| offset_page | 0.653 | 553.7 |
| wide_projection | 1.584 | 1,072.6 |
| aggregate_statistics | 0.338 | 99.3 |
| grouped_aggregate | 0.407 | 98.2 |
| distinct_values | 0.346 | 98.5 |
| inner_join | 0.588 | 384.4 |
| left_join | 0.634 | 518.8 |
| full_page_count | 0.548 | 354.6 |

## Raw Results

CSV files contain each latency sample and per-workload CPU/allocation summaries, plus the suite's peak process RSS:

- [500-row raw CSV](results/postgres_gateway_500_rows_2026-10-21.csv)
- [5,000-row raw CSV](results/postgres_gateway_5000_rows_2026-10-21.csv)

## Which Standard Benchmarks Fit?

- **TPC-H** is a useful first reference for analytical query coverage: its decision-support workload contains 22 business-oriented queries with different joins, aggregations, filters, and data volumes. A supported subset or adapted harness should be described as “TPC-H-derived,” not as an official TPC-H result.
- **TPC-DS** is broader and more complex. It is a later-stage comparison because it requires more schema, query-dialect, data-generation, and execution work.
- **PostgreSQL pgbench** is useful for PostgreSQL connection concurrency and transaction workloads. Its built-in workload is TPC-B-like, not an analytical SQL suite. It speaks PostgreSQL's wire protocol; Data Fabric's HTTP API needs an HTTP load tool or a dedicated adapter for end-to-end concurrency testing.
- The 14-case matrix here is a custom microbenchmark, not TPC-compliant. It compares the Data Fabric query service with a real local PostgreSQL source.

Official references: [TPC current specifications](https://tpc.org/TPC_Documents_Current_Versions/current_specifications5.asp), [TPC-H v3.0.1](https://www.tpc.org/TPC_Documents_Current_Versions/pdf/TPC-H_v3.0.1.pdf), [TPC-DS v4.0.0](https://www.tpc.org/TPC_Documents_Current_Versions/pdf/TPC-DS_v4.0.0.pdf), and [PostgreSQL pgbench documentation](https://www.postgresql.org/docs/current/pgbench.html).

## Reproduce

The PostgreSQL account needs permission to connect to the admin database and create `data_fabric_perf` if it does not already exist. Set the password through the shell environment; do not put real credentials in the repository. Docker is used to install the pinned SQLGlot Python dependency into a temporary directory, leaving the host Python environment untouched.

The runner defaults to 100 samples, 5 warmups, and 500 rows. To reproduce the recorded 500-row run:

~~~bash
export PGHOST=localhost
export PGPORT=5432
export PGUSER=your-postgres-user
export PGPASSWORD=your-postgres-password
export PGSSLMODE=disable
export DATA_FABRIC_PERF_ADMIN_DB=postgres
export DATA_FABRIC_PERF_ROWS=500
export DATA_FABRIC_PERF_SAMPLES=1000
export DATA_FABRIC_PERF_WARMUPS=20
export DATA_FABRIC_PERF_RAW_OUTPUT=docs/performance/results/postgres_gateway_500_rows_2026-10-21.csv
./scripts/performance/run_postgres_gateway_benchmark.sh
~~~

For the 5,000-row scale run, set `DATA_FABRIC_PERF_ROWS=5000`, `DATA_FABRIC_PERF_SAMPLES=100`, `DATA_FABRIC_PERF_WARMUPS=5`, and use `docs/performance/results/postgres_gateway_5000_rows_2026-10-21.csv` for `DATA_FABRIC_PERF_RAW_OUTPUT`. The runner prints PostgreSQL and Go versions, checks result equivalence, reports latency and resource measurements, and verifies removal of its temporary tables.

## Regression Coverage

The [query routing tests](../../internal/service/query_engine_routing_test.go) verify that a non-full first page returns an exact total without a count query, while a full page still runs a count query to preserve exact totals. This benchmark complements that deterministic coverage with a real local PostgreSQL source.
