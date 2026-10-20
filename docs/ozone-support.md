# Apache Ozone support status

Apache Ozone is not currently a supported Data Fabric connector. The Ozone client is a placeholder; its success-shaped responses do not establish connectivity or provide valid object metadata.

## Current operation status

| Operation | Current status |
| --- | --- |
| Create or update an Ozone data source | Rejected as unsupported before persistence |
| Advertise Ozone through GET /drivers | Not advertised |
| Query Ozone-backed Parquet, text, or Avro data | Not supported |
| Read, write, delete, rename, or range-read an object | Not implemented |
| List objects or retrieve object information | Placeholder only; must not be treated as a valid result |
| Check object existence or list buckets | Placeholder only; must not be treated as a valid result |
| Create a volume or bucket, or delete a bucket | Not implemented |
| Check Ozone connection or report cluster capacity | Placeholder only; must not be treated as a successful health check |

The ozone_parquet, ozone_text, and ozone_avro types are not registered in the driver registry. They therefore do not appear in GET /drivers; new create and update requests fail type validation before they reach the client stubs or persist a data source. An already stored Ozone source cannot resolve a registered driver for query execution.

The implementation can be advertised again after the object operations have context-aware requests, meaningful error and resource handling, and tests against a real or protocol-faithful Ozone endpoint.
