#!/usr/bin/env bash
# Verify the PostgreSQL quickstart with disposable synthetic data in CI.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT/deploy/mcp"
: "${COMPOSE_PROJECT_NAME:?set an isolated CI project name}"
: "${DATA_FABRIC_IMAGE:?set the locally built candidate image}"
if [[ ! "$COMPOSE_PROJECT_NAME" =~ ^df-pg18-ci-[0-9]+-[0-9]+$ ]]; then
  echo "Refusing to use a non-isolated Compose project name" >&2
  exit 2
fi
export MCP_OIDC_ISSUER="${MCP_OIDC_ISSUER:-https://issuer.example.invalid}"
export MCP_OIDC_AUDIENCE="${MCP_OIDC_AUDIENCE:-pg18-acceptance}"
export API_PORT="${API_PORT:-8080}"
python3 ../../scripts/mcp/onboard.py init --directory "$PWD"
compose=(docker compose --project-name "$COMPOSE_PROJECT_NAME" --env-file .env -f docker-compose.yml -f docker-compose.postgres.yml)
cleanup() { "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT

python3 - <<'PYCONFIG'
import json
import os
import subprocess

base = ['docker', 'compose', '--project-name', os.environ['COMPOSE_PROJECT_NAME'],
        '--env-file', '.env', '-f', 'docker-compose.yml']
def config(args):
    return json.loads(subprocess.check_output(base + args + ['config', '--format', 'json']))
mysql = config([])['services']['sample-source']
postgres = config(['-f', 'docker-compose.postgres.yml'])['services']['sample-source']
mysql_volumes = [v for v in mysql['volumes'] if v['target'] == '/var/lib/mysql']
pg_volumes = [v for v in postgres['volumes'] if v['target'] == '/var/lib/postgresql']
if not mysql['image'].startswith('mysql:') or len(mysql_volumes) != 1:
    raise SystemExit('Default MySQL sample or volume changed')
if postgres['image'] != 'postgres:18' or len(pg_volumes) != 1:
    raise SystemExit('PostgreSQL 18 sample or volume is incorrect')
if mysql_volumes[0]['source'] == pg_volumes[0]['source']:
    raise SystemExit('MySQL and PostgreSQL sample volumes must be separate')
if any(v['target'] == '/var/lib/postgresql/data' for v in postgres['volumes']):
    raise SystemExit('PostgreSQL 18 still mounts the old data path')
print('PASS: default MySQL and PostgreSQL sample volumes are separate')
PYCONFIG

"${compose[@]}" up --no-build --detach --wait --wait-timeout 240 metadata-db sample-source data-fabric-api
export DF_PG18_TEST_TOKEN="$(python3 ../../scripts/mcp/onboard.py token --user pg18-acceptance --tenant quickstart)"
export DF_PG18_SAMPLE_PASSWORD="$(python3 -c 'import sys; sys.path.insert(0, "../../scripts/mcp"); import onboard; from pathlib import Path; print(onboard.parse_env(Path(".env"))["SAMPLE_READER_PASSWORD"])')"
python3 - <<'PYACCEPT'
import json
import os
import subprocess
import urllib.request

base = 'http://127.0.0.1:' + os.environ['API_PORT']
token = os.environ['DF_PG18_TEST_TOKEN']
password = os.environ['DF_PG18_SAMPLE_PASSWORD']
compose = ['docker', 'compose', '--project-name', os.environ['COMPOSE_PROJECT_NAME'],
           '--env-file', '.env', '-f', 'docker-compose.yml', '-f', 'docker-compose.postgres.yml']
def call(path, payload):
    request = urllib.request.Request(base + path, data=json.dumps(payload).encode(),
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'}, method='POST')
    with urllib.request.urlopen(request, timeout=30) as response:
        result = json.load(response)
        if not result.get('success'):
            raise SystemExit('API request failed: ' + path)
        return result['data']
source = call('/api/v1/datasources', {'name': 'pg18-synthetic-acceptance', 'type': 'postgresql',
    'config': {'host': 'sample-source', 'port': 5432, 'database': 'sample', 'schema': 'public',
               'username': 'sample_reader', 'password': password}})
source_id = source['id']
def query_ids():
    result = call('/api/v1/fetch', {'dataSourceId': source_id,
        'sql': 'SELECT id FROM public.orders ORDER BY id', 'type': 1, 'batch_size': 10})
    ids = [int(row[0]) for row in result['entries']]
    if ids != [1, 2, 3]:
        raise SystemExit('PostgreSQL sample query returned unexpected IDs')
query_ids()
psql = compose + ['exec', '-T', '-e', 'PGPASSWORD=' + password, 'sample-source',
                  'psql', '-h', '127.0.0.1', '-U', 'sample_reader', '-d', 'sample', '-v', 'ON_ERROR_STOP=1']
write = subprocess.run(psql + ['-c', 'DELETE FROM public.orders WHERE id=1'], capture_output=True)
if write.returncode == 0:
    raise SystemExit('The PostgreSQL sample reader unexpectedly has write access')
subprocess.run(compose + ['up', '--detach', '--no-deps', '--force-recreate', '--wait',
                          '--wait-timeout', '180', 'sample-source'], check=True)
query_ids()
print('PASS: PostgreSQL 18 seed, read-only role, gateway query, and volume persistence')
PYACCEPT
