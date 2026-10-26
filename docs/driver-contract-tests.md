# Driver contract test report

## Verified baselines

The PostgreSQL, openGauss, MySQL, MariaDB, TiDB, ClickHouse, SQL Server, MongoDB, Oracle, Apache Doris, StarRocks, and MinIO CSV drivers have repeatable versioned contract tests against exact, pinned runtime artifacts.

| Driver | Exact server version | Pinned runtime artifact | Authentication |
| --- | --- | --- | --- |
| PostgreSQL | 17.11 | postgres:17-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24 | username and password |
| openGauss | 6.0.0 (server reports openGauss 6.0.0 build aee4abd5) | enmotech/opengauss:6.0.0@sha256:9bd81380273944e5a02a2139c90954d4f46813b71810f7b23fe8f738014d03b5 | username and password |
| MySQL | 8.0.46 | mysql:8.0@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b | username and password |
| MariaDB | 11.8.9 | mariadb:11@sha256:79d59758afc91b89b120b0a8904d637f5a3b3e1c4900f29b740d6d46c72fef68 | username and password |
| TiDB | 8.5.8 (server reports 8.0.11-TiDB-v8.5.8) | pingcap/tidb:v8.5.8@sha256:df168c764bf2dfdb166dc37a5c3b0e210d29d5f3ab2d33317fd0fdf7b32037f5 | username and password |
| ClickHouse | 26.8.20.9 | clickhouse/clickhouse-server:26.8.20.9-alpine@sha256:1e67397fa4828e8dbb95b3bcc37a8822749ca5e46bd208b3ef336f365c522688 | username and password |
| SQL Server | 16.0.4305.5 | mcr.microsoft.com/mssql/server:2022-latest@sha256:cf8f0ab2ebe61feec2eed2629061a07d247f791c2a8ddda74a4ac9f06a291718 | SQL authentication |
| MongoDB | 8.0.34 | mongo:8.0.34-noble@sha256:c2d54fddfb3738fb27fe117f51692e3f44e640a3e7aeb8da3937ee9cd33a650b | database-scoped username and password |
| Oracle | 23.26.3.0.0 | ghcr.io/gvenzl/oracle-free:23.26.3-slim@sha256:366fe85702d764d81a0272b72e94e629290c27aa72a47825fcc45701a0633dac | application schema user; SYSTEM for version probe |
| Apache Doris | 4.1.3 | apache/doris:all-in-one-4.1.3@sha256:4d3aef8f4fe63f6e32b493b4fd7b2a682d19e2c800d1d812131e67aaba0d212a | root for fixture setup; database-scoped username and password for the driver |
| StarRocks | 4.1.6 | starrocks/allin1-ubuntu:4.1.6@sha256:88d1192620d03cf7ae8d5ece0b02da1024372d33bc411e5a51e2d3b003c551b3 | root for fixture setup; table-scoped username and password for the driver |
| MinIO CSV | RELEASE.2025-04-22T22-12-26Z | [upstream source commit 0d7408fc9969caf07de6a8c3a84f9fbb10a6739e](https://github.com/minio/minio/tree/0d7408fc9969caf07de6a8c3a84f9fbb10a6739e), archive SHA-256 `7eb30a913fea30f18069abf194e1e78e4983b558cc526911ae1c11396a9859a5` | root access key and secret key |

The suites cover connection, SQL execution, value scanning, pagination, cancellation, and error handling where those operations apply. The Doris and StarRocks contracts also call the application's table metadata extractor against the live server, including column names, types, and nullability. The PostgreSQL, openGauss, MySQL, and MariaDB suites also verify database-side read-only session enforcement. The TiDB and SQL Server suites verify that database-side read-only initialization is unsupported for the tested version. The ClickHouse suite uses its native protocol. The MongoDB suite verifies the restricted SQL adapter against a real MongoDB server, including a database-scoped account, projections, filters, ordering, pagination, count, NULL values, and rejection of writes and unsupported SQL. The Oracle suite verifies the go-ora driver against Oracle AI Database 26ai Free with a dedicated application schema account; SYSTEM is used only to read the exact server version. It covers bind parameters, value scanning, DML, count, Oracle pagination, and database errors. Each suite checks the exact server product and version before exercising its adapter.

These test containers use test-only accounts and databases. They contain no production credentials. Production TLS certificate validation and other server versions are not covered by these baselines.

The MinIO CSV contract builds the pinned upstream source archive locally because the historical upstream container image is no longer published. The script verifies the archive SHA-256, checks the built binary's release identifier, then runs the same S3-compatible fixture against that MinIO process.

## Run the MinIO CSV contract locally

The script needs Bash, `curl`, `tar`, `sha256sum`, and the Go version declared in this repository's `go.mod`. It downloads and verifies the fixed MinIO source artifact, starts an isolated local process, and removes it when the test ends:

~~~~sh
bash scripts/contracts/run_minio_csv_contract.sh
~~~~

The support catalog records PostgreSQL 17.11, openGauss 6.0.0, MySQL 8.0.46, MariaDB 11.8.9, TiDB 8.5.8, ClickHouse 26.8.20.9, SQL Server 16.0.4305.5, MongoDB 8.0.34, Oracle AI Database 26ai Free 23.26.3.0.0, Apache Doris 4.1.3, StarRocks 4.1.6, and MinIO RELEASE.2025-04-22T22-12-26Z as versioned contract evidence. MongoDB and MinIO CSV remain experimental because their evidence applies to restricted adapter boundaries. Other connector types and versions retain their existing catalog status until a pinned server contract is added.

## Apache Druid protocol fixture

Apache Druid has a unified, read-only SQL gateway path but is not part of the
versioned baseline table. The protocol fixture and query-service test exercise
the Router/Broker status endpoint, optional HTTP basic authentication, the
`/druid/v2/sql` request shape, dynamic parameters, array responses with column
and SQL type headers, type normalization, pagination, and write rejection.

This is not evidence for a Druid server version. TLS behavior, asynchronous
statements, propagation of gateway cancellation to Druid's cancel endpoint,
native JSON queries, and coordinator metadata are outside this fixture. The
adapter remains experimental until a pinned live Druid contract is added under
[issue #35](https://github.com/RhinoLink-Tech/data-fabric/issues/35).

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

openGauss uses the PostgreSQL wire protocol in this contract. Its build-specific product signature is recorded separately from the PostgreSQL-compatible protocol version. TLS behavior and other openGauss versions are not covered.

## Run the SQL Server contract locally

The image requires an x86-64 Linux host. Start the exact image used by CI on local port 1434:

~~~~sh
docker run --rm --detach --name data-fabric-sqlserver-contract \
  --publish 1434:1433 \
  --env ACCEPT_EULA=Y \
  --env MSSQL_PID=Developer \
  --env MSSQL_SA_PASSWORD='DataFabric_2026!' \
  mcr.microsoft.com/mssql/server:2022-latest@sha256:cf8f0ab2ebe61feec2eed2629061a07d247f791c2a8ddda74a4ac9f06a291718
~~~~

Wait for SQL Server to accept connections, then run:

~~~~sh
export DATA_FABRIC_SQLSERVER_CONTRACT_HOST='127.0.0.1'
export DATA_FABRIC_SQLSERVER_CONTRACT_PORT='1434'
export DATA_FABRIC_SQLSERVER_CONTRACT_USERNAME='sa'
export DATA_FABRIC_SQLSERVER_CONTRACT_PASSWORD='DataFabric_2026!'
export DATA_FABRIC_SQLSERVER_CONTRACT_DATABASE='master'
export DATA_FABRIC_SQLSERVER_EXPECTED_VERSION='16.0.4305.5'
go test ./internal/database/drivers/traditional -run '^TestSQLServerDriverContract$' -count=1 -v
~~~~

Stop the test service when finished:

~~~~sh
docker stop data-fabric-sqlserver-contract
~~~~

Database-side read-only session enforcement is not supported by this SQL Server adapter. Use a SQL Server account with only the privileges the gateway needs. The test trusts the container's self-signed TLS certificate; production certificate validation and other SQL Server versions are not covered by this baseline.

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

For additional connector profiles, pinned versions, and deferred cases, see [issue #35](https://github.com/RhinoLink-Tech/data-fabric/issues/35).

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
