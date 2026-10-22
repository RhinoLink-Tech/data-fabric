# Driver contract test report

## Verified baseline

The PostgreSQL driver has a repeatable end-to-end contract test against PostgreSQL 17.11.

- Container image: postgres:17-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24
- Exact server version verified: 17.11
- Authentication: username and password
- Covered behavior: connection and health check, schema lookup, SQL value and NULL scanning, batch pagination, database-side read-only session enforcement, query cancellation, and server error propagation.
- The test container uses a test-only account and database. It contains no production credentials.
- TLS, other PostgreSQL server versions, and vendor-compatible PostgreSQL products are not covered by this baseline.

The support catalog reports PostgreSQL 17.11 as the only verified version. Other connector types and versions remain at their existing catalog status until a pinned server contract is added.

## Run the contract locally

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

CI runs this pinned service as part of the Go checks. For additional connector profiles, pinned versions, and deferred cases, see [issue #35](https://github.com/RhinoLink-Tech/data-fabric/issues/35).
