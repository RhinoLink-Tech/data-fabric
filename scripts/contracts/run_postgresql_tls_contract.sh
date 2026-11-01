#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
work_dir="$(mktemp -d)"
container_name="data-fabric-postgres-tls-$RANDOM-$RANDOM"
image_tag="data-fabric-postgres-tls:$RANDOM-$RANDOM"
postgres_image="postgres:17-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24"

cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  docker image rm -f "$image_tag" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

for command in docker openssl go python3; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$command" >&2
    exit 1
  fi
done

mkdir -p "$work_dir/context"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -keyout "$work_dir/ca.key" -out "$work_dir/ca.crt" \
  -subj "/CN=Data Fabric PostgreSQL Contract CA" \
  -addext "basicConstraints=critical,CA:TRUE" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes \
  -keyout "$work_dir/context/server.key" -out "$work_dir/server.csr" \
  -subj "/CN=localhost" >/dev/null 2>&1
cat >"$work_dir/server.ext" <<'EXT'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:localhost
EXT
openssl x509 -req -days 1 -in "$work_dir/server.csr" \
  -CA "$work_dir/ca.crt" -CAkey "$work_dir/ca.key" -CAcreateserial \
  -extfile "$work_dir/server.ext" -out "$work_dir/context/server.crt" >/dev/null 2>&1
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -keyout "$work_dir/bad-ca.key" -out "$work_dir/bad-ca.crt" \
  -subj "/CN=Untrusted Data Fabric Contract CA" \
  -addext "basicConstraints=critical,CA:TRUE" >/dev/null 2>&1
cp "$work_dir/ca.crt" "$work_dir/context/ca.crt"

cat >"$work_dir/context/Dockerfile" <<DOCKERFILE
FROM $postgres_image
RUN mkdir -p /etc/postgresql/tls && chown postgres:postgres /etc/postgresql/tls
COPY --chown=postgres:postgres server.crt /etc/postgresql/tls/server.crt
COPY --chown=postgres:postgres server.key /etc/postgresql/tls/server.key
RUN chmod 644 /etc/postgresql/tls/server.crt && chmod 600 /etc/postgresql/tls/server.key
DOCKERFILE

docker build --tag "$image_tag" "$work_dir/context"
docker run --detach --name "$container_name" \
  --env POSTGRES_USER=data_fabric_contract \
  --env POSTGRES_PASSWORD=contract-test-only \
  --env POSTGRES_DB=data_fabric_contract \
  --publish 127.0.0.1::5432 \
  "$image_tag" postgres \
  -c ssl=on \
  -c ssl_cert_file=/etc/postgresql/tls/server.crt \
  -c ssl_key_file=/etc/postgresql/tls/server.key >/dev/null
port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "5432/tcp") 0).HostPort}}' "$container_name")"

ready=false
for _ in $(seq 1 90); do
  if docker exec "$container_name" pg_isready -U data_fabric_contract -d data_fabric_contract >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [ "$ready" != true ]; then
  docker logs "$container_name"
  printf 'temporary PostgreSQL TLS service did not become ready\n' >&2
  exit 1
fi

cd "$repo_root"
ssl_root_cert="$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "$work_dir/ca.crt")"
DATA_FABRIC_POSTGRES_TLS_CONTRACT_DSN="postgres://data_fabric_contract:contract-test-only@localhost:$port/data_fabric_contract?sslmode=verify-full&sslrootcert=$ssl_root_cert" \
  DATA_FABRIC_POSTGRES_TLS_BAD_CA="$work_dir/bad-ca.crt" \
  DATA_FABRIC_POSTGRES_TLS_EXPECTED_VERSION=17.11 \
  go test ./internal/database/drivers/traditional -run '^TestPostgreSQLTLSVerification$' -count=1 -v
