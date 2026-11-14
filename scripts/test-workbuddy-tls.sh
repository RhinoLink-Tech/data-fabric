#!/bin/bash
set -euo pipefail
dir=$(mktemp -d /tmp/df-wb-tls-test-XXXXXXXX)
suffix=$(basename "$dir")
pg="df-wb-tls-pg-$suffix"
my="df-wb-tls-my-$suffix"
cleanup(){
 docker stop "$pg" "$my" >/dev/null 2>&1 || true
 rm -f -- "$dir/ca.key" "$dir/ca.pem" "$dir/ca.srl" "$dir/bad.key" "$dir/bad.pem" "$dir/server.key" "$dir/server.csr" "$dir/server.pem" "$dir/ext.cnf"
 rmdir -- "$dir" 2>/dev/null || true
}
trap cleanup EXIT
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$dir/ca.key" -out "$dir/ca.pem" -days 1 -subj "/CN=DF TLS synthetic CA" >/dev/null 2>&1
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$dir/bad.key" -out "$dir/bad.pem" -days 1 -subj "/CN=Other synthetic CA" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -keyout "$dir/server.key" -out "$dir/server.csr" -subj "/CN=db.example.test" >/dev/null 2>&1
echo "subjectAltName=DNS:db.example.test" > "$dir/ext.cnf"
openssl x509 -req -in "$dir/server.csr" -CA "$dir/ca.pem" -CAkey "$dir/ca.key" -CAcreateserial -out "$dir/server.pem" -days 1 -extfile "$dir/ext.cnf" >/dev/null 2>&1
chmod 755 "$dir"
chmod 644 "$dir/server.key" "$dir/server.pem" "$dir/ca.pem"
password='SyntheT1c@:/?&=测试'
docker run --rm -d --name "$pg" -p 127.0.0.1::5432 -v "$dir:/certs:ro" -e POSTGRES_PASSWORD="$password" -e POSTGRES_DB=synthetic --entrypoint sh postgres:17-alpine -c 'mkdir -p /tmp/dfcert; cp /certs/server.pem /certs/server.key /tmp/dfcert/; chown postgres:postgres /tmp/dfcert/*; chmod 600 /tmp/dfcert/server.key; exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/dfcert/server.pem -c ssl_key_file=/tmp/dfcert/server.key' >/dev/null
docker run --rm -d --name "$my" -p 127.0.0.1::3306 -v "$dir:/certs:ro" -e MYSQL_ROOT_PASSWORD="$password" -e MYSQL_DATABASE=synthetic mysql:8.4.13 --ssl-ca=/certs/ca.pem --ssl-cert=/certs/server.pem --ssl-key=/certs/server.key --require_secure_transport=ON >/dev/null
for i in $(seq 1 60); do
 if docker exec "$pg" pg_isready -U postgres -d synthetic >/dev/null 2>&1 && docker exec "$my" mysqladmin ping -h 127.0.0.1 -uroot -p"$password" --ssl-mode=REQUIRED >/dev/null 2>&1; then break; fi
 sleep 1
done
pgport=$(docker port "$pg" 5432/tcp)
myport=$(docker port "$my" 3306/tcp)
cd "$(dirname "$0")/.."
DF_TLS_CA="$dir/ca.pem" DF_TLS_BAD_CA="$dir/bad.pem" DF_TLS_POSTGRES="$pgport" DF_TLS_MYSQL="$myport" go test -v ./internal/database -run '^TestRestrictedTLSRealDatabases$' -count=1

DF_TLS_CA="$dir/ca.pem" DF_TLS_BAD_CA="$dir/bad.pem" DF_TLS_POSTGRES="$pgport" DF_TLS_MYSQL="$myport" go test -v ./internal/mcp -run '^TestMCPRestrictedTLSRealDatabases$' -count=1

DF_TLS_CA="$dir/ca.pem" DF_TLS_BAD_CA="$dir/bad.pem" DF_TLS_POSTGRES="$pgport" DF_TLS_MYSQL="$myport" DF_TLS_PASSWORD="$password" go test -race -v ./internal/workbuddy/onboardinghttp -run '^TestTLSUpdateRealDatabases$' -count=1
