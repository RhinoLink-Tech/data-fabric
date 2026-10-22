#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
container_name="data-fabric-s3-contract-$$"
garage_dir="$(mktemp -d)"
garage_image="dxflrs/garage:v2.4.1@sha256:9c96caa2612d3411acc5b0e6701fb238dbfba33e533a6d7d3d811a4b12d0d020"
access_key="GK0123456789ABCDEF0123456789ABCDEF"
secret_key="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
bucket="data-fabric-contract"

cleanup() {
	docker rm --force "$container_name" >/dev/null 2>&1 || true
	rm -rf "$garage_dir"
}
trap cleanup EXIT

cat >"$garage_dir/garage.toml" <<'EOF'
metadata_dir = "/tmp/meta"
data_dir = "/tmp/data"
db_engine = "sqlite"
replication_factor = 1
rpc_bind_addr = "[::]:3901"
rpc_public_addr = "127.0.0.1:3901"
rpc_secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

[s3_api]
s3_region = "us-east-1"
api_bind_addr = "[::]:3900"
root_domain = ".s3.garage.localhost"
advertise_endpoint = "http://127.0.0.1:3900"
EOF

docker run --detach \
	--name "$container_name" \
	--publish 3900:3900 \
	--publish 3901:3901 \
	--volume "$garage_dir/garage.toml:/etc/garage.toml:ro" \
	--env GARAGE_DEFAULT_ACCESS_KEY="$access_key" \
	--env GARAGE_DEFAULT_SECRET_KEY="$secret_key" \
	--env GARAGE_DEFAULT_BUCKET="$bucket" \
	"$garage_image" /garage server --single-node --default-bucket >/dev/null

ready=false
for _ in $(seq 1 60); do
	if docker exec "$container_name" /garage status >/dev/null 2>&1; then
		ready=true
		break
	fi
	sleep 1
done

if [[ "$ready" != true ]]; then
	docker logs "$container_name" >&2
	echo "Garage did not become ready within 60 seconds" >&2
	exit 1
fi

cd "$repo_root"
DATA_FABRIC_S3_CONTRACT_ENDPOINT="http://127.0.0.1:3900" \
DATA_FABRIC_S3_CONTRACT_REGION="us-east-1" \
DATA_FABRIC_S3_CONTRACT_ACCESS_KEY="$access_key" \
DATA_FABRIC_S3_CONTRACT_SECRET_KEY="$secret_key" \
DATA_FABRIC_S3_CONTRACT_BUCKET="$bucket" \
	go test ./internal/database/drivers/object_storage -run '^TestS3CSVContract$' -count=1 -v || {
		docker logs "$container_name" >&2
		exit 1
	}
