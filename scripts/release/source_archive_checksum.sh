#!/usr/bin/env bash
# Create a portable SHA-256 record for a release source archive.
set -euo pipefail

if [[ $# -ne 1 || ! -f "$1" ]]; then
  echo "Usage: $0 PATH_TO_SOURCE_ARCHIVE" >&2
  exit 1
fi

archive_dir=$(dirname -- "$1")
archive_name=$(basename -- "$1")
(
  cd -- "$archive_dir"
  temporary=$(mktemp -- ".${archive_name}.sha256.tmp.XXXXXXXX")
  trap 'rm -f -- "$temporary"' EXIT
  sha256sum -- "$archive_name" > "$temporary"
  sha256sum --check --status -- "$temporary"
  mv -- "$temporary" "${archive_name}.sha256"
  trap - EXIT
  printf 'Created %s\n' "${archive_name}.sha256"
)
