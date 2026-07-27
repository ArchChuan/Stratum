#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
mode=${1:-short}
duration=${STATEFUL_E2E_DURATION_SEC:-}
case "$mode" in
  short) duration=${duration:-600} ;;
  soak) duration=${duration:-3600} ;;
  *) printf 'unsupported stateful E2E mode: %s\n' "$mode" >&2; exit 2 ;;
esac
[[ "$duration" =~ ^[0-9]+$ ]] && ((duration >= 600 && duration <= 14400)) || {
  printf 'STATEFUL_E2E_DURATION_SEC must be between 600 and 14400\n' >&2; exit 2;
}

all_packs='dashboard,iam,workflow,agent,skill,mcp,agent-skill-mcp,knowledge,memory,evaluation,agent-context,evaluation-promotion'
packs=${STATEFUL_E2E_PACKS:-all}
[[ "$packs" == all ]] && packs=$all_packs
IFS=',' read -r -a selected_packs <<<"$packs"
for pack in "${selected_packs[@]}"; do
  [[ ",$all_packs," == *",$pack,"* ]] || { printf 'unknown stateful E2E pack: %s\n' "$pack" >&2; exit 2; }
done

database_url=${TEST_DATABASE_URL:-${STRATUM_TEST_POSTGRES_URL:-postgres://stratum:stratum@127.0.0.1:5432/stratum_e2e?sslmode=disable}}
[[ "$database_url" =~ ^postgres(ql)?://[^[:space:]]+@(127\.0\.0\.1|localhost|postgres)(:[0-9]+)?/[^/?]*(test|e2e)[^/?]*([?].*)?$ ]] || {
  printf 'unsafe E2E database target\n' >&2; exit 2;
}
database_name=${database_url%%\?*}
database_name=${database_name##*/}
[[ "$database_name" =~ ^[A-Za-z0-9_]+$ ]] || { printf 'unsafe E2E database name\n' >&2; exit 2; }

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-system-stateful.XXXXXX")
oauth_pid='' mcp_pid='' backend_pid='' frontend_pid='' infra_started=false infra_owned=false
shared_milvus_container='' shared_milvus_started=false
cleanup() {
  local status=$?
  [[ -z "$frontend_pid" ]] || { kill -- "-$frontend_pid" 2>/dev/null || true; wait "$frontend_pid" 2>/dev/null || true; }
  [[ -z "$backend_pid" ]] || { kill -- "-$backend_pid" 2>/dev/null || true; wait "$backend_pid" 2>/dev/null || true; }
  [[ -z "$mcp_pid" ]] || { kill -- "-$mcp_pid" 2>/dev/null || true; wait "$mcp_pid" 2>/dev/null || true; }
  [[ -z "$oauth_pid" ]] || { kill -- "-$oauth_pid" 2>/dev/null || true; wait "$oauth_pid" 2>/dev/null || true; }
  if [[ "$shared_milvus_started" == true ]]; then
    export STATEFUL_E2E_SHARED_MILVUS_CONTAINER=$shared_milvus_container
    bash -c "${STATEFUL_E2E_SHARED_MILVUS_STOP_COMMAND:-docker stop \"\$STATEFUL_E2E_SHARED_MILVUS_CONTAINER\"}" \
      >/dev/null 2>&1 || status=1
  fi
  if [[ "$infra_started" == true && "$infra_owned" == true ]]; then
    bash -c "${STATEFUL_E2E_INFRA_DOWN_COMMAND:-make -C '$repo_dir' infra-down}" >/dev/null 2>&1 || status=1
  fi
  rm -rf "$work_dir"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

common_git_dir=$(cd "$repo_dir" && git rev-parse --path-format=absolute --git-common-dir)
env_file=${STATEFUL_E2E_ENV_FILE:-$(dirname "$common_git_dir")/.env}
if [[ -r "$env_file" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "$env_file"
  set +a
fi
export TEST_DATABASE_URL=$database_url STRATUM_TEST_POSTGRES_URL=$database_url POSTGRES_URL=$database_url
export QWEN_BASE_URL=http://127.0.0.1:19091/v1

oauth_suffix="$(date +%s)-$$"
oauth_client_secret=$(openssl rand -hex 32)
oauth_github_id=$(($(date +%s) * 100000 + $$ % 100000))
export STRATUM_E2E_MODE=true
export GITHUB_CLIENT_ID=stratum-stateful-e2e GITHUB_CLIENT_SECRET=$oauth_client_secret
export GITHUB_CALLBACK_URL=http://127.0.0.1:18080/auth/github/callback
export GITHUB_AUTHORIZE_URL=http://127.0.0.1:19090/login/oauth/authorize
export GITHUB_TOKEN_URL=http://127.0.0.1:19090/login/oauth/access_token
export GITHUB_USER_URL=http://127.0.0.1:19090/user
export E2E_GITHUB_LISTEN_ADDRESS=127.0.0.1:19090 E2E_GITHUB_ID=$oauth_github_id
export E2E_GITHUB_LOGIN="stateful-oauth-$oauth_suffix" E2E_GITHUB_EMAIL="stateful-oauth-$oauth_suffix@example.test"

digest_command=${STATEFUL_E2E_DIGEST_COMMAND:-go run ./cmd/e2e-attestation digest --root .}
source_before=$(cd "$repo_dir" && bash -c "$digest_command")

base_infra_ready() {
  if [[ -n "${STATEFUL_E2E_BASE_INFRA_HEALTH_COMMAND:-}" ]]; then
    bash -c "$STATEFUL_E2E_BASE_INFRA_HEALTH_COMMAND"
    return
  fi
  local port
  for port in 5432 6379 4222; do
    timeout 1 bash -c "</dev/tcp/127.0.0.1/$port" 2>/dev/null || return 1
  done
}
milvus_ready() {
  if [[ -n "${STATEFUL_E2E_MILVUS_HEALTH_COMMAND:-}" ]]; then
    bash -c "$STATEFUL_E2E_MILVUS_HEALTH_COMMAND"
    return
  fi
  timeout 1 bash -c '</dev/tcp/127.0.0.1/19530' 2>/dev/null
}
find_shared_milvus() {
  if [[ -n "${STATEFUL_E2E_SHARED_MILVUS_FIND_COMMAND:-}" ]]; then
    bash -c "$STATEFUL_E2E_SHARED_MILVUS_FIND_COMMAND"
    return
  fi
  local projects project candidate found=''
  projects=$(docker ps --format '{{.Label "com.docker.compose.project"}} {{.Label "com.docker.compose.service"}}' | awk '
    $1 != "" && ($2 == "postgres" || $2 == "redis" || $2 == "nats") { seen[$1 ":" $2]=1; projects[$1]=1 }
    END { for (p in projects) if (seen[p ":postgres"] && seen[p ":redis"] && seen[p ":nats"]) print p }
  ')
  while IFS= read -r project; do
    [[ -n "$project" ]] || continue
    candidate=$(docker ps -a \
      --filter "label=com.docker.compose.project=$project" \
      --filter 'label=com.docker.compose.service=milvus' \
      --filter 'status=exited' --format '{{.ID}}')
    [[ -n "$candidate" && "$candidate" != *$'\n'* ]] || continue
    [[ -z "$found" ]] || return 1
    found=$candidate
  done <<<"$projects"
  [[ -n "$found" ]] || return 1
  printf '%s\n' "$found"
}
if [[ -n "${STATEFUL_E2E_INFRA_UP_COMMAND:-}" ]]; then
  infra_started=true
  bash -c "$STATEFUL_E2E_INFRA_UP_COMMAND"
elif ! base_infra_ready; then
  infra_owned=true
  infra_started=true
  make -C "$repo_dir" infra-up infra-wait
elif ! milvus_ready; then
  shared_milvus_container=$(find_shared_milvus) || {
    printf 'shared core infrastructure is running but a unique stopped Milvus container was not found\n' >&2
    exit 1
  }
  [[ "$shared_milvus_container" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]+$ ]] || {
    printf 'unsafe shared Milvus container identifier\n' >&2
    exit 1
  }
  export STATEFUL_E2E_SHARED_MILVUS_CONTAINER=$shared_milvus_container
  bash -c "${STATEFUL_E2E_SHARED_MILVUS_START_COMMAND:-docker start \"\$STATEFUL_E2E_SHARED_MILVUS_CONTAINER\"}"
  shared_milvus_started=true
  for _ in {1..60}; do
    milvus_ready && break
    sleep 1
  done
  milvus_ready || { printf 'shared Milvus failed health check\n' >&2; exit 1; }
fi
database_prepare_command=${STATEFUL_E2E_DATABASE_PREPARE_COMMAND:-\
"cd '$repo_dir/web' && node --input-type=module -e 'import pg from \"pg\"; const target=new URL(process.env.TEST_DATABASE_URL); const name=target.pathname.slice(1); target.pathname=\"/postgres\"; const client=new pg.Client({connectionString:target.toString()}); await client.connect(); const found=await client.query(\"SELECT 1 FROM pg_database WHERE datname = \$1\",[name]); if (found.rowCount === 0) await client.query(\"CREATE DATABASE \\\"\"+name+\"\\\"\"); await client.end();'"}
bash -c "$database_prepare_command"
migration_command=${STATEFUL_E2E_MIGRATION_COMMAND:-\
"cd '$repo_dir' && go run ./cmd/migrate-public --sql-dir '$repo_dir/pkg/migration/sql'"}
bash -c "$migration_command"

oauth_command=${STATEFUL_E2E_OAUTH_COMMAND:-"cd '$repo_dir' && go run ./cmd/e2e-github-oauth"}
setsid bash -c "exec bash -c \"\$1\"" _ "$oauth_command" >"$work_dir/oauth.log" 2>&1 & oauth_pid=$!
mcp_command=${STATEFUL_E2E_MCP_COMMAND:-"cd '$repo_dir' && go run ./cmd/e2e-mcp-server"}
setsid bash -c "exec bash -c \"\$1\"" _ "$mcp_command" >"$work_dir/mcp.log" 2>&1 & mcp_pid=$!
backend_command=${STATEFUL_E2E_BACKEND_COMMAND:-"cd '$repo_dir' && FRONTEND_URL=http://127.0.0.1:15173 OPIK_URL=http://127.0.0.1:19091/opik PORT=18080 SECURE_COOKIES=false go run ./cmd/server"}
setsid bash -c "exec bash -c \"\$1\"" _ "$backend_command" >"$work_dir/backend.log" 2>&1 & backend_pid=$!
frontend_command=${STATEFUL_E2E_FRONTEND_COMMAND:-"cd '$repo_dir/web' && VITE_API_BASE_URL=http://127.0.0.1:18080 npm run dev -- --host 127.0.0.1 --port 15173"}
setsid bash -c "exec bash -c \"\$1\"" _ "$frontend_command" >"$work_dir/frontend.log" 2>&1 & frontend_pid=$!

poll() {
  local label=$1 command=$2 attempts=${STATEFUL_E2E_HEALTH_ATTEMPTS:-120}
  for ((index=1; index<=attempts; index++)); do
    bash -c "$command" >/dev/null 2>&1 && return 0
    sleep 1
  done
  printf '%s failed health check\n' "$label" >&2
  return 1
}
poll 'oauth provider' "${STATEFUL_E2E_OAUTH_HEALTH_COMMAND:-curl -fsS http://127.0.0.1:19090/health}"
poll 'MCP server' "${STATEFUL_E2E_MCP_HEALTH_COMMAND:-curl -fsS http://127.0.0.1:19091/health}"
poll backend "${STATEFUL_E2E_BACKEND_HEALTH_COMMAND:-curl -fsS http://127.0.0.1:18080/health}"
poll frontend "${STATEFUL_E2E_FRONTEND_HEALTH_COMMAND:-curl -fsS http://127.0.0.1:15173/}"
kill -0 "$oauth_pid" 2>/dev/null || { printf 'oauth provider exited before browser execution\n' >&2; exit 1; }
kill -0 "$mcp_pid" 2>/dev/null || { printf 'MCP server exited before browser execution\n' >&2; exit 1; }
kill -0 "$backend_pid" 2>/dev/null || { printf 'backend exited before browser execution\n' >&2; exit 1; }
kill -0 "$frontend_pid" 2>/dev/null || { printf 'frontend exited before browser execution\n' >&2; exit 1; }

results_path=${STATEFUL_E2E_RESULTS_PATH:-$work_dir/safe-results.json}
export STATEFUL_E2E_MODE=$mode STATEFUL_E2E_DURATION_SEC=$duration STATEFUL_E2E_PACKS=$packs
export E2E_API_URL=http://127.0.0.1:18080 E2E_WEB_URL=http://127.0.0.1:15173 STATEFUL_E2E_RESULTS_PATH=$results_path
playwright_command=${STATEFUL_E2E_PLAYWRIGHT_COMMAND:-"cd '$repo_dir/web' && npx playwright test --config playwright.stateful.config.ts"}
bash -c "$playwright_command"

[[ -s "$results_path" ]] || { printf 'stateful E2E safe results are missing\n' >&2; exit 1; }
jq -e '
  .status == "passed" and
  (.cleanup.passed == true) and
  (.unverified_capabilities | length == 0) and
  (all(.packs[]; .status == "passed")) and
  (all(.capabilities[]; .status == "passed"))
' "$results_path" >/dev/null || { printf 'stateful E2E results contain failed or skipped coverage\n' >&2; exit 1; }

source_after=$(cd "$repo_dir" && bash -c "$digest_command")
[[ "$source_before" == "$source_after" ]] || { printf 'covered source changed during stateful E2E execution\n' >&2; exit 1; }

attestation_command=${STATEFUL_E2E_ATTESTATION_COMMAND:-"go run ./cmd/e2e-attestation generate --input '$results_path' --output-dir test/e2e/attestations"}
(cd "$repo_dir" && bash -c "$attestation_command")
