# Driver contract test report

## Verified baselines

The PostgreSQL, MySQL, and MariaDB drivers have repeatable end-to-end contract tests against exact, pinned server images.

| Driver | Exact server version | Pinned image | Authentication |
| --- | --- | --- | --- |
| PostgreSQL | 17.11 | postgres:17-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24 | username and password |
| MySQL | 8.0.46 | mysql:8.0@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b | username and password |
| MariaDB | 11.8.9 | mariadb:11@sha256:79d59758afc91b89b120b0a8904d637f5a3b3e1c4900f29b740d6d46c72fef68 | username and password |

All three suites cover connection and health checks, schema lookup, SQL value and NULL scanning, batch pagination, database-side read-only session enforcement, query cancellation, and server error propagation. Each suite verifies the exact server product and version before exercising the driver.

These test containers use test-only accounts and databases. They contain no production credentials. TLS and other server versions are not covered by these baselines.

The support catalog reports only PostgreSQL 17.11, MySQL 8.0.46, and MariaDB 11.8.9 as verified. Other connector types and versions retain their existing catalog status until a pinned server contract is added.

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

CI runs all three pinned services as part of the Go checks. For additional connector profiles, pinned versions, and deferred cases, see [issue #35](https://github.com/RhinoLink-Tech/data-fabric/issues/35).
