#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
work_dir="$(mktemp -d)"
container_name="data-fabric-sqlserver-tls-$RANDOM-$RANDOM"
image_tag="data-fabric-sqlserver-tls:$RANDOM-$RANDOM"
sqlserver_image="mcr.microsoft.com/mssql/server:2022-latest@sha256:cf8f0ab2ebe61feec2eed2629061a07d247f791c2a8ddda74a4ac9f06a291718"

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

mkdir -p "$work_dir/context"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -keyout "$work_dir/ca.key" -out "$work_dir/ca.crt" \
  -subj "/CN=Data Fabric SQL Server Contract CA" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" >/dev/null 2>&1
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
  -subj "/CN=Untrusted Data Fabric SQL Server Contract CA" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" >/dev/null 2>&1

cat >"$work_dir/context/Dockerfile" <<DOCKERFILE
FROM $sqlserver_image
USER root
COPY server.crt /var/opt/mssql/secrets/data-fabric-server.crt
COPY server.key /var/opt/mssql/secrets/data-fabric-server.key
RUN chown mssql:mssql /var/opt/mssql/secrets/data-fabric-server.crt /var/opt/mssql/secrets/data-fabric-server.key \\
    && chmod 444 /var/opt/mssql/secrets/data-fabric-server.crt \\
    && chmod 400 /var/opt/mssql/secrets/data-fabric-server.key \\
    && /opt/mssql/bin/mssql-conf set network.tlscert /var/opt/mssql/secrets/data-fabric-server.crt \\
    && /opt/mssql/bin/mssql-conf set network.tlskey /var/opt/mssql/secrets/data-fabric-server.key \\
    && /opt/mssql/bin/mssql-conf set network.forceencryption 0 \\
    && /opt/mssql/bin/mssql-conf set network.tlsprotocols 1.2
USER mssql
DOCKERFILE

docker build --tag "$image_tag" "$work_dir/context"
docker run --detach --name "$container_name" \
  --env ACCEPT_EULA=Y \
  --env MSSQL_PID=Developer \
  --env MSSQL_SA_PASSWORD='DataFabric_2026!' \
  --publish 127.0.0.1::1433 \
  "$image_tag" >/dev/null
port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "1433/tcp") 0).HostPort}}' "$container_name")"

ready=false
for _ in $(seq 1 120); do
  if docker exec "$container_name" bash -c 'exec 3<>/dev/tcp/127.0.0.1/1433' >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [ "$ready" != true ]; then
  docker logs "$container_name"
  printf 'temporary SQL Server TLS service did not become ready\n' >&2
  exit 1
fi

cd "$repo_root"
DATA_FABRIC_SQLSERVER_CONTRACT_HOST=localhost \
DATA_FABRIC_SQLSERVER_CONTRACT_PORT="$port" \
DATA_FABRIC_SQLSERVER_CONTRACT_USERNAME=sa \
DATA_FABRIC_SQLSERVER_CONTRACT_PASSWORD='DataFabric_2026!' \
DATA_FABRIC_SQLSERVER_CONTRACT_DATABASE=master \
DATA_FABRIC_SQLSERVER_EXPECTED_VERSION=16.0.4305.5 \
DATA_FABRIC_SQLSERVER_TLS_CA_CERT="$work_dir/ca.crt" \
DATA_FABRIC_SQLSERVER_TLS_BAD_CA="$work_dir/bad-ca.crt" \
  go test ./internal/database/drivers/traditional \
    -run '^TestSQLServer(DriverContract|TLSCertificateVerification)$' -count=1 -v
