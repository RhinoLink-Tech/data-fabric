#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
work_dir="$(mktemp -d)"
container_name="data-fabric-clickhouse-tls-$RANDOM-$RANDOM"
image_tag="data-fabric-clickhouse-tls:$RANDOM-$RANDOM"
clickhouse_image="clickhouse/clickhouse-server:26.8.20.9-alpine@sha256:1e67397fa4828e8dbb95b3bcc37a8822749ca5e46bd208b3ef336f365c522688"
username="data_fabric_contract"
password="contract-test-only"
database="data_fabric_contract"

cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
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

mkdir -p "$work_dir/context/certs"
openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -keyout "$work_dir/context/certs/ca.key" -out "$work_dir/context/certs/ca.crt" \
  -subj "/CN=Data Fabric ClickHouse Contract CA" \
  -addext "basicConstraints=critical,CA:TRUE" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes \
  -keyout "$work_dir/context/certs/server.key" -out "$work_dir/server.csr" \
  -subj "/CN=Data Fabric ClickHouse Contract Server" >/dev/null 2>&1
cat >"$work_dir/server.ext" <<'EXT'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=IP:127.0.0.1
EXT
openssl x509 -req -days 2 -in "$work_dir/server.csr" \
  -CA "$work_dir/context/certs/ca.crt" \
  -CAkey "$work_dir/context/certs/ca.key" -CAcreateserial \
  -extfile "$work_dir/server.ext" -out "$work_dir/context/certs/server.crt" >/dev/null 2>&1
openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -keyout "$work_dir/bad-ca.key" -out "$work_dir/bad-ca.crt" \
  -subj "/CN=Untrusted Data Fabric ClickHouse Contract CA" \
  -addext "basicConstraints=critical,CA:TRUE" >/dev/null 2>&1

cat >"$work_dir/context/clickhouse-tls.xml" <<'XML'
<clickhouse>
  <tcp_port_secure>9440</tcp_port_secure>
  <openSSL>
    <server>
      <certificateFile>/etc/clickhouse-server/certs/server.crt</certificateFile>
      <privateKeyFile>/etc/clickhouse-server/certs/server.key</privateKeyFile>
      <verificationMode>none</verificationMode>
    </server>
  </openSSL>
</clickhouse>
XML
cat >"$work_dir/context/Dockerfile" <<DOCKERFILE
FROM $clickhouse_image
USER root
RUN mkdir -p /etc/clickhouse-server/certs
COPY --chown=clickhouse:clickhouse certs/server.crt /etc/clickhouse-server/certs/server.crt
COPY --chown=clickhouse:clickhouse certs/server.key /etc/clickhouse-server/certs/server.key
COPY clickhouse-tls.xml /etc/clickhouse-server/config.d/data-fabric-tls.xml
RUN chmod 644 /etc/clickhouse-server/certs/server.crt && chmod 600 /etc/clickhouse-server/certs/server.key
DOCKERFILE

docker build --tag "$image_tag" "$work_dir/context"
docker run --detach --name "$container_name" \
  --ulimit nofile=262144:262144 \
  --publish 127.0.0.1::9440 \
  --env CLICKHOUSE_DB="$database" \
  --env CLICKHOUSE_USER="$username" \
  --env CLICKHOUSE_PASSWORD="$password" \
  --env CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1 \
  "$image_tag" >/dev/null
port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "9440/tcp") 0).HostPort}}' "$container_name")"

ready=false
for _ in $(seq 1 120); do
  if docker exec "$container_name" clickhouse-client --host 127.0.0.1 --port 9000 \
    --user "$username" --password "$password" --database "$database" --query 'SELECT 1' >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [ "$ready" != true ]; then
  docker logs "$container_name"
  printf 'pinned ClickHouse TLS service did not become ready\n' >&2
  exit 1
fi

cd "$repo_root"
DATA_FABRIC_CLICKHOUSE_CONTRACT_USERNAME="$username" \
  DATA_FABRIC_CLICKHOUSE_CONTRACT_PASSWORD="$password" \
  DATA_FABRIC_CLICKHOUSE_CONTRACT_DATABASE="$database" \
  DATA_FABRIC_CLICKHOUSE_TLS_CONTRACT_HOST=127.0.0.1 \
  DATA_FABRIC_CLICKHOUSE_TLS_CONTRACT_PORT="$port" \
  DATA_FABRIC_CLICKHOUSE_TLS_ROOT_CA="$work_dir/context/certs/ca.crt" \
  DATA_FABRIC_CLICKHOUSE_TLS_BAD_CA="$work_dir/bad-ca.crt" \
  DATA_FABRIC_CLICKHOUSE_TLS_EXPECTED_VERSION=26.8.20.9 \
  go test ./internal/database/drivers/olap -run '^TestClickHouseTLSVerification$' -count=1 -v || {
    docker logs "$container_name" 2>&1 | grep -iE 'error|ssl|tls|ready for connections' | tail -60 || true
    exit 1
  }
