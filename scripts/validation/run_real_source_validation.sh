#!/usr/bin/env bash
# Provisions ephemeral MySQL 8.4 and PostgreSQL 18 containers, creates a
# SELECT-only reader account in each, runs the real-source validation
# harness (scripts/validation/real_source_validation.go) against them, and
# tears everything down afterwards (#35, D14).
#
# Usage:
#   ./scripts/validation/run_real_source_validation.sh             # both engines
#   ./scripts/validation/run_real_source_validation.sh mysql       # engine subset
#
# The container passwords are generated per run with openssl and never
# leave the process environment; nothing is committed.
#
# Against external databases, skip this script: export
# DF_VALIDATE_<ENGINE>_HOST/PORT/DATABASE/ADMIN_USER/ADMIN_PASSWORD/
# READER_USER/READER_PASSWORD yourself and run
#   go run ./scripts/validation
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/../.." && pwd)
cd "$repo_root"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing prerequisite: $1" >&2
    exit 2
  }
}
need docker
need go
need openssl

engines="${*:-mysql postgres}"

mysql_root_pw=$(openssl rand -hex 16)
pg_root_pw=$(openssl rand -hex 16)
reader_pw=$(openssl rand -hex 16)
reader=df_reader
db=data_fabric_validation
run_id="df$(printf '%x' "$(date +%s)")"

containers=()
bin_dir=""
cleanup() {
  for container in "${containers[@]:-}"; do
    docker rm -f -v "$container" >/dev/null 2>&1 || true
  done
  [ -n "$bin_dir" ] && rm -rf "$bin_dir"
}
trap cleanup EXIT

mapped_port() { # container, container port
  docker port "$1" "$2/tcp" | sed 's/.*://' | head -1
}

start_mysql() {
  local name="dfval-mysql-$run_id"
  echo "starting mysql:8.4 ($name)..."
  docker run -d -P --name "$name" \
    -e MYSQL_ROOT_PASSWORD="$mysql_root_pw" \
    -e MYSQL_DATABASE="$db" \
    mysql:8.4 >/dev/null
  containers+=("$name")

  echo "waiting for mysql to accept connections..."
  # The entrypoint restarts mysqld after its init phase; a single success
  # can hit the temporary server, so require several in a row.
  local ready=0 i
  for i in $(seq 1 180); do
    if docker exec "$name" mysql -uroot -p"$mysql_root_pw" -e 'SELECT 1' >/dev/null 2>&1; then
      ready=$((ready + 1))
      if [ "$ready" -ge 5 ]; then
        break
      fi
    else
      ready=0
    fi
    if [ "$i" -eq 180 ]; then
      echo "mysql did not become ready in 180s" >&2
      exit 2
    fi
    sleep 1
  done

  # The reader carries INSERT as well: the matrix proves the gateway's
  # session-level read-only enforcement blocks writes even from a
  # write-capable account (#20's threat model).
  docker exec "$name" mysql -uroot -p"$mysql_root_pw" -e \
    "CREATE USER '$reader'@'%' IDENTIFIED BY '$reader_pw'; GRANT SELECT, INSERT ON \`$db\`.* TO '$reader'@'%'; FLUSH PRIVILEGES;" >/dev/null 2>&1

  DF_VALIDATE_MYSQL_HOST=127.0.0.1
  DF_VALIDATE_MYSQL_PORT=$(mapped_port "$name" 3306)
  export DF_VALIDATE_MYSQL_HOST DF_VALIDATE_MYSQL_PORT
  export DF_VALIDATE_MYSQL_DATABASE="$db"
  export DF_VALIDATE_MYSQL_ADMIN_USER=root
  export DF_VALIDATE_MYSQL_ADMIN_PASSWORD="$mysql_root_pw"
  export DF_VALIDATE_MYSQL_READER_USER="$reader"
  export DF_VALIDATE_MYSQL_READER_PASSWORD="$reader_pw"
  echo "mysql ready on 127.0.0.1:$DF_VALIDATE_MYSQL_PORT (reader: SELECT+INSERT, write-capable for read-only probe)"
}

start_postgres() {
  local name="dfval-postgres-$run_id"
  echo "starting postgres:18 ($name)..."
  docker run -d -P --name "$name" \
    -e POSTGRES_PASSWORD="$pg_root_pw" \
    -e POSTGRES_DB="$db" \
    postgres:18 >/dev/null
  containers+=("$name")

  echo "waiting for postgres to accept connections..."
  # Same restart race as mysql: require consecutive successes.
  local ready=0 i
  for i in $(seq 1 180); do
    if docker exec "$name" pg_isready -U postgres >/dev/null 2>&1; then
      ready=$((ready + 1))
      if [ "$ready" -ge 5 ]; then
        break
      fi
    else
      ready=0
    fi
    if [ "$i" -eq 180 ]; then
      echo "postgres did not become ready in 180s" >&2
      exit 2
    fi
    sleep 1
  done

  # The harness creates its fixture tables as the postgres role; the
  # default-privileges grant covers them for the reader, which carries
  # INSERT so the session-level read-only probe has a privilege to defeat.
  docker exec "$name" psql -U postgres -d "$db" -v ON_ERROR_STOP=1 -c \
    "CREATE ROLE $reader LOGIN PASSWORD '$reader_pw';" \
    -c "GRANT USAGE ON SCHEMA public TO $reader;" \
    -c "ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public GRANT SELECT, INSERT ON TABLES TO $reader;" >/dev/null

  DF_VALIDATE_POSTGRES_HOST=127.0.0.1
  DF_VALIDATE_POSTGRES_PORT=$(mapped_port "$name" 5432)
  export DF_VALIDATE_POSTGRES_HOST DF_VALIDATE_POSTGRES_PORT
  export DF_VALIDATE_POSTGRES_DATABASE="$db"
  export DF_VALIDATE_POSTGRES_ADMIN_USER=postgres
  export DF_VALIDATE_POSTGRES_ADMIN_PASSWORD="$pg_root_pw"
  export DF_VALIDATE_POSTGRES_READER_USER="$reader"
  export DF_VALIDATE_POSTGRES_READER_PASSWORD="$reader_pw"
  export DF_VALIDATE_POSTGRES_SSLMODE=disable
  echo "postgres ready on 127.0.0.1:$DF_VALIDATE_POSTGRES_PORT (reader: SELECT+INSERT, write-capable for read-only probe)"
}

selected=""
for engine in $engines; do
  case "$engine" in
    mysql) start_mysql ;;
    postgres | pg) start_postgres ;;
    *)
      echo "unknown engine: $engine (supported: mysql postgres)" >&2
      exit 2
      ;;
  esac
  selected="$selected ${engine/pg/postgres}"
done

export DF_VALIDATE_ENGINES=$(echo "$selected" | xargs)
echo
# Build once and run the binary: `go run` collapses every non-zero exit to
# 1, and the harness's three-way exit code is part of its contract.
bin_dir=$(mktemp -d)
bin="$bin_dir/real_source_validation"
go build -o "$bin" ./scripts/validation
# Propagates the harness exit code: 0 pass, 1 validation failure,
# 2 environment incomplete.
"$bin"
