#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
work_dir="$(mktemp -d)"
container_name="data-fabric-mysql-tls-$RANDOM-$RANDOM"
image_tag="data-fabric-mysql-tls:$RANDOM-$RANDOM"
mysql_image="${DATA_FABRIC_MYSQL_TLS_IMAGE:-mysql:8.0@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b}"
expected_version="${DATA_FABRIC_MYSQL_TLS_EXPECTED_VERSION:-8.0.46}"

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

mkdir -p "$work_dir/context/tls"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -keyout "$work_dir/ca.key" -out "$work_dir/ca.crt" \
  -subj "/CN=Data Fabric MySQL Contract CA" \
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
  -subj "/CN=Untrusted Data Fabric MySQL Contract CA" \
  -addext "basicConstraints=critical,CA:TRUE" >/dev/null 2>&1
cp "$work_dir/ca.crt" "$work_dir/context/tls/ca.crt"

cat >"$work_dir/context/Dockerfile" <<DOCKERFILE
FROM $mysql_image
RUN mkdir -p /etc/mysql/tls && chown mysql:mysql /etc/mysql/tls
COPY tls /etc/mysql/tls
RUN chown -R mysql:mysql /etc/mysql/tls && chmod 644 /etc/mysql/tls/ca.crt /etc/mysql/tls/server.crt && chmod 600 /etc/mysql/tls/server.key
DOCKERFILE

docker build --tag "$image_tag" "$work_dir/context"
docker run --detach --name "$container_name" \
  --env MYSQL_ROOT_PASSWORD=contract-test-only \
  --env MYSQL_ROOT_HOST=% \
  --env MYSQL_USER=data_fabric_contract \
  --env MYSQL_PASSWORD=contract-test-only \
  --env MYSQL_DATABASE=data_fabric_contract \
  --publish 127.0.0.1::3306 \
  "$image_tag" \
  --ssl-ca=/etc/mysql/tls/ca.crt \
  --ssl-cert=/etc/mysql/tls/server.crt \
  --ssl-key=/etc/mysql/tls/server.key >/dev/null
port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "3306/tcp") 0).HostPort}}' "$container_name")"

ready=false
for _ in $(seq 1 120); do
  if docker exec "$container_name" mysqladmin ping --protocol=tcp --host=127.0.0.1 \
    --user=root --password=contract-test-only --silent >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [ "$ready" != true ]; then
  docker logs "$container_name"
  printf 'temporary TLS-enabled MySQL service did not become ready\n' >&2
  exit 1
fi

cd "$repo_root"
DATA_FABRIC_MYSQL_TLS_CONTRACT_HOST=localhost \
  DATA_FABRIC_MYSQL_TLS_CONTRACT_PORT="$port" \
  DATA_FABRIC_MYSQL_TLS_CONTRACT_USERNAME=data_fabric_contract \
  DATA_FABRIC_MYSQL_TLS_CONTRACT_PASSWORD=contract-test-only \
  DATA_FABRIC_MYSQL_TLS_CONTRACT_DATABASE=data_fabric_contract \
  DATA_FABRIC_MYSQL_TLS_ROOT_CA="$work_dir/ca.crt" \
  DATA_FABRIC_MYSQL_TLS_BAD_CA="$work_dir/bad-ca.crt" \
  DATA_FABRIC_MYSQL_TLS_EXPECTED_VERSION="$expected_version" \
  go test ./internal/database/drivers/traditional -run '^TestMySQLTLSVerification$' -count=1 -v || {
    docker logs "$container_name" 2>&1 | grep -iE 'error|server is ready|ready for connections|ssl' | tail -40 || true
    exit 1
  }
