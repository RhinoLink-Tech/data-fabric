#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
work_dir="$(mktemp -d)"
container_name="data-fabric-opengauss-tls-$RANDOM-$RANDOM"
opengauss_image="enmotech/opengauss:6.0.0@sha256:9bd81380273944e5a02a2139c90954d4f46813b71810f7b23fe8f738014d03b5"
password="DataFabric_2026!"

cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

for command in docker openssl go; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$command" >&2
    exit 1
  fi
done

openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -keyout "$work_dir/ca.key" -out "$work_dir/ca.crt" \
  -subj "/CN=Data Fabric openGauss Contract CA" \
  -addext "basicConstraints=critical,CA:TRUE" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes \
  -keyout "$work_dir/server.key" -out "$work_dir/server.csr" \
  -subj "/CN=localhost" >/dev/null 2>&1
cat >"$work_dir/server.ext" <<'EXT'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:localhost
EXT
openssl x509 -req -days 2 -in "$work_dir/server.csr" \
  -CA "$work_dir/ca.crt" -CAkey "$work_dir/ca.key" -CAcreateserial \
  -extfile "$work_dir/server.ext" -out "$work_dir/server.crt" >/dev/null 2>&1
openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -keyout "$work_dir/bad-ca.key" -out "$work_dir/bad-ca.crt" \
  -subj "/CN=Untrusted Data Fabric openGauss Contract CA" \
  -addext "basicConstraints=critical,CA:TRUE" >/dev/null 2>&1

docker run --detach --name "$container_name" \
  --privileged=true \
  --publish 127.0.0.1::5432 \
  --env GS_PASSWORD="$password" \
  "$opengauss_image" >/dev/null
port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "5432/tcp") 0).HostPort}}' "$container_name")"

exec_gauss() {
  docker exec --user omm \
    --env GAUSSHOME=/usr/local/opengauss \
    --env LD_LIBRARY_PATH=/usr/local/opengauss/lib \
    --env PATH="/usr/local/opengauss/bin:$PATH" \
    "$container_name" "$@"
}

ready=false
for _ in $(seq 1 180); do
  if exec_gauss gsql -h 127.0.0.1 -p 5432 -U gaussdb -d postgres \
    -W "$password" -c 'SELECT 1' >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [ "$ready" != true ]; then
  docker logs "$container_name"
  printf 'pinned openGauss service did not become ready\n' >&2
  exit 1
fi

docker cp "$work_dir/server.crt" "$container_name:/var/lib/opengauss/data/server.crt"
docker cp "$work_dir/server.key" "$container_name:/var/lib/opengauss/data/server.key"
docker exec --user root "$container_name" chown omm:omm \
  /var/lib/opengauss/data/server.crt /var/lib/opengauss/data/server.key
docker exec --user root "$container_name" chmod 600 /var/lib/opengauss/data/server.crt
docker exec --user root "$container_name" chmod 600 /var/lib/opengauss/data/server.key
exec_gauss gs_guc set -D /var/lib/opengauss/data \
  -c 'ssl=on' \
  -c "ssl_cert_file = 'server.crt'" \
  -c "ssl_key_file = 'server.key'"
docker restart "$container_name" >/dev/null
port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "5432/tcp") 0).HostPort}}' "$container_name")"

ready=false
for _ in $(seq 1 180); do
  if exec_gauss gsql -h 127.0.0.1 -p 5432 -U gaussdb -d postgres \
    -W "$password" -c 'SELECT 1' >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [ "$ready" != true ]; then
  docker logs "$container_name"
  printf 'TLS-enabled openGauss service did not become ready\n' >&2
  exit 1
fi

ready=false
for _ in $(seq 1 60); do
  if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
    ready=true
    break
  fi
  sleep 1
done
if [ "$ready" != true ]; then
  docker port "$container_name"
  docker logs "$container_name"
  printf 'published openGauss host port did not become reachable\n' >&2
  exit 1
fi

cd "$repo_root"
DATA_FABRIC_OPENGAUSS_TLS_CONTRACT_HOST=localhost \
  DATA_FABRIC_OPENGAUSS_TLS_CONTRACT_PORT="$port" \
  DATA_FABRIC_OPENGAUSS_TLS_CONTRACT_USERNAME=gaussdb \
  DATA_FABRIC_OPENGAUSS_TLS_CONTRACT_PASSWORD="$password" \
  DATA_FABRIC_OPENGAUSS_TLS_CONTRACT_DATABASE=postgres \
  DATA_FABRIC_OPENGAUSS_TLS_ROOT_CA="$work_dir/ca.crt" \
  DATA_FABRIC_OPENGAUSS_TLS_BAD_CA="$work_dir/bad-ca.crt" \
  DATA_FABRIC_OPENGAUSS_TLS_EXPECTED_VERSION='openGauss 6.0.0 build aee4abd5' \
  go test ./internal/database/drivers/domestic -run '^TestOpenGaussTLSVerification$' -count=1 -v || {
    docker logs "$container_name" 2>&1 | grep -iE 'error|ssl|ready for connections' | tail -60 || true
    exit 1
  }

DATA_FABRIC_OPENGAUSS_CONTRACT_HOST=127.0.0.1 \
  DATA_FABRIC_OPENGAUSS_CONTRACT_PORT="$port" \
  DATA_FABRIC_OPENGAUSS_CONTRACT_USERNAME=gaussdb \
  DATA_FABRIC_OPENGAUSS_CONTRACT_PASSWORD="$password" \
  DATA_FABRIC_OPENGAUSS_CONTRACT_DATABASE=postgres \
  DATA_FABRIC_OPENGAUSS_EXPECTED_VERSION='openGauss 6.0.0 build aee4abd5' \
  go test ./internal/database/drivers/domestic -run '^TestOpenGaussDriverContract$' -count=1 -v || {
    docker logs "$container_name" 2>&1 | grep -iE 'error|ssl|ready for connections' | tail -60 || true
    exit 1
  }
