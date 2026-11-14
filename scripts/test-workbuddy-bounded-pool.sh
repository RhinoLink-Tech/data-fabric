#!/bin/bash
set -euo pipefail
suffix=$(date +%s)-$$
pg=df-bounded-pg-$suffix
my=df-bounded-mysql-$suffix
cleanup(){ docker stop "$pg" "$my" >/dev/null 2>&1 || true; }
trap cleanup EXIT
docker run --rm -d --name "$pg" -p 127.0.0.1::5432 -e POSTGRES_PASSWORD=synthetic -e POSTGRES_USER=synthetic -e POSTGRES_DB=synthetic postgres:17-alpine >/dev/null
docker run --rm -d --name "$my" -p 127.0.0.1::3306 -e MYSQL_ROOT_PASSWORD=synthetic -e MYSQL_DATABASE=synthetic mysql:8.4.13 >/dev/null
for i in $(seq 1 60); do
 if docker exec "$pg" pg_isready -U synthetic -d synthetic >/dev/null 2>&1 && docker exec "$my" mysqladmin ping -h 127.0.0.1 -uroot -psynthetic >/dev/null 2>&1; then break; fi
 sleep 1
done
pgaddr=$(docker port "$pg" 5432/tcp)
myaddr=$(docker port "$my" 3306/tcp)
DF_BOUNDED_META_DSN="root:synthetic@tcp($myaddr)/synthetic?parseTime=true" DF_BOUNDED_PG_ENDPOINT="$pgaddr" go test -race -v ./internal/database -run TestBoundedPoolsConvergeAcrossInstances -count=1

DF_BOUNDED_META_DSN="root:synthetic@tcp($myaddr)/synthetic?parseTime=true" DF_BOUNDED_PG_ENDPOINT="$pgaddr" go test -race -v ./internal/workbuddy/onboardingruntime -run TestBusyFreshnessKeepsRealPaginationAndCursor -count=1
