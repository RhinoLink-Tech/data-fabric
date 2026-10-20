#!/usr/bin/env bash
set -euo pipefail

image=data-fabric:ci
if [ "$#" -gt 0 ]; then image="$1"; fi

translate() {
  local request="$1"
  printf '%s' "$request" | docker run --rm -i -e PYTHONPATH=/opt/data-fabric/python --entrypoint /usr/bin/python3 "$image" /app/scripts/sql_translate.py
}

mysql_to_postgres="$(translate '{"sql":"SELECT IFNULL(name, 1) FROM users","source_dialect":"mysql","target_dialect":"postgres"}')"
printf '%s' "$mysql_to_postgres" | grep -qi 'COALESCE'

postgres_to_trino="$(translate '{"sql":"SELECT id::VARCHAR FROM users","source_dialect":"postgres","target_dialect":"trino"}')"
printf '%s' "$postgres_to_trino" | grep -qi 'CAST'
