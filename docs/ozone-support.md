# Apache Ozone support status

Data Fabric exposes one bounded Ozone Parquet object through Apache Ozone S3 Gateway. The adapter uses the gateway S3-compatible HTTP API and the shared read-only Parquet SQL path. It does not use the native Ozone Manager RPC client.

## Current operation status

| Operation | Current status |
| --- | --- |
| Create or update an ozone_parquet data source | Supported by the registered experimental adapter when endpoint, bucket, object key, and credentials validate |
| Advertise ozone_parquet through the database type API | Registered and reported as experimental |
| Query one Ozone Parquet object | Read-only SQL over a single configured Parquet object |
| Inspect the configured object | S3-compatible HEAD request to the configured S3 Gateway |
| Read the configured object | S3-compatible GET request to the configured S3 Gateway |
| List buckets or objects, write/delete/rename objects, administer volumes | Not provided by this adapter |
| Query Ozone text or Avro data | Unsupported; those types remain unregistered |
| Use Ozone-native RPC, Kerberos, or cluster-capacity APIs | Not provided |

## Configuration and limits

- database is the bucket name.
- additionalProps.endpoint_url is the S3 Gateway HTTP(S) origin. It must not include credentials, a path, query, or fragment. If no port is supplied, the adapter uses port 9878.
- host or additionalProps.object_key identifies one relative .parquet object key. object_key takes precedence.
- username and password are required S3 access-key and secret-key credentials. Requests use SigV4 and path-style bucket addressing. additionalProps.session_token may supply the paired session token.
- additionalProps.region defaults to us-east-1; additionalProps.table_name defaults to the object filename without its extension.
- The default object limit is 16 MiB; the configured maximum is 64 MiB. Each object is capped at 100,000 rows and materialized in gateway memory for the restricted SQL adapter.
- Parquet page codecs are handled by the shared decoder. An externally wrapped .gz object, directory or multi-object scan, schema evolution, and partition discovery are outside this adapter.

Deterministic tests use a local S3-protocol HTTP fixture and a Snappy-compressed Parquet object. They verify path-style requests, HEAD/GET, bounded reads, metadata discovery, read-only SQL, and write rejection. They do not establish compatibility with a live Apache Ozone release, gateway authentication configuration, TLS setup, or Kerberos. Keep this connector experimental until a version-pinned live Ozone contract has been run.
