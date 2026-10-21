# Delta Lake and Apache Hudi read contracts

This page records the read boundaries exposed by the table-format adapters. It does not claim that a table format provides a generic SQL or REST protocol.

## Delta Lake through Databricks

The current Delta adapter uses Unity Catalog for table discovery and Databricks SQL Statement Execution for SQL, table history, and Change Data Feed (CDF). A SQL Warehouse ID and workspace credentials are required for statement execution.

- ReadChangeDataFeed takes a catalog, schema, table, an inclusive start version, and an optional inclusive end version.
- It submits SELECT * FROM table_changes('<qualified table>', start, end) through Statement Execution. The table must have CDF enabled, and the requested history must still be retained.
- Statement execution is submitted asynchronously. The adapter polls the statement and collects all inline result chunks under the same statement ID before returning. Inline results are limited by Databricks to small results (up to 25 MiB); truncated results are rejected instead of returned as complete.
- Unity Catalog table discovery follows its continuation token. Table history is bounded to the newest 1,000 entries.
- This is a Databricks integration. It is not a direct reader for arbitrary Delta transaction-log storage, and it does not provide transactions or streaming.

## Apache Hudi

Apache Hudi has no generic REST query endpoint. The adapter separates storage metadata discovery from record reads.

### Storage metadata

The S3 metadata reader parses the table's .hoodie/hoodie.properties file, its Avro create schema, classic active timeline files, and recognized data-file paths for partition counts and byte totals.

- It reports completed .commit, .deltacommit, and .replacecommit instants, plus savepoints. It does not infer write operation types or row counts from timeline filenames.
- It supports the classic active timeline for table format versions below 8. It does not read archived timeline history or Hudi 1.x LSM timeline metadata.
- Partition listings are S3 object listings, not a transactional table snapshot. A record query must use an engine that pins the requested end instant.

### Incremental read contract

HudiIncrementalQuery passes a validated HudiIncrementalReadOptions value to a configured HudiIncrementalExecutor. The executor is responsible for translating the contract to its Hudi-capable engine.

| Option | Contract |
| --- | --- |
| BasePath | Required Hudi table path. |
| BeginInstantTime | Required and exclusive. earliest is accepted for the first range. |
| EndInstantTime | Required and inclusive; fixing this value keeps pages on one logical source range. |
| IncrementalFormat | latest_state returns the latest value per changed record; cdc requests change events. |
| SelectedFields | Optional field identifiers validated before delegation. |
| PageSize | Defaults to 1,000; accepted range is 1 to 10,000. |
| PageToken | Opaque engine token. The executor must bind it to the table, range, format, and selection. |

Hudi CDC requires hoodie.table.cdc.enabled=true and is only supported for Copy-on-Write tables by the documented Hudi contract. If the metadata reader is available, the adapter checks both conditions and rejects unsupported tables; otherwise, the engine adapter must validate the table before returning records. No configured executor means incremental reads return an explicit unsupported error rather than an empty success.
