#!/usr/bin/env bash
# Bounded REST + MySQL integration smoke using synthetic data on DataSemanticHub MariaDB.
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IMAGE=""
DSH_DEPLOY_DIR="${DSH_DEPLOY_DIR:-/home/fengfei/DataSemanticHub/deploy}"
DB_CONTAINER="${DSH_MARIADB_CONTAINER:-datasemantichub-mariadb}"
NETWORK="${DATA_FABRIC_EXTERNAL_NETWORK:-datasemantichub-network}"
DB_HOST="${EXTERNAL_MYSQL_HOST:-mariadb}"
API_PORT="${API_PORT:-18180}"

usage() {
  cat <<'USAGE'
Usage: dsh-local-acceptance.sh --image ghcr.io/rhinolink-tech/data-fabric@sha256:<digest> [options]

Options:
  --deploy-dir PATH   DataSemanticHub deploy directory
  --db-container NAME Existing DataSemanticHub MariaDB container
  --network NAME      Existing Docker network shared with MariaDB
  --db-host NAME      MariaDB DNS name on that network
  --api-port PORT     Loopback port for the disposable Data Fabric API (default 18180)

The image must already exist locally. This command never pulls images.
USAGE
}
while (($#)); do
  case "$1" in
    --image) IMAGE="${2:-}"; shift 2 ;;
    --deploy-dir) DSH_DEPLOY_DIR="${2:-}"; shift 2 ;;
    --db-container) DB_CONTAINER="${2:-}"; shift 2 ;;
    --network) NETWORK="${2:-}"; shift 2 ;;
    --db-host) DB_HOST="${2:-}"; shift 2 ;;
    --api-port) API_PORT="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ ! "$IMAGE" =~ ^ghcr\.io/rhinolink-tech/data-fabric(:[[:alnum:]_.-]+)?@sha256:[a-f0-9]{64}$ ]]; then
  echo "Provide an immutable Data Fabric GHCR image reference ending in @sha256:<64 lowercase hex characters>." >&2
  exit 2
fi
if ! [[ "$API_PORT" =~ ^[0-9]{1,5}$ ]] || ((API_PORT < 1024 || API_PORT > 65535)); then
  echo "API port must be between 1024 and 65535." >&2
  exit 2
fi
if [[ ! -f "$DSH_DEPLOY_DIR/docker-compose.yaml" || ! -f "$DSH_DEPLOY_DIR/.env" ]]; then
  echo "DataSemanticHub deploy directory must contain docker-compose.yaml and .env." >&2
  exit 2
fi
if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  echo "Candidate image is not present locally; pull it separately before running this no-pull check." >&2
  exit 2
fi
if ! docker inspect "$DB_CONTAINER" >/dev/null 2>&1; then
  echo "Existing MariaDB container not found: $DB_CONTAINER" >&2
  exit 2
fi
if ! docker network inspect "$NETWORK" >/dev/null 2>&1; then
  echo "Existing Docker network not found: $NETWORK" >&2
  exit 2
fi
if ! (cd "$DSH_DEPLOY_DIR" && docker compose --project-name deploy --profile internal-db --env-file "$DSH_DEPLOY_DIR/.env" -f "$DSH_DEPLOY_DIR/docker-compose.yaml" config --services 2>/dev/null | grep -qx mariadb); then
  echo "The selected deploy directory does not expose the expected internal-db MariaDB service." >&2
  exit 2
fi
if ! python3 - "$API_PORT" <<'PYPORT'
import socket, sys
sock = socket.socket()
try:
    sock.bind(("127.0.0.1", int(sys.argv[1])))
except OSError:
    raise SystemExit(1)
finally:
    sock.close()
PYPORT
then
  echo "Loopback port $API_PORT is already in use." >&2
  exit 2
fi

