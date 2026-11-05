#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
work_dir="$(mktemp -d)"
chmod 755 "$work_dir"
compose_file="$work_dir/compose.yaml"
compose_project="data-fabric-hdfs-columnar-contract-$RANDOM-$RANDOM"
hadoop_image="${DATA_FABRIC_HDFS_IMAGE:-apache/hadoop:3.5.0@sha256:46980ea1653eb3453d9cf43c4b7cb69c4729e19612d4902da0372d11e11a7a12}"
expected_version="${DATA_FABRIC_HDFS_EXPECTED_VERSION:-3.5.0}"
compose=(docker compose --project-name "$compose_project" --file "$compose_file")

cleanup() {
  "${compose[@]}" down --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
on_error() {
  "${compose[@]}" logs --no-color >&2 || true
}
trap cleanup EXIT
trap on_error ERR

for command in docker go; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$command" >&2
    exit 1
  fi
done
docker compose version >/dev/null

cd "$repo_root"
CGO_ENABLED=0 go test -c -o "$work_dir/hdfs-columnar-contract.test" ./internal/database/drivers/file_system
chmod 755 "$work_dir/hdfs-columnar-contract.test"

cat >"$compose_file" <<COMPOSE
services:
  namenode:
    image: "$hadoop_image"
    hostname: namenode
    command: ["hdfs", "namenode"]
    environment:
      ENSURE_NAMENODE_DIR: "/tmp/hadoop-root/dfs/name"
      CORE-SITE.XML_fs.defaultFS: "hdfs://namenode:8020"
      HDFS-SITE.XML_dfs.namenode.rpc-address: "namenode:8020"
      HDFS-SITE.XML_dfs.replication: "1"
  datanode:
    image: "$hadoop_image"
    hostname: datanode
    command: ["hdfs", "datanode"]
    environment:
      CORE-SITE.XML_fs.defaultFS: "hdfs://namenode:8020"
      HDFS-SITE.XML_dfs.namenode.rpc-address: "namenode:8020"
      HDFS-SITE.XML_dfs.replication: "1"
  contract:
    image: "$hadoop_image"
    entrypoint: ["/hdfs-columnar-contract.test"]
    command: ["-test.run=^TestHDFS(Parquet|ORC)DriverContract$", "-test.count=1", "-test.v"]
    environment:
      DATA_FABRIC_HDFS_CONTRACT_HOST: "namenode"
      DATA_FABRIC_HDFS_CONTRACT_PORT: "8020"
      DATA_FABRIC_HDFS_CONTRACT_USERNAME: "hadoop"
      DATA_FABRIC_HDFS_ORC_CONTRACT_FIXTURE: "/fixtures/orc_split_elim.orc"
    volumes:
      - "$work_dir/hdfs-columnar-contract.test:/hdfs-columnar-contract.test:ro"
      - "$repo_root/internal/database/drivers/object_storage/testdata/orc_split_elim.orc:/fixtures/orc_split_elim.orc:ro"
COMPOSE

"${compose[@]}" config >/dev/null
"${compose[@]}" up --detach namenode datanode

ready=false
for _ in $(seq 1 120); do
  version_line="$("${compose[@]}" exec --no-TTY namenode hadoop version 2>/dev/null | head -n 1 || true)"
  if [ "$version_line" = "Hadoop $expected_version" ]; then
    report="$("${compose[@]}" exec --no-TTY namenode hdfs dfsadmin -report 2>/dev/null || true)"
    if printf '%s\n' "$report" | grep -Fq 'Live datanodes (1)'; then
      ready=true
      break
    fi
  fi
  sleep 2
done
if [ "$ready" != true ]; then
  printf 'Hadoop %s NameNode/DataNode cluster did not become ready\n' "$expected_version" >&2
  "${compose[@]}" logs --no-color >&2 || true
  exit 1
fi

"${compose[@]}" exec --no-TTY namenode hdfs dfs -mkdir -p /warehouse
"${compose[@]}" run --rm --no-deps contract
