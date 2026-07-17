#!/usr/bin/env bash
set -euo pipefail
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
container="stratum-workflow-http-e2e-pg"
password="workflow_e2e_local_only"
database="stratum_e2e"
cleanup() { docker rm -f "$container" >/dev/null 2>&1 || true; }
trap cleanup EXIT
cleanup
docker run -d --name "$container" -e POSTGRES_PASSWORD="$password" -e POSTGRES_DB="$database" -p 127.0.0.1::5432 postgres:16-alpine >/dev/null
ready=false
for _ in $(seq 1 60); do
  if docker exec "$container" pg_isready -U postgres -d "$database" >/dev/null 2>&1; then ready=true; break; fi
  sleep 1
done
if [[ "$ready" != true ]]; then docker logs "$container"; exit 1; fi
port=$(docker port "$container" 5432/tcp | python3 -c 'import sys; print(sys.stdin.read().strip().rsplit(":", 1)[-1])')
export STRATUM_TEST_POSTGRES_URL="postgres://postgres:${password}@127.0.0.1:${port}/${database}?sslmode=disable"
gofmt -w test/e2e/workflow_lifecycle_test.go
go test -tags=integration ./test/e2e -run TestWorkflowHTTPPostgresWorkerApprovalRestartAndSSEE2E -count=1 -v
