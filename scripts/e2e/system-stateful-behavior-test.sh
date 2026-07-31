#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
runner=$repo_dir/scripts/e2e/system-stateful.sh
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-stateful-behavior.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT

fake_scope=$test_dir/fake-scope.sh
fake_playwright=$test_dir/fake-playwright.sh
fake_service=$test_dir/fake-service.sh

cat >"$fake_scope" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
events=$1; counter=$2; shift 2; command=$1; shift
scope_path=
while (($#)); do
  [[ "$1" == --scope ]] && scope_path=$2
  shift
done
case "$command" in
  allocate)
    count=$(($(cat "$counter" 2>/dev/null || printf 0) + 1)); printf '%s' "$count" >"$counter"
    suffix=$(printf '%016x' "$count"); base=$((30000 + count * 10))
    printf 'allocate:%s\n' "$count" >>"$events"
    jq -n --arg suffix "$suffix" --arg repo "$PWD" --argjson owner "$PPID" --argjson base "$base" '{
      schema_version:2,run_id:("20260731t000000z-"+$suffix),owner_pid:$owner,
      created_at:"2026-07-31T00:00:00Z",expires_at:"2026-08-01T00:00:00Z",repository:$repo,
      database_name:("stratum_e2e_20260731t000000z_"+$suffix),
      ports:{frontend:$base,backend:($base+1),oauth:($base+2),fixture:($base+3),platform_mcp:($base+4),internal_api:($base+5)},
      infrastructure:{lease_id:("20260731t000000z-"+$suffix),started_by_e2e:false}}'
    ;;
  create-database|drop-database)
    printf '%s:%s\n' "$command" "$(jq -r .run_id "$scope_path")" >>"$events"
    ;;
  mark-infrastructure-owned)
    printf 'mark:%s\n' "$(jq -r .run_id "$scope_path")" >>"$events"
    [[ "${FAIL_MARK:-false}" != true ]]
    ;;
  release)
    printf 'release:%s\n' "$(jq -r .run_id "$scope_path")" >>"$events"
    jq -n --arg owner "$(jq -r .run_id "$scope_path")" --argjson stop "${RELEASE_STOPS_INFRA:-true}" \
      '{stop_owned_infrastructure:$stop,ownership_run_id:$owner}'
    ;;
  confirm-infrastructure-stopped) printf 'confirm\n' >>"$events" ;;
  reap) printf '[]\n' ;;
  *) printf 'unexpected fake scope command: %s\n' "$command" >&2; exit 1 ;;
esac
EOF

cat >"$fake_playwright" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
jq -n --arg run "$E2E_RUN_INSTANCE_ID" '{status:"passed",cleanup:{passed:true},packs:[{id:"dashboard",status:"passed"}],capabilities:[],unverified_capabilities:[],run:$run}' >"$STATEFUL_E2E_RESULTS_PATH"
EOF

cat >"$fake_service" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ -z "${SERVICE_EVENT:-}" ]] || printf '%s:%s\n' "$SERVICE_EVENT" "$E2E_RUN_INSTANCE_ID" >>"$EVENTS_FILE"
exec sleep 60
EOF
chmod 700 "$fake_scope" "$fake_playwright" "$fake_service"

run_case() {
  local name=$1; shift
  local case_dir=$test_dir/$name events=$test_dir/$name/events counter=$test_dir/$name/counter infra=$test_dir/$name/infra
  mkdir -p "$case_dir"
  env \
    TEST_DATABASE_URL=postgres://stratum:stratum@127.0.0.1:5432/stratum_e2e?sslmode=disable \
    STATEFUL_E2E_REGISTRY_ROOT="$case_dir/registry" \
    STATEFUL_E2E_SCOPE_COMMAND="bash '$fake_scope' '$events' '$counter'" \
    STATEFUL_E2E_DIGEST_COMMAND="printf test-digest" \
    STATEFUL_E2E_INFRA_HEALTH_COMMAND="test -f '$infra'" \
    STATEFUL_E2E_INFRA_UP_COMMAND="touch '$infra'; printf 'infra-up\\n' >>'$events'" \
    STATEFUL_E2E_INFRA_DOWN_COMMAND="rm -f '$infra'; printf 'infra-down\\n' >>'$events'" \
    STATEFUL_E2E_MIGRATION_COMMAND=true \
    STATEFUL_E2E_OAUTH_COMMAND="EVENTS_FILE='$events' SERVICE_EVENT=oauth '$fake_service'" \
    STATEFUL_E2E_MCP_COMMAND="EVENTS_FILE='$events' SERVICE_EVENT=fixture '$fake_service'" \
    STATEFUL_E2E_PLATFORM_MCP_COMMAND="EVENTS_FILE='$events' SERVICE_EVENT=platform-mcp '$fake_service'" \
    STATEFUL_E2E_BACKEND_COMMAND="EVENTS_FILE='$events' SERVICE_EVENT=backend '$fake_service'" \
    STATEFUL_E2E_FRONTEND_COMMAND="EVENTS_FILE='$events' SERVICE_EVENT=frontend '$fake_service'" \
    STATEFUL_E2E_MCP_HEALTH_COMMAND=true STATEFUL_E2E_PLATFORM_MCP_HEALTH_COMMAND=true \
    STATEFUL_E2E_BACKEND_HEALTH_COMMAND=true STATEFUL_E2E_FRONTEND_HEALTH_COMMAND=true \
    STATEFUL_E2E_PLAYWRIGHT_COMMAND="bash '$fake_playwright'" \
    STATEFUL_E2E_ATTESTATION_COMMAND="printf 'attest\\n' >>'$events'" \
    STATEFUL_E2E_PACKS=dashboard STATEFUL_E2E_HEALTH_ATTEMPTS=1 \
    "$@" bash "$runner" short
}

run_case success STATEFUL_E2E_OAUTH_HEALTH_COMMAND=true
success_events=$test_dir/success/events
[[ $(grep -c '^infra-up$' "$success_events") == 1 && $(grep -c '^mark:' "$success_events") == 1 ]]
[[ $(grep -c '^release:' "$success_events") == 1 && $(grep -c '^infra-down$' "$success_events") == 1 ]]
[[ $(grep -c '^confirm$' "$success_events") == 1 && $(tail -n 1 "$success_events") == attest ]]

health_counter=$test_dir/retry-health
retry_health="count=\$(cat '$health_counter' 2>/dev/null || printf 0); count=\$((count+1)); printf '%s' \"\$count\" >'$health_counter'; ((count > 1))"
run_case retry STATEFUL_E2E_OAUTH_HEALTH_COMMAND="$retry_health"
retry_events=$test_dir/retry/events
[[ $(grep -c '^allocate:' "$retry_events") == 2 && $(grep -c '^release:' "$retry_events") == 2 ]]
[[ $(grep '^create-database:' "$retry_events" | sort -u | wc -l) == 2 ]]

set +e
run_case mark-failure FAIL_MARK=true RELEASE_STOPS_INFRA=false STATEFUL_E2E_OAUTH_HEALTH_COMMAND=true
mark_status=$?
set -e
((mark_status != 0))
[[ $(grep -c '^infra-up$' "$test_dir/mark-failure/events") == 1 ]]
[[ $(grep -c '^infra-down$' "$test_dir/mark-failure/events") == 1 ]]

printf 'stateful runner behavioral lifecycle contract passed\n'
