# Driver contract test report

## Verified baselines

The PostgreSQL, openGauss, MySQL, MariaDB, TiDB, ClickHouse, and SQL Server drivers have repeatable end-to-end contract tests against exact, pinned server images.

| Driver | Exact server version | Pinned image | Authentication |
| --- | --- | --- | --- |
| PostgreSQL | 17.11 | postgres:17-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24 | username and password |
| openGauss | 6.0.0 (server reports openGauss 6.0.0 build aee4abd5) | enmotech/opengauss:6.0.0@sha256:9bd81380273944e5a02a2139c90954d4f46813b71810f7b23fe8f738014d03b5 | username and password |
| MySQL | 8.0.46 | mysql:8.0@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b | username and password |
| MariaDB | 11.8.9 | mariadb:11@sha256:79d59758afc91b89b120b0a8904d637f5a3b3e1c4900f29b740d6d46c72fef68 | username and password |
| TiDB | 8.5.8 (server reports 8.0.11-TiDB-v8.5.8) | pingcap/tidb:v8.5.8@sha256:df168c764bf2dfdb166dc37a5c3b0e210d29d5f3ab2d33317fd0fdf7b32037f5 | username and password |
| ClickHouse | 26.8.20.9 | clickhouse/clickhouse-server:26.8.20.9-alpine@sha256:1e67397fa4828e8dbb95b3bcc37a8822749ca5e46bd208b3ef336f365c522688 | username and password |
| SQL Server | 16.0.4305.5 | mcr.microsoft.com/mssql/server:2022-latest@sha256:cf8f0ab2ebe61feec2eed2629061a07d247f791c2a8ddda74a4ac9f06a291718 | SQL authentication |

All seven suites cover connection and health checks, SQL value and NULL scanning, batch pagination, query cancellation, and server error propagation. The PostgreSQL, openGauss, MySQL, and MariaDB suites also verify database-side read-only session enforcement. The TiDB and SQL Server suites check that database-side read-only initialization is reported as unsupported for the tested version. The ClickHouse suite uses the native protocol and verifies that the SQL driver is registered before connecting. Each suite verifies the exact server product and version before exercising the driver.

These test containers use test-only accounts and databases. They contain no production credentials. TLS and other server versions are not covered by these baselines.

The support catalog reports only PostgreSQL 17.11, openGauss 6.0.0, MySQL 8.0.46, MariaDB 11.8.9, TiDB 8.5.8, ClickHouse 26.8.20.9, and SQL Server 16.0.4305.5 as verified. Other connector types and versions retain their existing catalog status until a pinned server contract is added.

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

Database-side read-only session enforcement is not supported by this SQL Server adapter. Use a SQL Server account with only the privileges the gateway needs. TLS behavior and other SQL Server versions are not covered by this baseline.

TiDB 8.5.8 does not implement the generic session read-only statement; configure server-side permissions for read-only accounts. ClickHouse uses the native protocol and also does not use the gateway read-only session initializer. CI runs seven pinned database contracts in two jobs to keep the SQL Server service isolated from the other database containers. For additional connector profiles, pinned versions, and deferred cases, see [issue #35](https://github.com/RhinoLink-Tech/data-fabric/issues/35).
