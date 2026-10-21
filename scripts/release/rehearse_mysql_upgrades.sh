#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

MYSQL_IMAGE="${MYSQL_IMAGE:-mysql@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b}"
RUN_ID="$(date -u +%Y%m%d%H%M%S)_${RANDOM}"
CONTAINER="data-fabric-mysql-rehearsal-${RUN_ID}"
DB="data_fabric_rehearsal_${RUN_ID}"
FAIL_DB="data_fabric_rehearsal_failclosed_${RUN_ID}"
ROOT_PASSWORD="$(openssl rand -hex 24)"
TMP_DIR="$(mktemp -d)"
SQL_TRANSLATION_MODULE_PATH="$TMP_DIR/sql-translation-python"
SERVER_PID=""

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  rm -rf -- "$TMP_DIR"
}
trap cleanup EXIT INT TERM

command -v docker >/dev/null || fail "docker is required"
command -v go >/dev/null || fail "go is required"
command -v openssl >/dev/null || fail "openssl is required"
command -v python3 >/dev/null || fail "python3 is required for the server startup check"
# The rehearsal starts the Go server outside the application container. Install
# the same pinned, pure-Python SQLGlot dependency into its temporary directory
# so the startup check reaches the metadata credential-migration gate even on
# hosts without a system SQLGlot installation. Install as the invoking user so
# the host-side cleanup can remove the temporary package without elevated rights.
mkdir -p "$SQL_TRANSLATION_MODULE_PATH"
docker run --rm --user "$(id -u):$(id -g)" \
  --volume "$SQL_TRANSLATION_MODULE_PATH:/runtime" \
  --volume "$ROOT_DIR/requirements-sql-translation.txt:/tmp/requirements-sql-translation.txt:ro" \
  python:3.12-alpine3.22 \
  python -m pip install --no-cache-dir --target=/runtime --requirement /tmp/requirements-sql-translation.txt >/dev/null

docker image inspect "$MYSQL_IMAGE" >/dev/null 2>&1 || docker pull "$MYSQL_IMAGE"

docker run --detach --name "$CONTAINER" --pull=missing \
  --publish 127.0.0.1::3306 \
  --env MYSQL_ROOT_PASSWORD="$ROOT_PASSWORD" \
  --env MYSQL_DATABASE="$DB" \
  "$MYSQL_IMAGE" >/dev/null

mysql_exec() {
  docker exec --env MYSQL_PWD="$ROOT_PASSWORD" "$CONTAINER" mysql --user=root "$@"
}

ready=0
for _ in $(seq 1 90); do
  if mysql_exec --execute='SELECT 1' >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
[[ "$ready" == "1" ]] || fail "MySQL did not become ready"

published="$(docker port "$CONTAINER" 3306/tcp | head -n 1)"
MYSQL_PORT="${published##*:}"
[[ "$MYSQL_PORT" =~ ^[0-9]+$ ]] || fail "could not determine the temporary MySQL port"

VERSION="$(mysql_exec --batch --skip-column-names --execute='SELECT VERSION()')"
IMAGE_ID="$(docker image inspect "$MYSQL_IMAGE" --format '{{.Id}}')"
IMAGE_DIGESTS="$(docker image inspect "$MYSQL_IMAGE" --format '{{json .RepoDigests}}')"
GO_VERSION="$(go version)"
DSN="root:${ROOT_PASSWORD}@tcp(127.0.0.1:${MYSQL_PORT})/${DB}?charset=utf8mb4&parseTime=True&loc=Local"
FAIL_DSN="root:${ROOT_PASSWORD}@tcp(127.0.0.1:${MYSQL_PORT})/${FAIL_DB}?charset=utf8mb4&parseTime=True&loc=Local"

echo "MySQL server: $VERSION"
echo "Image: $MYSQL_IMAGE"
echo "Image ID: $IMAGE_ID"
echo "Image digests: $IMAGE_DIGESTS"
echo "Go: $GO_VERSION"
echo "Running the v1.2.0 plaintext and recent encrypted upgrade paths..."
DATA_FABRIC_MYSQL_REHEARSAL_DSN="$DSN" go test -tags mysql_rehearsal -v ./internal/repository -run '^TestMySQLUpgradePaths$' -count=1

