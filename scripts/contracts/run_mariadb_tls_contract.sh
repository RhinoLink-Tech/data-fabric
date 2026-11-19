#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
work_dir="$(mktemp -d)"
container_name="data-fabric-mariadb-tls-$RANDOM-$RANDOM"
metadata_name="data-fabric-mariadb-e2e-meta-$RANDOM-$RANDOM"
image_tag="data-fabric-mariadb-tls:$RANDOM-$RANDOM"
mariadb_image="${DATA_FABRIC_MARIADB_TLS_IMAGE:-mariadb:11@sha256:79d59758afc91b89b120b0a8904d637f5a3b3e1c4900f29b740d6d46c72fef68}"
mariadb_expected_version="${DATA_FABRIC_MARIADB_TLS_EXPECTED_VERSION:-11.8.9}"

cleanup() {
  docker rm -f "$container_name" "$metadata_name" >/dev/null 2>&1 || true
  docker image rm -f "$image_tag" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

for command in docker openssl go; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$command" >&2
    exit 1
  fi
done

mkdir -p "$work_dir/context/tls"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -keyout "$work_dir/ca.key" -out "$work_dir/ca.crt" \
  -subj "/CN=Data Fabric MariaDB Contract CA" \
  -addext "basicConstraints=critical,CA:TRUE" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes \
  -keyout "$work_dir/context/tls/server.key" -out "$work_dir/server.csr" \
  -subj "/CN=localhost" >/dev/null 2>&1
cat >"$work_dir/server.ext" <<'EXT'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:localhost
EXT
openssl x509 -req -days 1 -in "$work_dir/server.csr" \
  -CA "$work_dir/ca.crt" -CAkey "$work_dir/ca.key" -CAcreateserial \
  -extfile "$work_dir/server.ext" -out "$work_dir/context/tls/server.crt" >/dev/null 2>&1
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -keyout "$work_dir/bad-ca.key" -out "$work_dir/bad-ca.crt" \
  -subj "/CN=Untrusted Data Fabric MariaDB Contract CA" \
  -addext "basicConstraints=critical,CA:TRUE" >/dev/null 2>&1
cp "$work_dir/ca.crt" "$work_dir/context/tls/ca.crt"

cat >"$work_dir/context/Dockerfile" <<DOCKERFILE
FROM $mariadb_image
RUN mkdir -p /etc/mysql/tls && chown mysql:mysql /etc/mysql/tls
COPY tls /etc/mysql/tls
RUN chown -R mysql:mysql /etc/mysql/tls && chmod 644 /etc/mysql/tls/ca.crt /etc/mysql/tls/server.crt && chmod 600 /etc/mysql/tls/server.key
DOCKERFILE

docker build --tag "$image_tag" "$work_dir/context"
docker run --detach --name "$container_name" \
  --env MARIADB_ROOT_PASSWORD=contract-test-only \
  --env MARIADB_USER=data_fabric_contract \
  --env MARIADB_PASSWORD=contract-test-only \
  --env MARIADB_DATABASE=data_fabric_contract \
  --publish 127.0.0.1::3306 \
  "$image_tag" \
  --ssl-ca=/etc/mysql/tls/ca.crt \
  --ssl-cert=/etc/mysql/tls/server.crt \
  --ssl-key=/etc/mysql/tls/server.key >/dev/null
port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "3306/tcp") 0).HostPort}}' "$container_name")"

ready=false
for _ in $(seq 1 120); do
  if docker exec "$container_name" mariadb-admin ping --protocol=tcp --host=127.0.0.1 \
    --user=root --password=contract-test-only --silent >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [ "$ready" != true ]; then
  docker logs "$container_name"
  printf 'temporary TLS-enabled MariaDB service did not become ready\n' >&2
  exit 1
fi

# A separate account has SELECT on one synthetic table only; the contract
# checks source-side privileges independently of the gateway read-only session.
docker exec --env MYSQL_PWD=contract-test-only "$container_name" \
  mariadb --user=root --protocol=tcp --host=127.0.0.1 --execute="
    CREATE TABLE data_fabric_contract.browser_orders (id BIGINT PRIMARY KEY, status VARCHAR(16) NOT NULL);
    INSERT INTO data_fabric_contract.browser_orders VALUES (1, 'shipped'), (2, 'new'), (3, 'shipped');
    CREATE USER 'browser_reader'@'%' IDENTIFIED BY 'browser-test-only';
    GRANT SELECT ON data_fabric_contract.browser_orders TO 'browser_reader'@'%';"

