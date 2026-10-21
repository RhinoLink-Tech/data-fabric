#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_DIR="$(mktemp -d)"
MODULE_PATH="$TMP_DIR/sql-translation-python"

cleanup() {
  rm -rf -- "$TMP_DIR"
}
trap cleanup EXIT INT TERM

command -v docker >/dev/null || { echo "docker is required" >&2; exit 1; }
command -v go >/dev/null || { echo "go is required" >&2; exit 1; }
command -v python3 >/dev/null || { echo "python3 is required" >&2; exit 1; }

mkdir -p "$MODULE_PATH"
docker run --rm --user "$(id -u):$(id -g)" \
  --volume "$MODULE_PATH:/runtime" \
  --volume "$ROOT_DIR/requirements-sql-translation.txt:/tmp/requirements-sql-translation.txt:ro" \
  python:3.12-alpine3.22 \
  python -m pip install --quiet --no-cache-dir --target=/runtime --requirement /tmp/requirements-sql-translation.txt

export SQL_TRANSLATION_PYTHON_MODULE_PATH="$MODULE_PATH"
cd "$ROOT_DIR"
go run ./scripts/performance/postgres_gateway_benchmark.go "$@"
