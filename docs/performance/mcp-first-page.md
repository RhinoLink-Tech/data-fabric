# MCP first-page evaluation (#649)

The MCP `execute_sql_query` first page now defaults to 100 rows when `batch_size` is omitted or non-positive. Explicit values up to 10,000 are still supported; larger values are capped at 10,000. This is a client-visible change from the former 10,000-row default. Clients must use the latest continuation fields while `nextUri` is present, or explicitly request `batch_size: 10000` to retain the previous initial page size. MCP continuations without a size retain the previous effective size; an explicit invalid continuation size still uses 10,000. REST defaults are unchanged.

## Reproducible synthetic measurement

On 2026-11-20, Linux/amd64 in WSL, Intel Core Ultra 9 275HX, the following used the existing SQLite Go driver, without a Docker image or network source:

```sh
CGO_ENABLED=1 GOPROXY=off go test -tags datafabric_sqlite ./internal/service \
  -run '^$' -bench '^BenchmarkFirstPageSynthetic$' -benchmem -benchtime=2s -count=3 -v
```

The fixture contains 20,000 synthetic orders with an integer ID and quantity, status, and a 64-byte description. Both cases query the same data with a non-indexed quantity predicate and ordered ID. This measures the actual `database/sql` pagination path, exact count for a full page, row encoding and JSON marshaling. It does **not** measure MCP transport, a remote database, SQL federation, or client/model time. Bytes allocated per operation are cumulative Go allocations, not resident memory.

| Initial rows | JSON bytes | Go bytes allocated/op (three runs) | Time/op (three runs) |
| --- | ---: | ---: | ---: |
| 100 | 8,988 | 54,989–55,089 | 447–453 µs |
| 10,000 | 879,290 | 6,171,698–6,332,325 | 13.05–13.12 ms |

For a single process-level peak-RSS observation, a separately compiled test binary was run once per page size with the same fixture and 30 iterations:

```sh
CGO_ENABLED=1 GOPROXY=off go test -tags datafabric_sqlite -c -o /tmp/df649-service.test ./internal/service
/usr/bin/time -f 'max_rss_kib=%M' /tmp/df649-service.test -test.run='^$' -test.bench='^BenchmarkFirstPageSynthetic/rows_100$' -test.benchtime=30x -test.count=1
/usr/bin/time -f 'max_rss_kib=%M' /tmp/df649-service.test -test.run='^$' -test.bench='^BenchmarkFirstPageSynthetic/rows_10000$' -test.benchtime=30x -test.count=1
```

Peak RSS was 89,444 KiB and 102,520 KiB respectively. This includes the Go runtime, test binary, SQLite fixture and setup; it is one observation, not gateway or production memory consumption.

These measurements support reducing the default response size for this fixture, not a general latency or memory claim. Both pages are full, so both invoke the exact count query. SQLite's `EXPLAIN QUERY PLAN` reported `SCAN orders` for that count. A smaller returned page therefore does not imply proportionally less source work; streaming and other drivers can behave differently. Wider rows and real workloads can change the result substantially. The benchmark is provided for reproducibility, not as a performance gate.

An in-process MCP-handler integration test uses a separate 205-row synthetic CSV source and verifies every row in both pagination and streaming modes for an omitted initial size and an explicitly requested 10,000-row page. The MCP handler contract also checks non-positive and capped sizes. These tests do not include MCP transport or a WorkBuddy client.
