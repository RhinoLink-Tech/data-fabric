#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
minio_version="RELEASE.2025-04-22T22-12-26Z"
minio_commit="0d7408fc9969caf07de6a8c3a84f9fbb10a6739e"
minio_source_sha256="7eb30a913fea30f18069abf194e1e78e4983b558cc526911ae1c11396a9859a5"
work_dir="$(mktemp -d)"
minio_pid=""
access_key="minio-contract-access"
secret_key="minio-contract-secret-2026"
bucket="data-fabric-contract"

cleanup() {
	if [[ -n "$minio_pid" ]]; then
		kill "$minio_pid" >/dev/null 2>&1 || true
		wait "$minio_pid" 2>/dev/null || true
	fi
	rm -rf "$work_dir"
}
trap cleanup EXIT

source_archive="$work_dir/minio.tar.gz"
source_dir="$work_dir/source"
binary="$work_dir/minio"
data_dir="$work_dir/data"

curl --fail --location --silent --show-error \
	"https://codeload.github.com/minio/minio/tar.gz/$minio_commit" \
	--output "$source_archive"
printf '%s  %s\n' "$minio_source_sha256" "$source_archive" | sha256sum --check --status
mkdir -p "$source_dir" "$data_dir"
tar --extract --gzip --file "$source_archive" --directory "$source_dir" --strip-components=1

# MinIO no longer distributes this historical container image. Build the
# immutable upstream source revision and stamp the release values used by the
# official build instead.
ldflags="-s -w -X github.com/minio/minio/cmd.Version=2025-04-22T22:12:26Z -X github.com/minio/minio/cmd.CopyrightYear=2025 -X github.com/minio/minio/cmd.ReleaseTag=$minio_version -X github.com/minio/minio/cmd.CommitID=$minio_commit -X github.com/minio/minio/cmd.ShortCommitID=${minio_commit:0:12}"
(
	cd "$source_dir"
	CGO_ENABLED=0 go build -trimpath --ldflags "$ldflags" -o "$binary" .
)
"$binary" --version | grep --fixed-strings --quiet "$minio_version"

MINIO_ROOT_USER="$access_key" \
MINIO_ROOT_PASSWORD="$secret_key" \
	"$binary" server "$data_dir" --address :3902 >"$work_dir/minio.log" 2>&1 &
minio_pid=$!

ready=false
for _ in $(seq 1 60); do
	if curl --fail --silent --output /dev/null http://127.0.0.1:3902/minio/health/ready; then
		ready=true
		break
	fi
	sleep 1
done

if [[ "$ready" != true ]]; then
	cat "$work_dir/minio.log" >&2
	echo "MinIO did not become ready within 60 seconds" >&2
	exit 1
fi

cd "$repo_root"
DATA_FABRIC_MINIO_CONTRACT_ENDPOINT="http://127.0.0.1:3902" \
DATA_FABRIC_MINIO_CONTRACT_REGION="us-east-1" \
DATA_FABRIC_MINIO_CONTRACT_ACCESS_KEY="$access_key" \
DATA_FABRIC_MINIO_CONTRACT_SECRET_KEY="$secret_key" \
DATA_FABRIC_MINIO_CONTRACT_BUCKET="$bucket" \
DATA_FABRIC_MINIO_CONTRACT_VERSION="$minio_version" \
DATA_FABRIC_MINIO_CONTRACT_COMMIT="$minio_commit" \
	go test ./internal/database/drivers/object_storage -run '^TestMinIOCSVContract$' -count=1 -v || {
		cat "$work_dir/minio.log" >&2
		exit 1
	}