mysql_exec --execute="CREATE DATABASE $FAIL_DB CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"
echo "Preparing the missing-key fixture and checking the real server startup gate..."
DATA_FABRIC_MYSQL_REHEARSAL_DSN="$FAIL_DSN" go test -tags mysql_rehearsal ./internal/repository -run '^TestMySQLFailClosedFixture$' -count=1
go build -o "$TMP_DIR/data-fabric-server" ./cmd/server

set +e
env \
  DATABASE_HOST=127.0.0.1 \
  DATABASE_PORT="$MYSQL_PORT" \
  DATABASE_DATABASE="$FAIL_DB" \
  DATABASE_USERNAME=root \
  DATABASE_PASSWORD="$ROOT_PASSWORD" \
  SECURITY_JWT_SECRET='rehearsal-only-jwt-secret-2026-10-20-7c4f7c2d' \
  SQL_TRANSLATION_PYTHON_MODULE_PATH="$SQL_TRANSLATION_MODULE_PATH" \
  DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=current \
  DATA_FABRIC_CREDENTIAL_KEYS='{"current":"Y2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2NjY2M="}' \
  SERVER_HOST=127.0.0.1 \
  SERVER_PORT=38197 \
  SERVER_MODE=test \
  LOGGING_LEVEL=error \
  "$TMP_DIR/data-fabric-server" >"$TMP_DIR/failclosed.log" 2>&1 &
SERVER_PID=$!
set -e

for _ in $(seq 1 30); do
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    break
  fi
  if grep -Fq 'Starting server on port' "$TMP_DIR/failclosed.log"; then
    cat "$TMP_DIR/failclosed.log" >&2
    fail "server reached the listening stage despite the missing credential key"
  fi
  sleep 1
done

if kill -0 "$SERVER_PID" 2>/dev/null; then
  cat "$TMP_DIR/failclosed.log" >&2
  fail "server did not exit on the missing credential key"
fi
set +e
wait "$SERVER_PID"
SERVER_STATUS=$?
set -e
SERVER_PID=""
if [[ "$SERVER_STATUS" == "0" ]] || ! grep -Fq 'credential migration failed; refusing to start' "$TMP_DIR/failclosed.log"; then
  cat "$TMP_DIR/failclosed.log" >&2
  fail "startup did not fail closed with the expected credential migration error"
fi
FAIL_ROWS="$(mysql_exec "$FAIL_DB" --batch --skip-column-names --execute='SELECT COUNT(*) FROM data_sources')"
[[ "$FAIL_ROWS" == "2" ]] || fail "failed startup changed the missing-key fixture; row count is $FAIL_ROWS"
ROLLBACK_CHECK="$(mysql_exec "$FAIL_DB" --batch --skip-column-names --execute="SELECT config LIKE '%rehearsal-before-failure-secret%' FROM data_sources WHERE id='a-before-failure'")"
[[ "$ROLLBACK_CHECK" == "1" ]] || fail "credential migration did not roll back the earlier row update"
echo "Startup gate and credential transaction: passed (server exited before listen; earlier row update rolled back)"

BACKUP="$TMP_DIR/rehearsal.sql"
docker exec --env MYSQL_PWD="$ROOT_PASSWORD" "$CONTAINER" mysqldump --user=root --single-transaction --skip-comments "$DB" >"$BACKUP"
BACKUP_SHA="$(sha256sum "$BACKUP" | awk '{print $1}')"
mysql_exec --execute="DROP DATABASE $DB; CREATE DATABASE $DB CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"
docker exec --interactive --env MYSQL_PWD="$ROOT_PASSWORD" "$CONTAINER" mysql --user=root "$DB" <"$BACKUP"
echo "Backup SHA-256: $BACKUP_SHA"
echo "Verifying restored records, decryptability, and indexes..."
DATA_FABRIC_MYSQL_REHEARSAL_DSN="$DSN" go test -tags mysql_rehearsal -v ./internal/repository -run '^TestMySQLRestoredRecentFixture$' -count=1

echo "MySQL upgrade and recovery rehearsal passed."
