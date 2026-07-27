#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
run_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-platform-assistant-e2e.XXXXXX")
container="stratum-platform-assistant-e2e-pg-$(basename "$run_dir")"
password="platform_assistant_e2e_local_only"
database="stratum_platform_assistant_e2e"

cleanup() {
  if command -v docker >/dev/null 2>&1; then
    docker rm -f -- "$container" >/dev/null 2>&1 || true
  fi
  rm -rf -- "$run_dir"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

fail() {
  printf 'platform assistant E2E: %s\n' "$1" >&2
  exit 1
}

command -v docker >/dev/null 2>&1 || fail 'Docker is required'
command -v go >/dev/null 2>&1 || fail 'Go is required'
docker info >/dev/null 2>&1 || fail 'Docker daemon is unavailable'

docker run -d --name "$container" \
  --label stratum.test=platform-assistant-e2e \
  -e POSTGRES_PASSWORD="$password" \
  -e POSTGRES_DB="$database" \
  -p 127.0.0.1::5432 \
  postgres:16-alpine >"$run_dir/container-id"

ready=false
for _ in $(seq 1 60); do
  if docker exec "$container" pg_isready -U postgres -d "$database" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [[ "$ready" != true ]]; then
  docker logs "$container" >&2
  fail 'ephemeral PostgreSQL did not become ready'
fi

port_output=$(docker port "$container" 5432/tcp)
port=${port_output##*:}
[[ "$port" =~ ^[0-9]+$ ]] || fail 'Docker did not assign a PostgreSQL port'

export STRATUM_TEST_POSTGRES_URL="postgres://postgres:${password}@127.0.0.1:${port}/${database}?sslmode=disable"
export REQUIRE_PLATFORM_ASSISTANT_E2E=1

cd "$repo_root"
go test -v ./test/e2e \
  -run 'TestSystemAssistantProposal(PostgresAuthorizationSecretsAndConcurrency|RealServices)$' \
  -count=1
