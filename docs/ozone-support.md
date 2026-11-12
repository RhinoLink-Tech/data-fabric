# Apache Ozone support status

Data Fabric exposes bounded Ozone Parquet, ORC, line-oriented text, Avro OCF, JSON, and CSV objects, plus a bounded Delta Lake snapshot, through Apache Ozone S3 Gateway. The adapters use the gateway S3-compatible HTTP API and shared read-only SQL paths. They do not use the native Ozone Manager RPC client.

## Current operation status

| Operation | Current status |
| --- | --- |
| Create or update an ozone_parquet, ozone_orc, ozone_text, ozone_avro, ozone_json, ozone_csv, or ozone_delta data source | Supported by the registered experimental adapter when endpoint, bucket, object or table path, and credentials validate |
| Advertise Ozone Parquet, ORC, text, Avro, JSON, CSV, and Delta Lake through the database type API | Registered and reported as experimental |
| Query one Ozone Parquet object | Read-only SQL over a single configured Parquet object |
| Query one Ozone ORC object | Read-only SQL over one configured ORC object with flat primitive columns |
| Query one Ozone text object | Read-only SQL over one UTF-8 .txt or .log object, with line_number and line columns |
| Query one Ozone Avro object | Read-only SQL over one Avro OCF object, limited to flat records with supported primitive or nullable primitive fields |
| Query one Ozone JSON object | Read-only SQL over one object, object array, or JSON Lines file with top-level scalar fields |
| Query one Ozone CSV object | Read-only SQL over one row-oriented file with configurable header, delimiter, skipped header rows, and null markers |
| Query one Ozone Delta table | Read-only SQL over the latest supported snapshot of one unpartitioned Delta table using Parquet data files |
| Inspect file object metadata | S3-compatible HEAD request to the configured S3 Gateway |
| Resolve and read Delta snapshot | S3-compatible ListObjects and bounded GET requests to the configured S3 Gateway |
| Read the configured file object | S3-compatible GET request to the configured S3 Gateway |
| List buckets or objects, write/delete/rename objects, administer volumes | Not provided by this adapter |
| Use Ozone-native RPC, Kerberos, or cluster-capacity APIs | Not provided |

## Configuration and limits

- database is the bucket name.
- additionalProps.endpoint_url is the S3 Gateway HTTP(S) origin. It must not include credentials, a path, query, or fragment. If no port is supplied, the adapter uses port 9878.
- For file formats, host or additionalProps.object_key identifies one relative .parquet, .txt, .log, .avro, supported JSON-suffix, or .csv object key for the selected source type. object_key takes precedence. For ozone_delta, host or additionalProps.table_path identifies the relative table root.
- username and password are required S3 access-key and secret-key credentials. Requests use SigV4 and path-style bucket addressing. additionalProps.session_token may supply the paired session token.
- additionalProps.region defaults to us-east-1; additionalProps.table_name defaults to the object filename without its extension or to the Delta table directory name.
- The default object limit is 16 MiB; the configured maximum is 64 MiB. Parquet and CSV objects are capped at 100,000 rows, text objects at 100,000 lines, and Avro and JSON objects at 100,000 records. Objects are materialized in gateway memory for the restricted SQL adapters.
- CSV reads one object into gateway memory before filtering and pagination; only flat row-oriented files are supported. The SQL subset is read-only.
- JSON supports object, array-of-objects, or JSON Lines input with top-level scalar fields; nested objects and arrays are rejected.
- Avro supports one OCF object with flat records containing supported primitive or nullable primitive fields; nested values, arrays, maps, unions beyond nullability, and schema registry workflows are outside the adapter.
- Parquet page codecs are handled by the shared decoder. An externally wrapped .gz object, directory or multi-object scan, schema evolution, and partition discovery are outside this adapter.
- Delta reads the latest contiguous transaction-log snapshot with reader protocol version 1, up to 999 JSON log objects (4 MiB each, 16 MiB total), 256 active Parquet files, 100,000 rows, and a default 16 MiB data limit configurable up to 64 MiB. Checkpoints, partitioning, deletion vectors, time travel, writes, and newer reader features are rejected.

Deterministic tests use local S3-protocol HTTP fixtures and verify path-style requests, SigV4, HEAD/GET/listing, bounded reads, metadata discovery, read-only SQL, and write rejection for the supported formats, including Snappy Parquet, Avro OCF, JSON, CSV, and Delta snapshots. They do not establish compatibility with a live Apache Ozone release, gateway authentication configuration, TLS setup, or Kerberos. Keep this connector experimental until a version-pinned live Ozone contract has been run.
