#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
work_dir="$(mktemp -d)"
container_name="data-fabric-tidb-tls-$RANDOM-$RANDOM"
image_tag="data-fabric-tidb-tls:$RANDOM-$RANDOM"
tidb_image="pingcap/tidb:v8.5.8@sha256:df168c764bf2dfdb166dc37a5c3b0e210d29d5f3ab2d33317fd0fdf7b32037f5"

cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  docker image rm -f "$image_tag" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

for command in docker openssl go python3 curl; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$command" >&2
    exit 1
  fi
done

mkdir -p "$work_dir/context/tls"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -keyout "$work_dir/ca.key" -out "$work_dir/ca.crt" \
  -subj "/CN=Data Fabric TiDB Contract CA" \
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
  -subj "/CN=Untrusted Data Fabric TiDB Contract CA" \
  -addext "basicConstraints=critical,CA:TRUE" >/dev/null 2>&1
cp "$work_dir/ca.crt" "$work_dir/context/tls/ca.crt"

cat >"$work_dir/context/tidb.toml" <<'TOML'
[security]
ssl-cert = "/etc/tidb/tls/server.crt"
ssl-key = "/etc/tidb/tls/server.key"
TOML
cat >"$work_dir/context/Dockerfile" <<DOCKERFILE
FROM $tidb_image
COPY tls /etc/tidb/tls
COPY tidb.toml /etc/tidb/tidb.toml
RUN chmod 644 /etc/tidb/tls/server.crt /etc/tidb/tls/ca.crt && chmod 600 /etc/tidb/tls/server.key
DOCKERFILE

docker build --tag "$image_tag" "$work_dir/context"
docker run --detach --name "$container_name" \
  --publish 127.0.0.1::4000 \
  --publish 127.0.0.1::10080 \
  "$image_tag" \
  --config=/etc/tidb/tidb.toml \
  --host=0.0.0.0 \
  --store=unistore \
  --path=/tmp/data-fabric-tidb-tls-contract \
  --initialize-insecure=true >/dev/null
port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "4000/tcp") 0).HostPort}}' "$container_name")"
status_port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "10080/tcp") 0).HostPort}}' "$container_name")"

ready=false
for _ in $(seq 1 120); do
  if curl --fail --silent "http://127.0.0.1:$status_port/status" >/dev/null; then
    ready=true
    break
  fi
  sleep 1
done
if [ "$ready" != true ]; then
  docker logs "$container_name"
  printf 'temporary TLS-enabled TiDB service did not become ready\n' >&2
  exit 1
fi

cd "$repo_root"
DATA_FABRIC_TIDB_TLS_CONTRACT_HOST=localhost \
  DATA_FABRIC_TIDB_TLS_CONTRACT_PORT="$port" \
  DATA_FABRIC_TIDB_TLS_ROOT_CA="$work_dir/ca.crt" \
  DATA_FABRIC_TIDB_TLS_BAD_CA="$work_dir/bad-ca.crt" \
  DATA_FABRIC_TIDB_TLS_EXPECTED_VERSION=8.0.11-TiDB-v8.5.8 \
  go test ./internal/database/drivers/domestic -run '^TestTiDBTLSVerification$' -count=1 -v || {
    docker logs "$container_name" 2>&1 | grep -iE "mysql protocol server secure connection|error|server is running|listening" | tail -40 || true
    exit 1
  }