WAS_RUNNING="$(docker inspect --format '{{.State.Running}}' "$DB_CONTAINER")"
STARTED_DSH=0
TMP="$(mktemp -d /tmp/data-fabric-dsh-acceptance.XXXXXX)"
PROJECT=""
DB_CREATED=0
META_DB=""
SOURCE_DB=""
META_USER=""
SOURCE_USER=""
cleanup() {
  local original_status=$?
  local cleanup_status=0
  trap - EXIT
  set +e
  if [[ -n "$PROJECT" && -f "$TMP/.env" ]]; then
    docker compose --project-name "$PROJECT" --env-file "$TMP/.env" -f "$ROOT/deploy/mcp/docker-compose.external-mysql.yml" down --remove-orphans >/dev/null 2>&1 || cleanup_status=1
  fi
  if [[ "$DB_CREATED" == 1 ]] && docker inspect "$DB_CONTAINER" >/dev/null 2>&1; then
    docker exec -i "$DB_CONTAINER" sh -lc 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mariadb -uroot -N' <<SQL >/dev/null 2>&1 || cleanup_status=1
DROP USER IF EXISTS '$META_USER'@'%';
DROP USER IF EXISTS '$SOURCE_USER'@'%';
DROP DATABASE IF EXISTS $META_DB;
DROP DATABASE IF EXISTS $SOURCE_DB;
SQL
  fi
  if [[ "$STARTED_DSH" == 1 ]]; then docker stop "$DB_CONTAINER" >/dev/null 2>&1 || cleanup_status=1; fi
  case "$TMP" in
    /tmp/data-fabric-dsh-acceptance.*) rm -rf -- "$TMP" || cleanup_status=1 ;;
    *) echo "Refusing unsafe temporary cleanup path." >&2; cleanup_status=1 ;;
  esac
  if [[ "$cleanup_status" == 0 ]]; then
    echo "CLEANUP=complete (temporary schemas/users/containers removed; original MariaDB state restored)"
  else
    echo "CLEANUP=needs-attention (inspect temporary acceptance resources before retrying)" >&2
  fi
  if [[ "$original_status" != 0 ]]; then exit "$original_status"; fi
  exit "$cleanup_status"
}
trap cleanup EXIT
chmod 700 "$TMP"

SUFFIX="$(date -u +%Y%m%d%H%M%S)$(openssl rand -hex 3)"
META_DB="data_fabric_acceptance_$SUFFIX"
SOURCE_DB="df_synthetic_source_$SUFFIX"
META_USER="dfmeta_$SUFFIX"
SOURCE_USER="dfread_$SUFFIX"
META_PASSWORD="$(openssl rand -hex 32)"
SOURCE_PASSWORD="$(openssl rand -hex 32)"
JWT_SECRET="$(openssl rand -hex 32)"
VAULT_KEY="$(openssl rand -base64 32)"
PROJECT="data-fabric-dsh-$SUFFIX"
DB_STATUS=""

if [[ "$WAS_RUNNING" != true ]]; then
  docker start "$DB_CONTAINER" >/dev/null
  STARTED_DSH=1
fi
for _ in $(seq 1 40); do
  DB_STATUS="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$DB_CONTAINER")"
  if [[ "$DB_STATUS" == healthy ]] && docker exec "$DB_CONTAINER" sh -lc 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mariadb -uroot -Nse "SELECT 1" >/dev/null' 2>/dev/null; then break; fi
  sleep 2
done
if [[ "$DB_STATUS" != healthy ]]; then
  echo "DataSemanticHub MariaDB did not become healthy." >&2
  exit 1
fi

cat > "$TMP/bootstrap.sql" <<SQL
CREATE DATABASE $META_DB CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE $SOURCE_DB CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER '$META_USER'@'%' IDENTIFIED BY '$META_PASSWORD';
GRANT ALL PRIVILEGES ON $META_DB.* TO '$META_USER'@'%';
CREATE USER '$SOURCE_USER'@'%' IDENTIFIED BY '$SOURCE_PASSWORD';
GRANT SELECT, SHOW VIEW ON $SOURCE_DB.* TO '$SOURCE_USER'@'%';
CREATE TABLE $SOURCE_DB.acceptance_fixture (id INT PRIMARY KEY, label VARCHAR(64) NOT NULL, amount DECIMAL(10,2) NOT NULL);
INSERT INTO $SOURCE_DB.acceptance_fixture (id,label,amount) VALUES (1,'synthetic-alpha',10.25),(2,'synthetic-beta',20.50);
SQL
chmod 600 "$TMP/bootstrap.sql"
DB_CREATED=1
docker exec -i "$DB_CONTAINER" sh -lc 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mariadb -uroot --default-character-set=utf8mb4' < "$TMP/bootstrap.sql"

KEYRING_JSON="$(python3 -c 'import json,sys; print(json.dumps({"acceptance":sys.argv[1]},separators=(",",":")))' "$VAULT_KEY")"
cat > "$TMP/.env" <<ENV
JWT_SECRET=$JWT_SECRET
DATA_FABRIC_IMAGE=$IMAGE
DATA_FABRIC_EXTERNAL_NETWORK=$NETWORK
EXTERNAL_MYSQL_HOST=$DB_HOST
EXTERNAL_MYSQL_PORT=3306
METADATA_DB_NAME=$META_DB
METADATA_DB_USER=$META_USER
METADATA_DB_PASSWORD=$META_PASSWORD
DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=acceptance
DATA_FABRIC_CREDENTIAL_KEYS=$KEYRING_JSON
API_PORT=$API_PORT
ENV
chmod 600 "$TMP/.env"
python3 "$ROOT/scripts/mcp/onboard.py" token --directory "$TMP" --user local-dsh-acceptance --tenant local-dsh-acceptance > "$TMP/token"
chmod 600 "$TMP/token"

