#!/usr/bin/env bash
# Disposable synthetic fixtures only. Never point this test at a real database.
set -euo pipefail
suffix="$(date +%s)-$$"
pg="df-optin-test-pg-$suffix"
my="df-optin-test-mysql-$suffix"
cleanup() { docker stop "$pg" "$my" >/dev/null 2>&1 || true; }
trap cleanup EXIT
docker run --rm -d --name "$pg" -p 127.0.0.1::5432 -e POSTGRES_USER=reader -e POSTGRES_PASSWORD=synthetic-password -e POSTGRES_DB=orders postgres:17-alpine >/dev/null
docker run --rm -d --name "$my" -p 127.0.0.1::3306 -e MYSQL_ROOT_PASSWORD=synthetic-test-only -e MYSQL_DATABASE=isolated_test mysql:8.4.13 >/dev/null
ready=false
for _ in $(seq 1 60); do
 if docker exec "$pg" pg_isready -U reader -d orders >/dev/null 2>&1 && docker exec "$my" mysqladmin ping -h 127.0.0.1 -uroot -psynthetic-test-only --silent >/dev/null 2>&1; then ready=true; break; fi
 sleep 1
done
if [ "$ready" != true ]; then echo "Synthetic fixtures did not start" >&2; exit 1; fi
pgport="$(docker port "$pg" 5432/tcp)"; pgport="${pgport##*:}"
myport="$(docker port "$my" 3306/tcp)"; myport="${myport##*:}"
dsn="root:synthetic-test-only@tcp(127.0.0.1:$myport)/isolated_test?parseTime=true"
cd "$(dirname "$0")/.."
DF_ONBOARDING_TEST_MYSQL_DSN="$dsn" go test -race -count=1 ./internal/security/onboarding -run '^TestMySQLCompletedSetupsAllowUpdate$'
go test -race -count=1 ./cmd/server -run '^TestWorkbuddyRequiresIsolatedRestrictedConfiguration$'
DF_SINGLETON_TEST_MYSQL_DSN="$dsn" go test -race -count=1 ./internal/workbuddy/singleton ./internal/workbuddy/onboardingruntime -run 'TestMySQLExclusionAndLostSession|TestMySQLUpdateGuardLossStopsRequestsAndPools'
DF_GUARD_TEST_MYSQL_DSN="$dsn" go test -race -count=1 ./cmd/server -run '^TestWorkBuddyMainMuxGuardProcessLoss$'
# This test modifies its own disposable MySQL tables and synthetic PG password.
DF_WB_E2E_MYSQL_DSN="$dsn" DF_WB_E2E_POSTGRES_PORT="$pgport" DF_WB_E2E_UPDATE=true go test -race -count=1 ./internal/workbuddy/onboardingruntime -run '^TestSignedOIDCAndMCPBrowserLifecycle$'
