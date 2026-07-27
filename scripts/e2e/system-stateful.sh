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

database_url=${TEST_DATABASE_URL:-${STRATUM_TEST_POSTGRES_URL:-}}
[[ "$database_url" =~ ^postgres(ql)?://[^[:space:]]+@(127\.0\.0\.1|localhost|postgres)(:[0-9]+)?/[^/?]*(test|e2e)[^/?]*([?].*)?$ ]] || {
  printf 'unsafe E2E database target\n' >&2; exit 2;
}

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-system-stateful.XXXXXX")
backend_pid='' frontend_pid='' infra_started=false
cleanup() {
  local status=$?
  [[ -z "$frontend_pid" ]] || { kill -- "-$frontend_pid" 2>/dev/null || true; wait "$frontend_pid" 2>/dev/null || true; }
  [[ -z "$backend_pid" ]] || { kill -- "-$backend_pid" 2>/dev/null || true; wait "$backend_pid" 2>/dev/null || true; }
  if [[ "$infra_started" == true ]]; then
    bash -c "${STATEFUL_E2E_INFRA_DOWN_COMMAND:-make -C '$repo_dir' infra-down}" >/dev/null 2>&1 || status=1
  fi
  rm -rf "$work_dir"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

digest_command=${STATEFUL_E2E_DIGEST_COMMAND:-go run ./cmd/e2e-attestation digest --root .}
source_before=$(cd "$repo_dir" && bash -c "$digest_command")

bash -c "${STATEFUL_E2E_INFRA_UP_COMMAND:-make -C '$repo_dir' infra-up infra-wait}"
infra_started=true

backend_command=${STATEFUL_E2E_BACKEND_COMMAND:-"cd '$repo_dir' && FRONTEND_URL=http://127.0.0.1:15173 PORT=18080 SECURE_COOKIES=false go run ./cmd/server"}
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
poll backend "${STATEFUL_E2E_BACKEND_HEALTH_COMMAND:-curl -fsS http://127.0.0.1:18080/health}"
poll frontend "${STATEFUL_E2E_FRONTEND_HEALTH_COMMAND:-curl -fsS http://127.0.0.1:15173/}"
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
