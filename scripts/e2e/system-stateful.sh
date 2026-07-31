#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
mode=${1:-short}; profile=${STATEFUL_E2E_PROFILE:-}; duration=${STATEFUL_E2E_DURATION_SEC:-}
case "$mode" in
  short) [[ -z "$profile" ]] || { printf 'short mode cannot declare an acceptance profile\n' >&2; exit 2; }; duration=${duration:-600} ;;
  soak) profile=${profile:-test}; [[ "$profile" == test || "$profile" == release ]] || { printf 'unsupported stateful E2E profile: %s\n' "$profile" >&2; exit 2; }; duration=${duration:-$([[ "$profile" == release ]] && printf 3600 || printf 600)} ;;
  *) printf 'unsupported stateful E2E mode: %s\n' "$mode" >&2; exit 2 ;;
esac
[[ "$duration" =~ ^[0-9]+$ ]] && ((duration >= 600 && duration <= 14400)) || { printf 'STATEFUL_E2E_DURATION_SEC must be between 600 and 14400\n' >&2; exit 2; }
[[ "$profile" != release || "$duration" -ge 3600 ]] || { printf 'release profile requires at least 3600 seconds\n' >&2; exit 2; }

all_packs='dashboard,iam,workflow,agent,skill,mcp,agent-skill-mcp,knowledge,memory,evaluation,agent-context,evaluation-promotion,llm-admin'
packs=${STATEFUL_E2E_PACKS:-all}; [[ "$packs" == all ]] && packs=$all_packs
IFS=',' read -r -a selected_packs <<<"$packs"
for pack in "${selected_packs[@]}"; do [[ ",$all_packs," == *",$pack,"* ]] || { printf 'unknown stateful E2E pack: %s\n' "$pack" >&2; exit 2; }; done

