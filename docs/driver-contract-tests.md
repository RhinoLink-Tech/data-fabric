# Driver contract test report

## Verified baselines

The PostgreSQL, openGauss, MySQL, MariaDB, TiDB, ClickHouse, SQL Server, MongoDB, Oracle, Apache Doris, StarRocks, S3 CSV, S3 Parquet, S3 ORC, S3 Avro, S3 JSON, MinIO CSV, and MinIO Parquet drivers have repeatable versioned contract tests against exact, pinned runtime artifacts.

| Driver | Exact server version | Pinned runtime artifact | Authentication |
| --- | --- | --- | --- |
| PostgreSQL | 15.19 | postgres:15.19-alpine@sha256:54e0dd8e8cddcdbb9156c2486eed5a43eccb7214d7c6828097d41ce049fb3984 | username/password |
| PostgreSQL | 16.15 | postgres:16.15-alpine@sha256:0cefaabd882e42bcb2a2f3dc56d3ce4770560ba62fd7f197f1a2e008fd5731ab | username/password |
| PostgreSQL | 17.11 | postgres:17-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24 | username/password; TLS verify-full with test CA |
| PostgreSQL | 18.6 | postgres:18.6-alpine@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5 | username/password |
| openGauss | 6.0.0 (server reports openGauss 6.0.0 build aee4abd5) | enmotech/opengauss:6.0.0@sha256:9bd81380273944e5a02a2139c90954d4f46813b71810f7b23fe8f738014d03b5 | username/password; TLS verify-ca and verify-full with generated CA |
| MySQL | 8.0.46 | mysql:8.0@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b | username/password; TLS certificate and hostname validation with private test CA |
| MySQL | 8.4.13 | mysql:8.4.13@sha256:a0225d1ea6631c8bc2385dc4c2faba17bbf1834ecf369e1659a9a382bd8d5dd7 | username/password; TLS certificate and hostname validation with private test CA |
| MariaDB | 10.11.19 | mariadb:10.11.19@sha256:d769e37960821dfdcf096954f6e7ac89f747e116c1bba98e48edd265dfc7deae | username/password; TLS certificate and hostname validation with private test CA |
| MariaDB | 11.4.13 | mariadb:11.4.13@sha256:d1891ede5a559f1086f55053bb8cc1569b0298338c4d72d71725a7f74f6ba620 | username/password; TLS certificate and hostname validation with private test CA |
| MariaDB | 11.8.9 | mariadb:11@sha256:79d59758afc91b89b120b0a8904d637f5a3b3e1c4900f29b740d6d46c72fef68 | username/password; TLS certificate and hostname validation with private test CA |
| MariaDB | 12.3.3 | mariadb:12.3.3@sha256:d46e102fa8b9b72bfcdeb56f9dd538742092119dc8b11f31fe662605cd234cd9 | username/password; TLS certificate and hostname validation with private test CA |
| TiDB | 8.5.8 (server reports 8.0.11-TiDB-v8.5.8) | pingcap/tidb:v8.5.8@sha256:df168c764bf2dfdb166dc37a5c3b0e210d29d5f3ab2d33317fd0fdf7b32037f5 | username/password; TLS certificate and hostname validation with test CA |
| ClickHouse | 26.8.20.9 | clickhouse/clickhouse-server:26.8.20.9-alpine@sha256:1e67397fa4828e8dbb95b3bcc37a8822749ca5e46bd208b3ef336f365c522688 | username/password; TLS verification with private test CA |
| SQL Server | 16.0.4305.5 | mcr.microsoft.com/mssql/server:2022-latest@sha256:cf8f0ab2ebe61feec2eed2629061a07d247f791c2a8ddda74a4ac9f06a291718 | SQL authentication; TLS certificate and hostname validation with generated CA |
| MongoDB | 8.0.34 | mongo:8.0.34-noble@sha256:c2d54fddfb3738fb27fe117f51692e3f44e640a3e7aeb8da3937ee9cd33a650b | database-scoped username and password |
| Oracle | 23.26.3.0.0 | ghcr.io/gvenzl/oracle-free:23.26.3-slim@sha256:366fe85702d764d81a0272b72e94e629290c27aa72a47825fcc45701a0633dac | application schema user; SYSTEM for version probe |
| Apache Doris | 4.1.3 | apache/doris:all-in-one-4.1.3@sha256:4d3aef8f4fe63f6e32b493b4fd7b2a682d19e2c800d1d812131e67aaba0d212a | root for fixture setup; database-scoped username and password for the driver |
| StarRocks | 4.1.6 | starrocks/allin1-ubuntu:4.1.6@sha256:88d1192620d03cf7ae8d5ece0b02da1024372d33bc411e5a51e2d3b003c551b3 | root for fixture setup; table-scoped username and password for the driver |
| S3 CSV | Garage v2.4.1 | dxflrs/garage:v2.4.1@sha256:9c96caa2612d3411acc5b0e6701fb238dbfba33e533a6d7d3d811a4b12d0d020 | root access key and secret key (SigV4) |
| S3 Parquet | Garage v2.4.1 | dxflrs/garage:v2.4.1@sha256:9c96caa2612d3411acc5b0e6701fb238dbfba33e533a6d7d3d811a4b12d0d020 | root access key and secret key (SigV4) |
| S3 ORC | Garage v2.4.1 | dxflrs/garage:v2.4.1@sha256:9c96caa2612d3411acc5b0e6701fb238dbfba33e533a6d7d3d811a4b12d0d020 | root access key and secret key (SigV4) |
| S3 Avro OCF | Garage v2.4.1 | dxflrs/garage:v2.4.1@sha256:9c96caa2612d3411acc5b0e6701fb238dbfba33e533a6d7d3d811a4b12d0d020 | root access key and secret key (SigV4) |
| S3 JSON | Garage v2.4.1 | dxflrs/garage:v2.4.1@sha256:9c96caa2612d3411acc5b0e6701fb238dbfba33e533a6d7d3d811a4b12d0d020 | root access key and secret key (SigV4) |
| MinIO CSV | RELEASE.2025-04-22T22-12-26Z | [upstream source commit 0d7408fc9969caf07de6a8c3a84f9fbb10a6739e](https://github.com/minio/minio/tree/0d7408fc9969caf07de6a8c3a84f9fbb10a6739e), archive SHA-256 `7eb30a913fea30f18069abf194e1e78e4983b558cc526911ae1c11396a9859a5` | root access key and secret key |
| MinIO Parquet | RELEASE.2025-04-22T22-12-26Z | [upstream source commit 0d7408fc9969caf07de6a8c3a84f9fbb10a6739e](https://github.com/minio/minio/tree/0d7408fc9969caf07de6a8c3a84f9fbb10a6739e), archive SHA-256 `7eb30a913fea30f18069abf194e1e78e4983b558cc526911ae1c11396a9859a5` | root access key and secret key |

The main CI job publishes pass, skip, failure, and version/product-mismatch results for its six configured live database contracts in the GitHub Actions step summary. Its configured baselines must all run and pass; a missing endpoint or skipped contract cannot satisfy the CI check. A separate PostgreSQL matrix runs the same driver contract against PostgreSQL 15.19, 16.15, and 18.6; the main job covers PostgreSQL 17.11. A separate MySQL job covers 8.4.13; the main job covers MySQL 8.0.46 and MariaDB 11.8.9. A separate MariaDB driver job covers 10.11.19; the MariaDB TLS jobs cover 11.8.9 and 10.11.19. Isolated SQL Server, MongoDB, Oracle, Doris, StarRocks, Garage, and MinIO jobs print verbose per-contract outcomes. Locally, an unconfigured endpoint is reported as skipped. A server outside the exact baseline fails the version assertion; other versions and compatibility modes remain unverified unless listed here.


The PostgreSQL TLS contract starts a pinned PostgreSQL 17.11 server with a generated CA and a certificate for `localhost`. It verifies TLS with `sslmode=verify-full`, rejects an untrusted CA and a hostname mismatch, confirms legacy `ssl: true` still encrypts without certificate verification, and confirms legacy `ssl: false` still uses a non-TLS connection. Run it locally with `bash scripts/contracts/run_postgresql_tls_contract.sh`; it creates and removes its temporary Docker image and container.

The MySQL TLS contract runs against pinned MySQL 8.0.46 and 8.4.13 images. Each contract starts its server with an ephemeral certificate, verifies the certificate and hostname with a trusted private CA, rejects an untrusted CA and hostname mismatch, confirms system roots reject the private CA when no custom CA is configured, and confirms `ssl: false` remains plaintext. Run `bash scripts/contracts/run_mysql_tls_contract.sh` for MySQL 8.0.46; set `DATA_FABRIC_MYSQL_TLS_IMAGE` and `DATA_FABRIC_MYSQL_TLS_EXPECTED_VERSION` to select another pinned image, as the 8.4.13 CI job does.

The MariaDB TLS contract starts the pinned MariaDB 11.8.9 image with an ephemeral server certificate. It exercises the shared MySQL-protocol driver, verifies a trusted private CA and hostname, rejects an untrusted CA and hostname mismatch, confirms system roots reject the test CA when no custom CA is configured, and confirms `ssl: false` remains plaintext. Run it locally with `bash scripts/contracts/run_mariadb_tls_contract.sh`; it creates and removes its temporary Docker image and container.

The same TLS identity contract also runs against the pinned MariaDB 10.11.19 image in a separate CI job. To select another pinned image locally, set DATA_FABRIC_MARIADB_TLS_IMAGE and DATA_FABRIC_MARIADB_TLS_EXPECTED_VERSION before running the script.

The TiDB TLS contract starts the same pinned TiDB v8.5.8 release with an ephemeral server certificate. It verifies TLS with a trusted test CA, rejects an untrusted CA and a hostname mismatch, confirms system roots reject the test CA when no custom CA is configured, and confirms ssl: false remains plaintext. Run it locally with bash scripts/contracts/run_tidb_tls_contract.sh; it creates and removes its temporary Docker image and container.

The openGauss TLS contract starts the pinned 6.0.0 image with a generated server certificate. It verifies a trusted CA with verify-full and verify-ca, rejects an untrusted CA and hostname mismatch, confirms system roots reject the private CA, and checks that legacy ssl: true still requires encryption while ssl: false disables TLS. Run it with bash scripts/contracts/run_opengauss_tls_contract.sh; it creates and removes its temporary container and certificates.

The suites cover connection, SQL execution, value scanning, pagination, cancellation, and error handling where those operations apply. The Doris and StarRocks contracts also call the application's table metadata extractor against the live server, including column names, types, and nullability. The PostgreSQL, openGauss, MySQL, and MariaDB suites also verify database-side read-only session enforcement. The TiDB and SQL Server suites verify that database-side read-only initialization is unsupported for the tested version. The ClickHouse suite uses its native protocol; its separate TLS contract verifies trusted private-CA connections and rejects untrusted certificates and hostname mismatches. The MongoDB suite verifies the restricted SQL adapter against a real MongoDB server, including a database-scoped account, projections, filters, ordering, pagination, count, NULL values, and rejection of writes and unsupported SQL. The Oracle suite verifies the go-ora driver against Oracle AI Database 26ai Free with a dedicated application schema account; SYSTEM is used only to read the exact server version. It covers bind parameters, value scanning, DML, count, Oracle pagination, and database errors. The S3-compatible Garage contracts validate the CSV, flat-schema Parquet, flat-schema ORC, and flat-record Avro OCF and S3 JSON adapters for object, array, and JSON Lines documents, and the MinIO object-storage contracts validate both CSV and flat-schema Parquet unified query adapters against the same pinned MinIO source build, including metadata and health checks, query results, pagination, counts, and write rejection. Each suite checks the exact server product and version before exercising its adapter.

These test containers use test-only accounts, databases, and ephemeral TLS keys. They contain no production credentials. PostgreSQL 15.19, 16.15, 17.11, and 18.6; MySQL 8.0.46 and 8.4.13; and MariaDB 10.11.19, 11.4.13, 11.8.9, and 12.3.3 are covered by driver contracts. TLS certificate validation is covered for PostgreSQL 17.11; MySQL 8.0.46 and 8.4.13; and MariaDB 10.11.19, 11.4.13, 11.8.9, and 12.3.3. openGauss 6.0.0, TiDB v8.5.8, and ClickHouse 26.8.20.9 certificate validation are also covered by pinned TLS contracts. TLS validation for other MariaDB versions, other listed baselines, and PostgreSQL versions remains unverified.

The MinIO contracts build the pinned upstream source archive locally because the historical upstream container image is no longer published. The script verifies the archive SHA-256, checks the built binary's release identifier, then runs the CSV and Parquet fixtures against that MinIO process. The S3 CSV, Parquet, ORC, Avro OCF, and JSON contracts run against the pinned Garage v2.4.1 image.

## Run the S3-compatible object-storage contracts locally

The script starts the pinned Garage v2.4.1 container and runs both the CSV and
Parquet contracts with temporary root credentials:

~~~~sh
bash scripts/contracts/run_s3_csv_contract.sh
~~~~

## Run the MinIO object-storage contracts locally

The script needs Bash, `curl`, `tar`, `sha256sum`, and the Go version declared in this repository's `go.mod`. It downloads and verifies the fixed MinIO source artifact, starts an isolated local process, and removes it when the test ends:

~~~~sh
bash scripts/contracts/run_minio_object_storage_contract.sh
~~~~

The support catalog records Garage v2.4.1 S3 CSV, S3 Parquet, S3 ORC, S3 Avro, and S3 JSON, PostgreSQL 15.19, 16.15, 17.11, and 18.6, openGauss 6.0.0, MySQL 8.0.46 and 8.4.13, MariaDB 10.11.19, 11.4.13, 11.8.9, and 12.3.3, TiDB 8.5.8, ClickHouse 26.8.20.9, SQL Server 16.0.4305.5, MongoDB 8.0.34, Oracle AI Database 26ai Free 23.26.3.0.0, Apache Doris 4.1.3, StarRocks 4.1.6, MinIO CSV, and MinIO Parquet as versioned contract evidence. MongoDB and both MinIO adapters remain experimental because their evidence applies to restricted adapter boundaries. Other connector types and versions retain their existing catalog status until a pinned server contract is added.

## Apache Druid protocol fixture

Apache Druid has a unified, read-only SQL gateway path but is not part of the
versioned baseline table. The protocol fixture and query-service test exercise
the Router/Broker status endpoint, optional HTTP basic authentication, the
`/druid/v2/sql` request shape, dynamic parameters, array responses with column
and SQL type headers, type normalization, pagination, and write rejection.

This is not evidence for a Druid server version. TLS behavior, asynchronous
statements, propagation of gateway cancellation to Druid's cancel endpoint,
native JSON queries, and coordinator metadata are outside this fixture. The
adapter remains experimental until a pinned live Druid contract is added and recorded in the support catalog.

## S3 Delta local protocol fixture

The S3 Delta fixture uses a local HTTP server and the AWS SDK to exercise
SigV4-signed bucket-in-path requests, Delta log listing and reads, snapshot
reconstruction, health checks, metadata discovery, restricted read-only SQL,
filtering, ordering, pagination, COUNT(*), cancellation, and write rejection.
Run it with:

~~~~sh
go test ./internal/database/drivers/object_storage -run 'TestS3DeltaProtocolFixture$' -count=1 -v
~~~~

This local fixture does not run against AWS S3 or an S3-compatible service and
does not establish a live provider version contract. The adapter remains
experimental.

## MinIO Delta protocol fixture

The MinIO Delta fixture exercises MinIO's S3-compatible SDK against a local
HTTP server. It covers signed log listing and reads, snapshot reconstruction,
health checks, metadata discovery, restricted read-only SQL filtering/order/
pagination/count, cancellation, and write rejection. The query-service fixture
also exercises pagination. Run the fixtures directly with:

~~~~sh
go test ./internal/database/drivers/object_storage ./internal/service -run 'MinIODelta' -count=1 -v
~~~~

These fixtures do not run against a live MinIO deployment and do not establish a
MinIO release contract. The adapter remains experimental.

## Azure Blob Parquet protocol fixture

The Azure Blob Parquet fixture uses the Azure SDK against a local HTTP server.
It exercises Shared Key request signing, SAS query-token propagation, blob
properties, health checks, metadata discovery, read-only filtering/order/
pagination/count queries, cancellation, and write rejection for both registered
type names. Run it directly with:

~~~~sh
go test ./internal/database/drivers/object_storage -run 'Test(AzureBlobParquetProtocolFixture|AzureParquetTypeAliasSharesBlobSQLPath)$' -count=1 -v
~~~~

This fixture does not run against Azure Storage and is not a live version
contract. TLS, cloud account behavior, and SAS authorization enforcement remain
unverified.

## MinIO JSON local S3-compatible fixture

The MinIO JSON fixture serves a JSON object, a JSON array, and JSON Lines from a
local HTTP server that checks SigV4 authorization, bucket-in-path addressing,
object metadata requests, and full-object reads. It exercises source validation,
health, metadata discovery, schema inference, read-only filtering, ordering,
COUNT(*), schema-only queries, and write rejection through the configured
database/sql adapter. It is local protocol coverage, not a live MinIO release
contract. Run it with:

~~~~sh
go test ./internal/database/drivers/object_storage -run 'MinIOJSON' -count=1 -v
~~~~

## MinIO Avro OCF local S3-compatible fixture

The MinIO Avro fixture serves a deterministic OCF sample through a local HTTP
server that checks SigV4 authorization, bucket-in-path addressing, object
metadata requests, and full-object reads. It covers source validation, health,
metadata discovery, filtering, ordering, pagination, COUNT(*), schema discovery,
and write rejection through the configured database/sql adapter. It is local
protocol coverage, not a live MinIO release contract. Run it with:

~~~~sh
go test ./internal/database/drivers/object_storage -run 'MinIOAvro' -count=1 -v
~~~~

## MinIO ORC local S3-compatible fixture

The MinIO ORC fixture serves the committed ORC sample through a local HTTP
server that checks SigV4 authorization, bucket-in-path addressing, object
metadata requests, and byte-range reads. It exercises source validation,
health, metadata discovery, SQL filtering/order/count, and write rejection
through the configured database/sql adapter. This fixture is local protocol
coverage, not a live MinIO release contract. Run it with:

~~~~sh
go test ./internal/database/drivers/object_storage -run 'MinIOORC' -count=1 -v
~~~~

## MinIO Iceberg local catalog fixture

The MinIO Iceberg fixture routes a configured source through a local Iceberg
REST catalog and an Iceberg table stored in a temporary local warehouse. It
covers catalog health, configured-table metadata, schema discovery, read-only
filtering and pagination, and table-boundary enforcement. A separate config
fixture verifies how MinIO endpoint and credentials map into S3 FileIO
properties. These fixtures do not call a MinIO service or establish live
MinIO/REST Catalog compatibility. Run them with:

~~~~sh
go test ./internal/database/drivers/object_storage -run 'MinIOIceberg' -count=1 -v
~~~~

## Azure Blob Delta protocol fixture

The Azure Delta fixture exercises the Azure SDK against a local HTTP server. It
covers Shared Key signing, SAS query-token propagation, Delta snapshot listing
and replay, active Parquet reads, health checks, schema discovery, read-only SQL
filtering/order/pagination/count, cancellation, and write rejection. The
query-service fixture also exercises batch pagination. Run the fixtures with:

~~~~sh
go test ./internal/database/drivers/object_storage ./internal/service -run 'AzureDelta' -count=1 -v
~~~~

These fixtures do not run against Azure Storage and do not establish a live
Azure version contract. The adapter remains experimental.

## Tencent COS Parquet protocol fixture

The COS Parquet adapter has deterministic protocol-fixture coverage using the
Tencent COS SDK and its request signing transport against a local HTTP server.
The fixture exercises signed object reads, metadata and health checks, schema
discovery, read-only SQL filtering/order/pagination/count behavior, request
cancellation, and write rejection. Run it directly with:

~~~~sh
go test ./internal/database/drivers/object_storage -run '^TestCOSParquetProtocolFixture$' -count=1 -v
~~~~

This fixture does not run against Tencent Cloud and does not establish
compatibility with a live COS release or temporary credentials. The connector
remains experimental pending a version-pinned live contract.

## Alibaba OSS Delta protocol fixture

The OSS Delta fixture exercises the SDK against a local HTTP object-store
server. It covers transaction-log listing and replay, active Parquet reads,
health checks, table metadata, read-only SQL filtering/order/pagination/count,
cancellation, and write rejection. The query-service fixture also exercises
batch pagination. Run the focused fixtures directly with:

~~~~sh
go test ./internal/database/drivers/object_storage ./internal/service -run 'OSSDelta' -count=1 -v
~~~~

These fixtures do not run against Alibaba Cloud and do not establish a live OSS
release contract. The adapter remains experimental.

## Alibaba OSS Parquet protocol fixture

The OSS Parquet adapter has deterministic protocol-fixture coverage using the
Alibaba OSS SDK against a local HTTP server. The fixture checks access-key
request construction, object metadata and health checks, schema discovery,
read-only SQL filtering/order/pagination/count behavior, and write rejection.
It is included in the normal Go test suite and can be run directly with:

~~~~sh
go test ./internal/database/drivers/object_storage -run '^TestOSSParquetProtocolFixture$' -count=1 -v
~~~~

The fixture does not run against Alibaba Cloud and is not evidence for any OSS
service version, deployment mode, TLS configuration, or STS credential flow.
The connector remains experimental until a live, version-pinned contract is
available.

## Run the PostgreSQL contract locally

Start the pinned test service:

~~~~sh
docker run --rm --detach --name data-fabric-postgres-contract \
  --publish 5432:5432 \
  --env POSTGRES_USER=data_fabric_contract \
  --env POSTGRES_PASSWORD=contract-test-only \
  --env POSTGRES_DB=data_fabric_contract \
  postgres:17-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24
~~~~

Wait for PostgreSQL to accept connections, then run:

~~~~sh
export DATA_FABRIC_POSTGRES_CONTRACT_DSN='postgres://data_fabric_contract:contract-test-only@localhost:5432/data_fabric_contract?sslmode=disable'
export DATA_FABRIC_POSTGRES_EXPECTED_VERSION='17.11'
go test ./internal/database/drivers/traditional -run '^TestPostgreSQLDriverContract$' -count=1 -v
~~~~

Stop the test service when finished:

~~~~sh
docker stop data-fabric-postgres-contract
~~~~

## Run the MySQL contract locally

Start the pinned test service:

~~~~sh
docker run --rm --detach --name data-fabric-mysql-contract \
  --publish 3306:3306 \
  --env MYSQL_ROOT_PASSWORD=root-contract-only \
  --env MYSQL_USER=data_fabric_contract \
  --env MYSQL_PASSWORD=contract-test-only \
  --env MYSQL_DATABASE=data_fabric_contract \
  mysql:8.0@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b
~~~~

Wait for MySQL to accept connections, then run:

~~~~sh
export DATA_FABRIC_MYSQL_CONTRACT_HOST='127.0.0.1'
export DATA_FABRIC_MYSQL_CONTRACT_PORT='3306'
export DATA_FABRIC_MYSQL_CONTRACT_USERNAME='data_fabric_contract'
export DATA_FABRIC_MYSQL_CONTRACT_PASSWORD='contract-test-only'
export DATA_FABRIC_MYSQL_CONTRACT_DATABASE='data_fabric_contract'
export DATA_FABRIC_MYSQL_EXPECTED_VERSION='8.0.46'
go test ./internal/database/drivers/traditional -run '^TestMySQLDriverContract$' -count=1 -v
~~~~

Stop the test service when finished:

~~~~sh
docker stop data-fabric-mysql-contract
~~~~

## Run the MariaDB contract locally

Start the pinned test service:

~~~~sh
docker run --rm --detach --name data-fabric-mariadb-contract \
  --publish 3306:3306 \
  --env MARIADB_ROOT_PASSWORD=root-contract-only \
  --env MARIADB_USER=data_fabric_contract \
  --env MARIADB_PASSWORD=contract-test-only \
  --env MARIADB_DATABASE=data_fabric_contract \
  mariadb:11@sha256:79d59758afc91b89b120b0a8904d637f5a3b3e1c4900f29b740d6d46c72fef68
~~~~

Wait for MariaDB to accept connections, then run:

~~~~sh
export DATA_FABRIC_MARIADB_CONTRACT_HOST='127.0.0.1'
export DATA_FABRIC_MARIADB_CONTRACT_PORT='3306'
export DATA_FABRIC_MARIADB_CONTRACT_USERNAME='data_fabric_contract'
export DATA_FABRIC_MARIADB_CONTRACT_PASSWORD='contract-test-only'
export DATA_FABRIC_MARIADB_CONTRACT_DATABASE='data_fabric_contract'
export DATA_FABRIC_MARIADB_EXPECTED_VERSION='11.8.9'
go test ./internal/database/drivers/traditional -run '^TestMariaDBDriverContract$' -count=1 -v
~~~~

Stop the test service when finished:

~~~~sh
docker stop data-fabric-mariadb-contract
~~~~

## Run the ClickHouse contract locally

Start the pinned test service:

~~~~sh
docker run --rm --detach --name data-fabric-clickhouse-contract \
  --ulimit nofile=262144:262144 \
  --publish 9000:9000 \
  --env CLICKHOUSE_DB=data_fabric_contract \
  --env CLICKHOUSE_USER=data_fabric_contract \
  --env CLICKHOUSE_PASSWORD=contract-test-only \
  --env CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1 \
  clickhouse/clickhouse-server:26.8.20.9-alpine@sha256:1e67397fa4828e8dbb95b3bcc37a8822749ca5e46bd208b3ef336f365c522688
~~~~

Wait for ClickHouse to accept connections, then run:

~~~~sh
export DATA_FABRIC_CLICKHOUSE_CONTRACT_HOST='127.0.0.1'
export DATA_FABRIC_CLICKHOUSE_CONTRACT_PORT='9000'
export DATA_FABRIC_CLICKHOUSE_CONTRACT_USERNAME='data_fabric_contract'
export DATA_FABRIC_CLICKHOUSE_CONTRACT_PASSWORD='contract-test-only'
export DATA_FABRIC_CLICKHOUSE_CONTRACT_DATABASE='data_fabric_contract'
export DATA_FABRIC_CLICKHOUSE_EXPECTED_VERSION='26.8.20.9'
go test ./internal/database/drivers/olap -run '^TestClickHouseDriverContract$' -count=1 -v
~~~~

Stop the test service when finished:

~~~~sh
docker stop data-fabric-clickhouse-contract
~~~~

## Run the ClickHouse TLS contract locally

The contract builds the same pinned ClickHouse image with a temporary CA and a
server certificate for `127.0.0.1`, starts its secure native port, verifies a
trusted connection, and checks that system roots, an untrusted CA, and a
hostname mismatch are rejected. The container, image, and generated keys are
removed when the script exits:

~~~~sh
bash scripts/contracts/run_clickhouse_tls_contract.sh
~~~~

## Run the TiDB contract locally

Start the pinned single-server test service:

~~~~sh
docker run --rm --detach --name data-fabric-tidb-contract --publish 4000:4000 --publish 10080:10080 pingcap/tidb:v8.5.8@sha256:df168c764bf2dfdb166dc37a5c3b0e210d29d5f3ab2d33317fd0fdf7b32037f5
~~~~

Wait for TiDB to accept connections. The contract test uses the disposable container root account to create its test database and a password-authenticated test user, then run:

~~~~sh
export DATA_FABRIC_TIDB_CONTRACT_HOST='127.0.0.1'
export DATA_FABRIC_TIDB_CONTRACT_PORT='4000'
export DATA_FABRIC_TIDB_CONTRACT_USERNAME='data_fabric_contract'
export DATA_FABRIC_TIDB_CONTRACT_PASSWORD='contract-test-only'
export DATA_FABRIC_TIDB_CONTRACT_DATABASE='data_fabric_contract'
export DATA_FABRIC_TIDB_EXPECTED_VERSION='8.0.11-TiDB-v8.5.8'
go test ./internal/database/drivers/domestic -run '^TestTiDBDriverContract$' -count=1 -v
~~~~

Stop the test service when finished:

~~~~sh
docker stop data-fabric-tidb-contract
~~~~

## Run the openGauss contract locally

The openGauss container needs privileged mode and a password meeting openGauss complexity requirements:

~~~~sh
docker run --rm --detach --name data-fabric-opengauss-contract \
  --privileged=true \
  --publish 5439:5432 \
  --env GS_PASSWORD='DataFabric_2026!' \
  enmotech/opengauss:6.0.0@sha256:9bd81380273944e5a02a2139c90954d4f46813b71810f7b23fe8f738014d03b5
~~~~

Wait for openGauss to accept connections, then run:

~~~~sh
export DATA_FABRIC_OPENGAUSS_CONTRACT_HOST='127.0.0.1'
export DATA_FABRIC_OPENGAUSS_CONTRACT_PORT='5439'
export DATA_FABRIC_OPENGAUSS_CONTRACT_USERNAME='gaussdb'
export DATA_FABRIC_OPENGAUSS_CONTRACT_PASSWORD='DataFabric_2026!'
export DATA_FABRIC_OPENGAUSS_CONTRACT_DATABASE='postgres'
export DATA_FABRIC_OPENGAUSS_EXPECTED_VERSION='openGauss 6.0.0 build aee4abd5'
go test ./internal/database/drivers/domestic -run '^TestOpenGaussDriverContract$' -count=1 -v
~~~~

Stop the test service when finished:

~~~~sh
docker stop data-fabric-opengauss-contract
~~~~

openGauss uses the PostgreSQL wire protocol in this contract. Its build-specific product signature is recorded separately from the PostgreSQL-compatible protocol version. The separate TLS contract below covers client-side certificate verification only for the pinned openGauss build; other openGauss versions are not covered.

## Run the openGauss TLS contract locally

The TLS contract requires Docker, OpenSSL, and the Go version declared in go.mod. It starts the pinned openGauss 6.0.0 image, creates a temporary CA and localhost server certificate, configures TLS on the server, runs both the TLS verification and regular driver contracts, then removes the container and generated certificates:

~~~~sh
bash scripts/contracts/run_opengauss_tls_contract.sh
~~~~


## Run the SQL Server contract locally

On an x86-64 Linux host with Docker, OpenSSL, and Go installed, run:

~~~~sh
bash scripts/contracts/run_sqlserver_tls_contract.sh
~~~~

The script builds the pinned SQL Server image with a short-lived test certificate for localhost, runs the SQL Server query contract, and checks verified TLS, untrusted-CA rejection, hostname-mismatch rejection, the explicit TrustServerCertificate compatibility path, and that ssl=false leaves query traffic unencrypted (the login packet may still use TLS). It removes the temporary container and image when the run finishes.

Database-side read-only session enforcement is not supported by this SQL Server adapter. Use a SQL Server account with only the privileges the gateway needs. The test-only TrustServerCertificate path deliberately bypasses certificate verification; the separate verified-TLS cases keep that option disabled. Other SQL Server versions remain outside this contract.

TiDB 8.5.8 does not implement the generic session read-only statement; configure server-side permissions for read-only accounts. ClickHouse uses the native protocol and also does not use the gateway read-only session initializer.

## Run the MongoDB contract locally

Start the pinned MongoDB 8.0.34 image on local port 27018:

~~~~sh
docker run --rm --detach --name data-fabric-mongodb-contract \
  --publish 27018:27017 \
  --env MONGO_INITDB_ROOT_USERNAME=contract_admin \
  --env MONGO_INITDB_ROOT_PASSWORD=root-contract-only \
  mongo:8.0.34-noble@sha256:c2d54fddfb3738fb27fe117f51692e3f44e640a3e7aeb8da3937ee9cd33a650b
~~~~

The contract provisions a temporary database-scoped account with read/write access, then connects through the gateway using that account. Run:

~~~~sh
export DATA_FABRIC_MONGODB_CONTRACT_HOST='127.0.0.1'
export DATA_FABRIC_MONGODB_CONTRACT_PORT='27018'
export DATA_FABRIC_MONGODB_CONTRACT_USERNAME='data_fabric_contract'
export DATA_FABRIC_MONGODB_CONTRACT_PASSWORD='contract-test-only'
export DATA_FABRIC_MONGODB_CONTRACT_DATABASE='data_fabric_contract'
export DATA_FABRIC_MONGODB_BOOTSTRAP_USERNAME='contract_admin'
export DATA_FABRIC_MONGODB_BOOTSTRAP_PASSWORD='root-contract-only'
export DATA_FABRIC_MONGODB_EXPECTED_VERSION='8.0.34'
go test ./internal/database/drivers/nosql -run '^TestMongoDriverContract$' -count=1 -v
~~~~

Stop the test service when finished:

~~~~sh
docker stop data-fabric-mongodb-contract
~~~~

MongoDB does not speak SQL. The contract verifies only the gateway's currently implemented single-collection SQL subset; joins, OR predicates, grouping, schema discovery, and transactions remain outside this adapter. CI runs eleven pinned database contracts across six jobs. For the exact support boundary, see [the generated data source support catalog](data-source-support.md).

For additional connector profiles, pinned versions, and deferred cases, consult the support catalog and prioritize a focused issue when there is a concrete user need.

## Run the Oracle contract locally

Start Oracle AI Database Free 26ai with its application schema user:

~~~~sh
docker run --rm --detach --name data-fabric-oracle-contract \
  --publish 1521:1521 \
  --env ORACLE_PASSWORD='Contract2026Admin' \
  --env APP_USER='data_fabric_contract' \
  --env APP_USER_PASSWORD='Contract2026Test' \
  ghcr.io/gvenzl/oracle-free:23.26.3-slim@sha256:366fe85702d764d81a0272b72e94e629290c27aa72a47825fcc45701a0633dac
~~~~

Wait until the container health check reports ready, then run:

~~~~sh
export DATA_FABRIC_ORACLE_CONTRACT_HOST='127.0.0.1'
export DATA_FABRIC_ORACLE_CONTRACT_PORT='1521'
export DATA_FABRIC_ORACLE_CONTRACT_USERNAME='data_fabric_contract'
export DATA_FABRIC_ORACLE_CONTRACT_PASSWORD='Contract2026Test'
export DATA_FABRIC_ORACLE_CONTRACT_SERVICE='FREEPDB1'
export DATA_FABRIC_ORACLE_ADMIN_USERNAME='system'
export DATA_FABRIC_ORACLE_ADMIN_PASSWORD='Contract2026Admin'
export DATA_FABRIC_ORACLE_EXPECTED_VERSION='23.26.3.0.0'
go test ./internal/database/drivers/traditional -run '^TestOracleDriverContract$' -count=1 -v
~~~~

Stop the test service when finished:

~~~~sh
docker stop data-fabric-oracle-contract
~~~~

The Oracle baseline verifies Oracle AI Database 26ai Free 23.26.3.0.0 only. It does not imply compatibility with licensed editions or other Oracle releases. TLS certificate behavior and database-side read-only session enforcement are not covered.


## Run the Apache Doris contract locally

Start the pinned single-container service:

~~~~sh
docker run --rm --detach --name data-fabric-doris-contract \
  --publish 9030:9030 \
  apache/doris:all-in-one-4.1.3@sha256:4d3aef8f4fe63f6e32b493b4fd7b2a682d19e2c800d1d812131e67aaba0d212a
~~~~

Wait for Doris to accept connections, then run the driver and application metadata contracts:

~~~~sh
export DATA_FABRIC_DORIS_CONTRACT_HOST='127.0.0.1'
export DATA_FABRIC_DORIS_CONTRACT_PORT='9030'
export DATA_FABRIC_DORIS_CONTRACT_USERNAME='data_fabric_contract'
export DATA_FABRIC_DORIS_CONTRACT_PASSWORD='Contract2026Test!'
export DATA_FABRIC_DORIS_CONTRACT_DATABASE='data_fabric_contract'
export DATA_FABRIC_DORIS_EXPECTED_VERSION='doris-4.1.3'
go test ./internal/database/drivers/olap -run '^TestDorisDriverContract$' -count=1 -v
go test ./internal/database/metadata -run '^TestDorisMetadataExtractorContract$' -count=1 -v
~~~~

Stop the test service when finished:

~~~~sh
docker stop data-fabric-doris-contract
~~~~

## Run the StarRocks contract locally

The contract uses the official StarRocks 4.1.6 single-container image. It creates a disposable database, fixture table, and password-authenticated user with SELECT permission on that table.

~~~~sh
docker run --rm --detach --name data-fabric-starrocks-contract \
  --publish 9030:9030 \
  starrocks/allin1-ubuntu:4.1.6@sha256:88d1192620d03cf7ae8d5ece0b02da1024372d33bc411e5a51e2d3b003c551b3
~~~~

Wait until the service health check reports ready, then run:

~~~~sh
export DATA_FABRIC_STARROCKS_CONTRACT_HOST='127.0.0.1'
export DATA_FABRIC_STARROCKS_CONTRACT_PORT='9030'
export DATA_FABRIC_STARROCKS_CONTRACT_USERNAME='data_fabric_contract'
export DATA_FABRIC_STARROCKS_CONTRACT_PASSWORD='Contract2026Test!'
export DATA_FABRIC_STARROCKS_CONTRACT_DATABASE='data_fabric_contract'
export DATA_FABRIC_STARROCKS_EXPECTED_VERSION='4.1.6'
go test ./internal/database/drivers/olap -run '^TestStarRocksDriverContract$' -count=1 -v
go test ./internal/database/metadata -run '^TestStarRocksMetadataExtractorContract$' -count=1 -v
~~~~

Stop the test service when finished:

~~~~sh
docker stop data-fabric-starrocks-contract
~~~~

The StarRocks baseline verifies the pinned 4.1.6 image only. The password-authenticated query user receives table-scoped SELECT privileges; TLS, writes through that user, multi-node deployments, and other StarRocks releases are not covered.

## HDFS CSV local file-client fixture

The HDFS CSV adapter exposes one configured CSV file through the shared
read-only query service. Its deterministic local file-client fixture covers
bounded file reads, a simple projection/filter/order/page query, COUNT(*),
metadata discovery, and write rejection without requiring a running HDFS
cluster. It does not certify NameNode/DataNode interoperability, HDFS release
compatibility, authorization behavior, Kerberos, HA failover, or large-file
performance. Keep HDFS CSV experimental until a versioned live-cluster contract
is available.

## HDFS Parquet local file-client fixture

The HDFS Parquet adapter reads a single configured file through the shared
read-only SQL path and uses the existing Parquet decoder. A deterministic local
Parquet fixture and injected file client cover projected and filtered SQL,
ordering and pagination, COUNT(*), metadata discovery, configured file bounds,
and write rejection. This fixture requires no HDFS service and does not certify
NameNode/DataNode interoperability, a live HDFS release, authorization,
Kerberos, HA behavior, or large-file performance. The connector remains
experimental until a pinned live HDFS contract is available.

## HDFS Avro OCF local file-client fixture

A deterministic Avro OCF fixture and injected local file client cover record
decoding, nullable fields, read-only projection/filter/order/page queries,
COUNT(*), metadata discovery, file and record limits, cancellation/error
handling, and write rejection without requiring HDFS. The fixture does not
certify NameNode/DataNode interoperability, a live HDFS release, authorization,
Kerberos, HA behavior, or large-file performance. Keep the connector
experimental until a pinned live HDFS contract is available.

## HDFS plain-text local file-client fixture

A deterministic UTF-8 file fixture and injected HDFS file client cover LF and
CRLF splitting, blank lines, a final line without a newline, read-only SQL
projection/filter/order/page queries, COUNT(*), metadata discovery, byte and
line limits, invalid UTF-8, cancellation, I/O errors, and write rejection
without an HDFS service. The fixture does not establish compatibility with a
live HDFS release, authorization, Kerberos, HA behavior, or large-file
performance. The connector remains experimental until a pinned live HDFS
contract is available.

## HDFS compressed Parquet local file-client fixture

A deterministic Snappy-compressed Parquet fixture and injected HDFS file client
cover the shared SQL reader, projections, filtering, ordering, pagination,
COUNT(*), metadata discovery, configured path validation, and write rejection
without HDFS. External gzip wrapping is rejected. The fixture does not
establish compatibility with a live HDFS release, authorization, Kerberos, HA
behavior, other Parquet codecs, or large-file performance. The connector
remains experimental until a pinned live HDFS contract is available.


## Ozone S3 Gateway protocol fixtures

The Ozone Parquet, text, and Avro adapters use the shared S3 client against local HTTP protocol fixtures. Their deterministic tests cover path-style bucket addressing, SigV4 authorization, metadata HEAD, bounded object GET, Snappy Parquet, line-oriented text, or Avro OCF read-only SQL, metadata discovery, and write rejection. This is protocol simulation only; it does not establish compatibility with a live Ozone release or gateway deployment. The connector remains experimental until a version-pinned live Ozone contract is run.
