#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
runner=$repo_dir/scripts/e2e/system-stateful.sh
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-stateful-behavior.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT

# The full fake-runner scenarios below are intentionally contract-level: production JSON
# validation and registry lifecycle remain active while external services are replaced.
grep -Fq 'prepare-registry' "$runner" || { printf 'missing safe registry preparation\n' >&2; exit 1; }
grep -Fq 'database-url' "$runner" || { printf 'missing database URL command\n' >&2; exit 1; }
grep -Fq 'STATEFUL_E2E_PORT_ALLOCATION_ATTEMPTS' "$runner" || { printf 'missing finite allocation retry\n' >&2; exit 1; }

target=$test_dir/target; mkdir "$target"
ln -s "$target" "$test_dir/registry-link"
set +e
output=$(env STATEFUL_E2E_REGISTRY_ROOT="$test_dir/registry-link" \
  STATEFUL_E2E_SCOPE_COMMAND='go run ./cmd/e2e-run-scope' \
  STATEFUL_E2E_DIGEST_COMMAND='printf digest' bash "$runner" short 2>&1)
status=$?
set -e
((status != 0)) || { printf 'symlink registry root was accepted\n' >&2; exit 1; }
[[ "$output" == *registry* ]] || { printf 'symlink rejection did not identify registry failure\n' >&2; exit 1; }