common_git_dir=$(cd "$repo_dir" && git rev-parse --path-format=absolute --git-common-dir)
env_file=${STATEFUL_E2E_ENV_FILE:-$(dirname "$common_git_dir")/.env}
if [[ -r "$env_file" ]]; then set -a; source "$env_file"; set +a; fi
base_dsn=${TEST_DATABASE_URL:-${STRATUM_TEST_POSTGRES_URL:-postgres://stratum:stratum@127.0.0.1:5432/stratum_e2e?sslmode=disable}}
registry_root=${STATEFUL_E2E_REGISTRY_ROOT:-${TMPDIR:-/tmp}/stratum-stateful-e2e}
mkdir -p "$registry_root"; chmod 700 "$registry_root"
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-stateful-run.XXXXXX")
scope_file=$work_dir/scope.json; results_path=${STATEFUL_E2E_RESULTS_PATH:-$work_dir/safe-results.json}
scope_command=${STATEFUL_E2E_SCOPE_COMMAND:-"go run ./cmd/e2e-run-scope"}
oauth_pid= mcp_pid= backend_pid= frontend_pid=; lease_registered=false; database_created=false
database_dropped=false; lease_removed=false; cleanup_done=false

stop_process() { local pid=${1:-}; [[ -z "$pid" ]] || { kill -- "-$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; }; }
cleanup_owned() {
  [[ "$cleanup_done" == true ]] && return 0
  local status=0 release_json=$work_dir/release.json
  stop_process "$frontend_pid"; stop_process "$backend_pid"; stop_process "$mcp_pid"; stop_process "$oauth_pid"
  if [[ "$database_created" == true ]]; then
    (cd "$repo_dir" && bash -c "$scope_command drop-database --scope '$scope_file' --base-dsn-env TEST_DATABASE_URL") || status=1
    [[ "$status" -ne 0 ]] || database_dropped=true
  else database_dropped=true; fi
  if [[ "$lease_registered" == true ]]; then
    exec 9>"$registry_root/registry.lock"; flock 9
    (cd "$repo_dir" && bash -c "$scope_command release --scope '$scope_file' --registry '$registry_root'") >"$release_json" || status=1
    flock -u 9
    [[ "$status" -ne 0 ]] || lease_removed=true
  else lease_removed=true; fi
  cleanup_done=true
  return "$status"
}
on_exit() { local status=$?; cleanup_owned || status=1; rm -rf "$work_dir"; exit "$status"; }
trap on_exit EXIT; trap 'exit 130' INT; trap 'exit 143' TERM

exec 9>"$registry_root/registry.lock"; flock 9
(cd "$repo_dir" && bash -c "$scope_command allocate --repository '$repo_dir' --registry '$registry_root'") >"$scope_file"
lease_registered=true
flock -u 9
jq -e '.schema_version == 1 and (.run_id|type=="string") and (.database_name|type=="string")' "$scope_file" >/dev/null
run_id=$(jq -er .run_id "$scope_file"); database_name=$(jq -er .database_name "$scope_file")
frontend_port=$(jq -er .ports.frontend "$scope_file"); backend_port=$(jq -er .ports.backend "$scope_file")
oauth_port=$(jq -er .ports.oauth "$scope_file"); fixture_port=$(jq -er .ports.fixture "$scope_file")
export E2E_API_URL="http://127.0.0.1:$backend_port" E2E_WEB_URL="http://127.0.0.1:$frontend_port"
export E2E_FIXTURE_URL="http://127.0.0.1:$fixture_port" E2E_RUN_INSTANCE_ID=$run_id
export GITHUB_CALLBACK_URL="$E2E_API_URL/auth/github/callback"
export GITHUB_AUTHORIZE_URL="http://127.0.0.1:$oauth_port/login/oauth/authorize"
export GITHUB_TOKEN_URL="http://127.0.0.1:$oauth_port/login/oauth/access_token" GITHUB_USER_URL="http://127.0.0.1:$oauth_port/user"
export QWEN_BASE_URL="$E2E_FIXTURE_URL/v1" E2E_GITHUB_LISTEN_ADDRESS="127.0.0.1:$oauth_port" E2E_MCP_LISTEN_ADDRESS="127.0.0.1:$fixture_port"
(cd "$repo_dir" && TEST_DATABASE_URL="$base_dsn" bash -c "$scope_command create-database --scope '$scope_file' --base-dsn-env TEST_DATABASE_URL")
TEST_DATABASE_URL=$(BASE_DSN="$base_dsn" DATABASE_NAME="$database_name" node --input-type=module -e 'const u=new URL(process.env.BASE_DSN); u.pathname="/"+process.env.DATABASE_NAME; process.stdout.write(u.toString())')
export STRATUM_TEST_POSTGRES_URL=$TEST_DATABASE_URL POSTGRES_URL=$TEST_DATABASE_URL; database_created=true

export TEST_DATABASE_URL STRATUM_TEST_POSTGRES_URL POSTGRES_URL E2E_API_URL E2E_WEB_URL E2E_FIXTURE_URL E2E_RUN_INSTANCE_ID
[[ -n "${JWT_PRIVATE_KEY_PEM:-}" ]] || { JWT_PRIVATE_KEY_PEM=$(openssl genrsa 2048 2>/dev/null); export JWT_PRIVATE_KEY_PEM; }
export STRATUM_E2E_MODE=true GITHUB_CLIENT_ID=stratum-stateful-e2e GITHUB_CLIENT_SECRET=${GITHUB_CLIENT_SECRET:-$(openssl rand -hex 32)}
export E2E_GITHUB_ID=${E2E_GITHUB_ID:-730001} E2E_GITHUB_LOGIN=${E2E_GITHUB_LOGIN:-stateful-oauth-$run_id} E2E_GITHUB_EMAIL=${E2E_GITHUB_EMAIL:-stateful-oauth-$run_id@example.test}

migration_command=${STATEFUL_E2E_MIGRATION_COMMAND:-"cd '$repo_dir' && go run ./cmd/migrate-public --sql-dir '$repo_dir/pkg/migration/sql'"}; bash -c "$migration_command"
start_child() { local variable=$1 command=$2 log=$3 pid; setsid bash -c "$command" >"$log" 2>&1 & pid=$!; printf -v "$variable" '%s' "$pid"; }
start_child oauth_pid "${STATEFUL_E2E_OAUTH_COMMAND:-cd '$repo_dir' && go run ./cmd/e2e-github-oauth}" "$work_dir/oauth.log"
start_child mcp_pid "${STATEFUL_E2E_MCP_COMMAND:-cd '$repo_dir' && go run ./cmd/e2e-mcp-server}" "$work_dir/mcp.log"
start_child backend_pid "${STATEFUL_E2E_BACKEND_COMMAND:-cd '$repo_dir' && FRONTEND_URL='$E2E_WEB_URL' OPIK_URL='$E2E_FIXTURE_URL/opik' PORT='$backend_port' SECURE_COOKIES=false go run ./cmd/server}" "$work_dir/backend.log"
start_child frontend_pid "${STATEFUL_E2E_FRONTEND_COMMAND:-cd '$repo_dir/web' && CI=1 VITE_API_BASE_URL='$E2E_API_URL' npm run dev -- --host 127.0.0.1 --port '$frontend_port' --strictPort}" "$work_dir/frontend.log"
poll() { local label=$1 command=$2; for _ in $(seq 1 "${STATEFUL_E2E_HEALTH_ATTEMPTS:-120}"); do bash -c "$command" >/dev/null 2>&1 && return 0; sleep 1; done; printf '%s failed health check\n' "$label" >&2; return 1; }
poll oauth "${STATEFUL_E2E_OAUTH_HEALTH_COMMAND:-curl -fsS -D - -H 'X-Stratum-E2E-Instance: $run_id' 'http://127.0.0.1:$oauth_port/health' | grep -Fi 'X-Stratum-E2E-Instance: $run_id'}"
poll MCP "${STATEFUL_E2E_MCP_HEALTH_COMMAND:-curl -fsS -D - -H 'X-Stratum-E2E-Instance: $run_id' '$E2E_FIXTURE_URL/health' | grep -Fi 'X-Stratum-E2E-Instance: $run_id'}"
poll backend "${STATEFUL_E2E_BACKEND_HEALTH_COMMAND:-curl -fsS '$E2E_API_URL/health'}"; poll frontend "${STATEFUL_E2E_FRONTEND_HEALTH_COMMAND:-curl -fsS '$E2E_WEB_URL/'}"

export STATEFUL_E2E_MODE=$mode STATEFUL_E2E_DURATION_SEC=$duration STATEFUL_E2E_PACKS=$packs STATEFUL_E2E_RESULTS_PATH=$results_path
[[ "$mode" == soak ]] && export STATEFUL_E2E_PROFILE=$profile || unset STATEFUL_E2E_PROFILE
bash -c "${STATEFUL_E2E_PLAYWRIGHT_COMMAND:-cd '$repo_dir/web' && npx playwright test --config playwright.stateful.config.ts}"
jq -e '.status == "passed" and .cleanup.passed and (.unverified_capabilities|length==0)' "$results_path" >/dev/null
cleanup_owned
[[ "$database_dropped" == true && "$lease_removed" == true ]] || { printf 'owned cleanup incomplete\n' >&2; exit 1; }
jq --arg run "$run_id" --arg db "$database_name" --argjson fp "$frontend_port" --argjson bp "$backend_port" --argjson op "$oauth_port" --argjson xp "$fixture_port" '. + {run_topology:{run_id:$run,host:"127.0.0.1",ports:{frontend:$fp,backend:$bp,oauth:$op,fixture:$xp},database_name:$db},owned_cleanup:{database_dropped:true,lease_removed:true}}' "$results_path" >"$work_dir/results-v2.json"
mv "$work_dir/results-v2.json" "$results_path"
attestation_command=${STATEFUL_E2E_ATTESTATION_COMMAND:-"cd '$repo_dir' && go run ./cmd/e2e-attestation generate --input '$results_path' --output-dir test/e2e/attestations"}; bash -c "$attestation_command"
trap - EXIT; rm -rf "$work_dir"
