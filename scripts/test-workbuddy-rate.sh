#!/bin/bash
set -euo pipefail
name=df-rate-test-$$
cleanup(){ docker stop "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT
docker run --rm -d --name "$name" -p 127.0.0.1::3306 -e MYSQL_ROOT_PASSWORD=synthetic -e MYSQL_DATABASE=synthetic mysql:8.4.13 >/dev/null
for i in $(seq 1 60); do
 if docker exec "$name" mysqladmin ping -h 127.0.0.1 -uroot -psynthetic >/dev/null 2>&1; then break; fi
 sleep 1
done
addr=$(docker port "$name" 3306/tcp)
DF_WB_E2E_MYSQL_DSN="root:synthetic@tcp($addr)/synthetic?parseTime=true" go test -race -v ./internal/workbuddy/onboardingruntime -run '^TestSharedOnboardingRateAcrossReplicasMySQL$' -count=1