cd "$repo_root"
DATA_FABRIC_MARIADB_TLS_CONTRACT_HOST=localhost \
  DATA_FABRIC_MARIADB_TLS_CONTRACT_PORT="$port" \
  DATA_FABRIC_MARIADB_TLS_CONTRACT_USERNAME=data_fabric_contract \
  DATA_FABRIC_MARIADB_TLS_CONTRACT_PASSWORD=contract-test-only \
  DATA_FABRIC_MARIADB_TLS_CONTRACT_DATABASE=data_fabric_contract \
  DATA_FABRIC_MARIADB_TLS_ROOT_CA="$work_dir/ca.crt" \
  DATA_FABRIC_MARIADB_TLS_BAD_CA="$work_dir/bad-ca.crt" \
  DATA_FABRIC_MARIADB_TLS_EXPECTED_VERSION="$mariadb_expected_version" \
  go test ./internal/database/drivers/traditional -run '^TestMariaDBTLSVerification$' -count=1 -v || {
    docker logs "$container_name" 2>&1 | grep -iE 'error|ready for connections|ssl' | tail -40 || true
    exit 1
  }

# The same pinned server also verifies the browser's restricted connector,
# not just the generic MariaDB driver. This runs on remote CI when selected.
DATA_FABRIC_MARIADB_TLS_CONTRACT_PORT="$port" \
  DATA_FABRIC_MARIADB_TLS_CONTRACT_USERNAME=data_fabric_contract \
  DATA_FABRIC_MARIADB_TLS_CONTRACT_PASSWORD=contract-test-only \
  DATA_FABRIC_MARIADB_TLS_CONTRACT_DATABASE=data_fabric_contract \
  DATA_FABRIC_MARIADB_TLS_ROOT_CA="$work_dir/ca.crt" \
  DATA_FABRIC_MARIADB_TLS_BAD_CA="$work_dir/bad-ca.crt" \
  go test ./internal/database -run '^TestMariaDBRestrictedOnboardingTLSContract$' -count=1 -v

DATA_FABRIC_MARIADB_TLS_CONTRACT_PORT="$port" \
  DATA_FABRIC_MARIADB_TLS_CONTRACT_USERNAME=browser_reader \
  DATA_FABRIC_MARIADB_TLS_CONTRACT_PASSWORD=browser-test-only \
  DATA_FABRIC_MARIADB_TLS_CONTRACT_DATABASE=data_fabric_contract \
  DATA_FABRIC_MARIADB_TLS_ROOT_CA="$work_dir/ca.crt" \
  go test ./internal/workbuddy/onboardinghttp -run '^TestMariaDBBrowserRestrictedTLSContract$' -count=1 -v

# One pinned MariaDB version also covers the signed-OIDC, browser and MCP
# lifecycle. MySQL remains the separate supported metadata backend.
if [ "$mariadb_expected_version" = "11.8.9" ]; then
  docker run --detach --name "$metadata_name" \
    --env MYSQL_ROOT_PASSWORD=metadata-root-test-only \
    --env MYSQL_DATABASE=df_metadata \
    --env MYSQL_USER=df_meta \
    --env MYSQL_PASSWORD=metadata-test-only \
    --publish 127.0.0.1::3306 \
    mysql:8.4.13@sha256:a0225d1ea6631c8bc2385dc4c2faba17bbf1834ecf369e1659a9a382bd8d5dd7 >/dev/null
  metadata_port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "3306/tcp") 0).HostPort}}' "$metadata_name")"
  metadata_ready=false
  for _ in $(seq 1 120); do
    if docker exec "$metadata_name" mysqladmin ping --protocol=tcp --host=127.0.0.1 \
      --user=root --password=metadata-root-test-only --silent >/dev/null 2>&1; then
      metadata_ready=true
      break
    fi
    sleep 1
  done
  if [ "$metadata_ready" != true ]; then
    printf 'temporary metadata service did not become ready\n' >&2
    exit 1
  fi
  DF_WB_E2E_UPDATE=false DF_WB_E2E_POSTGRES_PORT= \
    DF_WB_E2E_MYSQL_DSN="df_meta:metadata-test-only@tcp(127.0.0.1:${metadata_port})/df_metadata?parseTime=true" \
    DF_WB_E2E_MARIADB_PORT="$port" \
    DF_WB_E2E_MARIADB_CA="$work_dir/ca.crt" \
    go test -count=1 ./internal/workbuddy/onboardingruntime -run '^TestSignedOIDCAndMCPBrowserLifecycle$' -v
fi