compose() {
  docker compose --project-name "$PROJECT" --env-file "$TMP/.env" -f "$ROOT/deploy/mcp/docker-compose.external-mysql.yml" "$@"
}
compose config --quiet
compose up -d --pull never
ready=0
for _ in $(seq 1 60); do
  if curl --fail --silent "http://127.0.0.1:$API_PORT/health" >/dev/null 2>&1; then ready=1; break; fi
  sleep 2
done
if [[ "$ready" != 1 ]]; then
  echo "Candidate API did not become healthy; temporary stack will be removed." >&2
  exit 1
fi

export DSH_ACCEPTANCE_FILE="$TMP/acceptance.json"
python3 - "$TMP/token" "$TMP/acceptance.json" "$SOURCE_DB" "$SOURCE_USER" "$SOURCE_PASSWORD" "$API_PORT" "$DB_HOST" <<'PYSTATE'
import json, sys
from pathlib import Path
Path(sys.argv[2]).write_text(json.dumps({
    "token": Path(sys.argv[1]).read_text().strip(),
    "source_database": sys.argv[3],
    "source_user": sys.argv[4],
    "source_password": sys.argv[5],
    "api_port": int(sys.argv[6]),
    "source_host": sys.argv[7],
}), encoding="utf-8")
Path(sys.argv[2]).chmod(0o600)
PYSTATE
python3 - <<'PYACCEPT'
import json, os, urllib.error, urllib.request
with open(os.environ["DSH_ACCEPTANCE_FILE"], encoding="utf-8") as source:
    state = json.load(source)
base = f"http://127.0.0.1:{state['api_port']}"
def call(method, path, payload=None, auth=True):
    body = None if payload is None else json.dumps(payload).encode()
    headers = {"Content-Type":"application/json"}
    if auth:
        headers["Authorization"] = "Bearer " + state["token"]
    request = urllib.request.Request(base + path, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return response.status, json.loads(response.read())
    except urllib.error.HTTPError as error:
        return error.code, error.read().decode(errors="replace")
if call("GET", "/health", auth=False)[0] != 200:
    raise SystemExit("Candidate API health check failed.")
print("PASS candidate API health")
if call("GET", "/api/v1/datasources", auth=False)[0] not in (401, 403):
    raise SystemExit("Management API accepted an unauthenticated request.")
print("PASS unauthenticated management request rejected")
source = {
    "name":"dsh-synthetic-acceptance",
    "type":"mysql",
    "config":{
        "host":state["source_host"],"port":3306,
        "database":state["source_database"],
        "schema":state["source_database"],
        "username":state["source_user"],
        "password":state["source_password"],
    },
}
status, envelope = call("POST", "/api/v1/datasources", source)
if status != 201 or not isinstance(envelope, dict) or envelope.get("success") is not True:
    raise SystemExit("Synthetic MySQL source registration failed.")
source_id = envelope.get("data", {}).get("id", "")
if not source_id:
    raise SystemExit("Data source registration returned no ID.")
print("PASS synthetic source registration on DataSemanticHub MariaDB")
query = {"dataSourceId":source_id,"sql":"SELECT id, label, amount FROM acceptance_fixture ORDER BY id","type":1,"batch_size":10}
status, envelope = call("POST", "/api/v1/fetch", query)
if status != 200 or not isinstance(envelope, dict) or envelope.get("success") is not True:
    raise SystemExit("Synthetic source query failed.")
rows = [list(row) for row in envelope.get("data", {}).get("entries", [])]
if len(rows) != 2 or int(rows[0][0]) != 1 or str(rows[0][1]) != "synthetic-alpha" or int(rows[1][0]) != 2 or str(rows[1][1]) != "synthetic-beta":
    raise SystemExit("Synthetic query returned unexpected rows.")
print("PASS cross-container query returned 2 synthetic rows")
print(f"DATASOURCE_ID={source_id}")
PYACCEPT

REVISION="$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$IMAGE")"
PLATFORM="$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$IMAGE")"
printf 'CANDIDATE=%s\nSOURCE_REVISION=%s\nPLATFORM=%s\n' "$IMAGE" "$REVISION" "$PLATFORM"