STRATUM_WORKTREE_ROOT=$repo_dir go build -o "$test_dir/e2e-run-scope" ./cmd/e2e-run-scope
cat >"$test_dir/scope-command" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
command=$1; shift
evidence_root=${ATTEMPT_ROOT:-$FAKE_ROOT}
case "$command" in
  prepare-registry|mark-infrastructure-owned|validate)
    exec "$FAKE_ROOT/e2e-run-scope" "$command" "$@" ;;
  release)
    printf 'release\n' >>"$evidence_root/release-attempts"
    [[ ! -e "$FAKE_ROOT/fail-release" ]] || exit 10
    exec "$FAKE_ROOT/e2e-run-scope" "$command" "$@" ;;
  confirm-infrastructure-stopped)
    printf 'confirm\n' >>"$evidence_root/confirm-attempts"
    [[ ! -e "$FAKE_ROOT/fail-confirm" ]] || exit 11
    exec "$FAKE_ROOT/e2e-run-scope" "$command" "$@" ;;
  allocate)
    output=$("$FAKE_ROOT/e2e-run-scope" allocate "$@")
    printf '%s\n' "$output" >>"$FAKE_ROOT/allocations.jsonl"
    printf '%s\n' "$output" ;;
  reap) printf '[]\n' ;;
  create-database)
    while (($#)); do [[ "$1" == --scope ]] && { scope=$2; break; }; shift; done
    database=$(jq -er .database_name "$scope"); : >"$FAKE_ROOT/db-$database" ;;
  drop-database)
    while (($#)); do [[ "$1" == --scope ]] && { scope=$2; break; }; shift; done
    database=$(jq -er .database_name "$scope")
    printf 'drop\n' >>"$evidence_root/drop-attempts"
    [[ ! -e "$FAKE_ROOT/fail-drop" ]] || exit 9
    [[ -e "$FAKE_ROOT/db-$database" ]] || exit 1
    find "$FAKE_ROOT" -maxdepth 1 -name 'db-*' | wc -l >>"$FAKE_ROOT/databases-before-drop"
    rm "$FAKE_ROOT/db-$database"; printf '%s\n' "$database" >>"$FAKE_ROOT/dropped" ;;
  database-url)
    [[ ! -e "$FAKE_ROOT/fail-database-url" ]] || exit 8
    while (($#)); do [[ "$1" == --database-name ]] && { database=$2; break; }; shift; done
    printf 'postgres://u:p@127.0.0.1:5432/%s?sslmode=disable\n' "$database" ;;
  *) exit 2 ;;
esac
EOF
chmod +x "$test_dir/scope-command"

cat >"$test_dir/playwright-overlap" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
: >"$FAKE_ROOT/entered-$E2E_RUN_INSTANCE_ID"
for _ in $(seq 1 100); do
  [[ $(find "$FAKE_ROOT" -maxdepth 1 -name 'entered-*' | wc -l) -ge 2 ]] && break
  sleep 0.05
done
[[ $(find "$FAKE_ROOT" -maxdepth 1 -name 'entered-*' | wc -l) -ge 2 ]]
printf '%s\n' "$E2E_RUN_INSTANCE_ID" >>"$FAKE_ROOT/overlapped"
printf '{"status":"passed","cleanup":{"passed":true},"unverified_capabilities":[],"packs":[],"capabilities":[]}\n' >"$STATEFUL_E2E_RESULTS_PATH"
EOF
cat >"$test_dir/playwright-pass" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
: >"$FAKE_ROOT/passed-$E2E_RUN_INSTANCE_ID"
printf '{"status":"passed","cleanup":{"passed":true},"unverified_capabilities":[],"packs":[],"capabilities":[]}\n' >"$STATEFUL_E2E_RESULTS_PATH"
EOF
cat >"$test_dir/health-fail-once" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$E2E_RUN_INSTANCE_ID" >>"$FAKE_ROOT/health-runs"
if [[ ! -e "$FAKE_ROOT/health-failed" ]]; then : >"$FAKE_ROOT/health-failed"; exit 1; fi
EOF
cat >"$test_dir/migration-fail-overlap" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
for _ in $(seq 1 100); do
  [[ -n $(find "$FAKE_ROOT" -maxdepth 1 -name 'passed-*' -print -quit) ]] && exit 1
  sleep 0.05
done
printf 'peer did not reach Playwright before migration failure\n' >&2
exit 2
EOF
cat >"$test_dir/browser-fail-seven" <<'EOF'
#!/usr/bin/env bash
exit 7
EOF
cat >"$test_dir/ignore-term" <<'EOF'
#!/usr/bin/env bash
trap '' TERM
while :; do sleep 1; done
EOF
cat >"$test_dir/term-descendant" <<'EOF'
#!/usr/bin/env bash
bash -c 'trap "" TERM; while :; do sleep 1; done' &
printf '%s\n' "$!" >>"$FAKE_ROOT/term-descendants"
wait
EOF
chmod +x "$test_dir/playwright-overlap" "$test_dir/playwright-pass" "$test_dir/health-fail-once" \
  "$test_dir/migration-fail-overlap" "$test_dir/browser-fail-seven" "$test_dir/ignore-term" \
  "$test_dir/term-descendant"

fake_env=(
  TEST_DATABASE_URL=postgres://u:p@127.0.0.1:5432/e2e
  STATEFUL_E2E_REGISTRY_ROOT="$test_dir/registry"
  STATEFUL_E2E_SCOPE_COMMAND="$test_dir/scope-command"
  STATEFUL_E2E_INFRA_HEALTH_COMMAND="test -e '$test_dir/infra-ready'"
  STATEFUL_E2E_INFRA_UP_COMMAND="touch '$test_dir/infra-ready'; echo up >>'$test_dir/infra-events'"
  STATEFUL_E2E_INFRA_WAIT_COMMAND=true
  STATEFUL_E2E_INFRA_DOWN_COMMAND="rm -f '$test_dir/infra-ready'; echo down >>'$test_dir/infra-events'"
  STATEFUL_E2E_MIGRATION_COMMAND=true
  STATEFUL_E2E_OAUTH_COMMAND='while :; do sleep 1; done'
  STATEFUL_E2E_MCP_COMMAND='while :; do sleep 1; done'
  STATEFUL_E2E_PLATFORM_MCP_COMMAND='while :; do sleep 1; done'
  STATEFUL_E2E_BACKEND_COMMAND='while :; do sleep 1; done'
  STATEFUL_E2E_FRONTEND_COMMAND='while :; do sleep 1; done'
  STATEFUL_E2E_OAUTH_HEALTH_COMMAND=true
  STATEFUL_E2E_MCP_HEALTH_COMMAND=true
  STATEFUL_E2E_PLATFORM_MCP_HEALTH_COMMAND=true
  STATEFUL_E2E_INTERNAL_API_HEALTH_COMMAND=true
  STATEFUL_E2E_BACKEND_HEALTH_COMMAND=true
  STATEFUL_E2E_FRONTEND_HEALTH_COMMAND=true
  STATEFUL_E2E_DIGEST_COMMAND='printf digest'
  STATEFUL_E2E_ATTESTATION_COMMAND=true
  STATEFUL_E2E_HEALTH_ATTEMPTS=1
  FAKE_ROOT="$test_dir"
)

env "${fake_env[@]}" STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-overlap" bash "$runner" short >"$test_dir/a.log" 2>&1 & a_pid=$!
env "${fake_env[@]}" STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-overlap" bash "$runner" short >"$test_dir/b.log" 2>&1 & b_pid=$!
wait "$a_pid" || { cat "$test_dir/a.log" >&2; exit 1; }
wait "$b_pid" || { cat "$test_dir/b.log" >&2; exit 1; }
[[ $(wc -l <"$test_dir/overlapped") -eq 2 ]]
jq -s -e 'length == 2 and (.[0].run_id != .[1].run_id) and (.[0].database_name != .[1].database_name) and (([.[].ports[]]|unique|length)==12)' "$test_dir/allocations.jsonl" >/dev/null
[[ $(grep -c '^up$' "$test_dir/infra-events") -eq 1 && $(grep -c '^down$' "$test_dir/infra-events") -eq 1 ]]
grep -qx '2' "$test_dir/databases-before-drop"
[[ $(sort -u "$test_dir/dropped" | wc -l) -eq 2 ]]
[[ -z $(find "$test_dir/registry/runs" -name '*.json' -print -quit) ]]

rm -f "$test_dir"/entered-* "$test_dir"/passed-* "$test_dir/allocations.jsonl" "$test_dir/dropped" \
  "$test_dir/databases-before-drop" "$test_dir/infra-events"
env "${fake_env[@]}" STATEFUL_E2E_MIGRATION_COMMAND="$test_dir/migration-fail-overlap" \
  STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-pass" bash "$runner" short >"$test_dir/fail.log" 2>&1 & fail_pid=$!
env "${fake_env[@]}" STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-pass" bash "$runner" short >"$test_dir/pass.log" 2>&1 & pass_pid=$!
set +e; wait "$fail_pid"; fail_status=$?; set -e
((fail_status != 0))
wait "$pass_pid" || { cat "$test_dir/pass.log" >&2; exit 1; }
[[ -n $(find "$test_dir" -maxdepth 1 -name 'passed-*' -print -quit) ]]
[[ -z $(find "$test_dir/registry/runs" -name '*.json' -print -quit) ]]
[[ -z $(find "$test_dir" -maxdepth 1 -name 'db-*' -print -quit) ]]

rm -f "$test_dir"/passed-* "$test_dir/allocations.jsonl" "$test_dir/dropped" "$test_dir/databases-before-drop" \
  "$test_dir/infra-events" "$test_dir/health-failed" "$test_dir/health-runs"
env "${fake_env[@]}" STATEFUL_E2E_OAUTH_HEALTH_COMMAND="$test_dir/health-fail-once" \
  STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-pass" STATEFUL_E2E_PORT_ALLOCATION_ATTEMPTS=2 \
  bash "$runner" short >"$test_dir/retry.log" 2>&1 || { cat "$test_dir/retry.log" >&2; exit 1; }
jq -s -e 'length == 2 and (.[0].run_id != .[1].run_id) and (.[0].database_name != .[1].database_name) and (([.[].ports[]]|unique|length)==12)' "$test_dir/allocations.jsonl" >/dev/null
[[ $(sort -u "$test_dir/dropped" | wc -l) -eq 2 ]]
[[ $(sort -u "$test_dir/health-runs" | wc -l) -eq 2 ]]
[[ -z $(find "$test_dir/registry/runs" -name '*.json' -print -quit) ]]
[[ -z $(find "$test_dir" -maxdepth 1 -name 'db-*' -print -quit) ]]

rm -f "$test_dir/health-failed" "$test_dir/health-runs" "$test_dir/allocations.jsonl" "$test_dir/dropped" \
  "$test_dir/databases-before-drop" "$test_dir/infra-events" "$test_dir"/passed-*
set +e
env "${fake_env[@]}" STATEFUL_E2E_OAUTH_HEALTH_COMMAND=false STATEFUL_E2E_PORT_ALLOCATION_ATTEMPTS=2 \
  STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-pass" bash "$runner" short >"$test_dir/exhausted.log" 2>&1
status=$?
set -e
((status != 0))
[[ $(wc -l <"$test_dir/allocations.jsonl") -eq 2 ]]
[[ -z $(find "$test_dir/registry/runs" -name '*.json' -print -quit) ]]
[[ -z $(find "$test_dir" -maxdepth 1 -name 'db-*' -print -quit) ]]

rm -rf "$test_dir/primary-registry"; rm -f "$test_dir/infra-ready"; : >"$test_dir/fail-drop"
set +e
env "${fake_env[@]}" STATEFUL_E2E_REGISTRY_ROOT="$test_dir/primary-registry" \
  STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/browser-fail-seven" bash "$runner" short >"$test_dir/primary.log" 2>&1
status=$?
set -e
rm -f "$test_dir/fail-drop"
[[ "$status" -eq 7 ]] || { printf 'cleanup replaced primary exit status: %d\n' "$status" >&2; exit 1; }
grep -Eq 'residual database: stratum_e2e_[0-9]{8}t[0-9]{6}z_[a-f0-9]{16}; lease: .*/runs/[0-9]{8}t[0-9]{6}z-[a-f0-9]{16}\.json' \
  "$test_dir/primary.log" || { printf 'drop failure omitted exact residual identifiers\n' >&2; exit 1; }

rm -rf "$test_dir/url-registry"; rm -f "$test_dir/infra-ready" "$test_dir/dropped"; : >"$test_dir/fail-database-url"
set +e
env "${fake_env[@]}" STATEFUL_E2E_REGISTRY_ROOT="$test_dir/url-registry" \
  STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-pass" bash "$runner" short >"$test_dir/url.log" 2>&1
status=$?
set -e
rm -f "$test_dir/fail-database-url"
((status != 0))
[[ $(wc -l <"$test_dir/dropped") -eq 1 ]] || { printf 'database-url failure leaked database ownership\n' >&2; exit 1; }
[[ -z $(find "$test_dir/url-registry/runs" -name '*.json' -print -quit) ]]

rm -rf "$test_dir/wait-registry"; rm -f "$test_dir/infra-resource" "$test_dir/infra-events"
set +e
env "${fake_env[@]}" STATEFUL_E2E_REGISTRY_ROOT="$test_dir/wait-registry" STATEFUL_E2E_INFRA_HEALTH_COMMAND=false \
  STATEFUL_E2E_INFRA_UP_COMMAND="touch '$test_dir/infra-resource'; echo up >>'$test_dir/infra-events'" \
  STATEFUL_E2E_INFRA_WAIT_COMMAND=false \
  STATEFUL_E2E_INFRA_DOWN_COMMAND="rm -f '$test_dir/infra-resource'; echo down >>'$test_dir/infra-events'" \
  bash "$runner" short >"$test_dir/wait.log" 2>&1
status=$?
set -e
((status != 0))
[[ ! -e "$test_dir/infra-resource" ]] || { printf 'readiness failure leaked started infrastructure\n' >&2; exit 1; }
[[ $(grep -c '^down$' "$test_dir/infra-events") -eq 1 ]]

rm -rf "$test_dir/term-registry"; rm -f "$test_dir/infra-ready"
set +e
timeout 12 env "${fake_env[@]}" STATEFUL_E2E_REGISTRY_ROOT="$test_dir/term-registry" \
  STATEFUL_E2E_OAUTH_COMMAND="$test_dir/ignore-term" STATEFUL_E2E_MCP_COMMAND="$test_dir/ignore-term" \
  STATEFUL_E2E_PLATFORM_MCP_COMMAND="$test_dir/ignore-term" STATEFUL_E2E_BACKEND_COMMAND="$test_dir/ignore-term" \
  STATEFUL_E2E_FRONTEND_COMMAND="$test_dir/ignore-term" STATEFUL_E2E_CHILD_TERM_TIMEOUT_SEC=1 \
  STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-pass" bash "$runner" short >"$test_dir/term.log" 2>&1
status=$?
set -e
[[ "$status" -ne 124 ]] || { printf 'TERM-resistant child blocked owned cleanup\n' >&2; exit 1; }
[[ -z $(find "$test_dir/term-registry/runs" -name '*.json' -print -quit) ]]

rm -rf "$test_dir/descendant-registry"; rm -f "$test_dir/infra-ready" "$test_dir/term-descendants"
set +e
timeout 12 env "${fake_env[@]}" STATEFUL_E2E_REGISTRY_ROOT="$test_dir/descendant-registry" \
  STATEFUL_E2E_OAUTH_COMMAND="$test_dir/term-descendant" STATEFUL_E2E_MCP_COMMAND="$test_dir/term-descendant" \
  STATEFUL_E2E_PLATFORM_MCP_COMMAND="$test_dir/term-descendant" \
  STATEFUL_E2E_BACKEND_COMMAND="$test_dir/term-descendant" \
  STATEFUL_E2E_FRONTEND_COMMAND="$test_dir/term-descendant" STATEFUL_E2E_CHILD_TERM_TIMEOUT_SEC=1 \
  STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-pass" \
  bash "$runner" short >"$test_dir/descendant.log" 2>&1
status=$?
set -e
[[ "$status" -ne 124 ]] || { printf 'TERM-resistant descendant blocked owned cleanup\n' >&2; exit 1; }
while read -r descendant; do
  ! kill -0 "$descendant" 2>/dev/null || { printf 'TERM-resistant descendant survived cleanup\n' >&2; exit 1; }
done <"$test_dir/term-descendants"
[[ -z $(find "$test_dir/descendant-registry/runs" -name '*.json' -print -quit) ]]

for failure in drop release confirm; do
  registry="$test_dir/$failure-failure-registry"
  attempts="$test_dir/$failure-failure-attempts"
  rm -rf "$registry"
  mkdir "$attempts"
  rm -f "$test_dir/infra-ready"
  : >"$test_dir/fail-$failure"
  set +e
  env "${fake_env[@]}" STATEFUL_E2E_REGISTRY_ROOT="$registry" \
    ATTEMPT_ROOT="$attempts" \
    STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-pass" bash "$runner" short \
    >"$test_dir/$failure-failure.log" 2>&1
  status=$?
  set -e
  rm -f "$test_dir/fail-$failure"
  ((status != 0))
  case "$failure" in
    drop)
      [[ $(wc -l <"$attempts/drop-attempts") -eq 2 ]]
      [[ ! -e "$attempts/release-attempts" ]] ;;
    release)
      [[ $(wc -l <"$attempts/release-attempts") -eq 2 ]]
      [[ ! -e "$attempts/confirm-attempts" ]] ;;
    confirm)
      [[ $(wc -l <"$attempts/release-attempts") -eq 1 ]]
      [[ $(wc -l <"$attempts/confirm-attempts") -eq 2 ]] ;;
  esac
done

printf 'stateful runner behavior contract passed\n'
