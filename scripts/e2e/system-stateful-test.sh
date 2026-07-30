#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
runner="$repo_dir/scripts/e2e/system-stateful.sh"
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-stateful-runner-test.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT

grep -Eq '^[[:space:]]+- "127\.0\.0\.1:9091:9091"$' "$repo_dir/docker-compose.yml" || {
  printf 'docker compose must publish the Milvus health endpoint used by infra-wait\n' >&2
  exit 1
}
grep -Fq -- '--noproxy stratum-platform-mcp' "$runner" || {
  printf 'Platform MCP health check must bypass external proxies\n' >&2
  exit 1
}
grep -Fq 'stateful_e2e_lock="$common_git_dir/stateful-e2e.lock"' "$runner" || {
  printf 'stateful E2E lock must be shared by all worktrees\n' >&2
  exit 1
}
grep -Fq 'flock "$stateful_e2e_lock_fd"' "$runner" || {
  printf 'stateful E2E runner must serialize shared ports and database access\n' >&2
  exit 1
}
grep -Fq 'CI=1 VITE_API_BASE_URL=http://127.0.0.1:18080 npm run dev' "$runner" || {
  printf 'stateful E2E frontend must disable Vite browser auto-open\n' >&2
  exit 1
}

expect_failure() {
  local expected=$1; shift
  set +e
  output=$("$@" 2>&1); status=$?
  set -e
  ((status != 0)) || { printf 'expected failure containing: %s\n' "$expected" >&2; exit 1; }
  [[ "$output" == *"$expected"* ]] || { printf 'missing failure %s in: %s\n' "$expected" "$output" >&2; exit 1; }
}

safe_db='postgres://e2e:e2e@127.0.0.1:5432/stratum_e2e?sslmode=disable'
expect_failure unsupported env TEST_DATABASE_URL="$safe_db" bash "$runner" invalid
expect_failure 'between 600 and 14400' env TEST_DATABASE_URL="$safe_db" STATEFUL_E2E_DURATION_SEC=599 bash "$runner" short
expect_failure 'release profile requires at least 3600' env TEST_DATABASE_URL="$safe_db" \
  STATEFUL_E2E_PROFILE=release STATEFUL_E2E_DURATION_SEC=3599 bash "$runner" soak
expect_failure 'short mode cannot declare' env TEST_DATABASE_URL="$safe_db" STATEFUL_E2E_PROFILE=test bash "$runner" short
expect_failure 'unknown stateful E2E pack' env TEST_DATABASE_URL="$safe_db" STATEFUL_E2E_PACKS=unknown bash "$runner" short
expect_failure 'unsafe E2E database target' env TEST_DATABASE_URL='postgres://u:p@prod-db/stratum' bash "$runner" short

cat >"$test_dir/digest" <<'SH'
#!/usr/bin/env bash
printf '%064d\n' 1
SH
cat >"$test_dir/process" <<'SH'
#!/usr/bin/env bash
trap 'touch "$STATEFUL_E2E_CLEANUP_MARKER"; exit 0' TERM INT
while :; do read -r -t 1 _ || true; done
SH
cat >"$test_dir/oauth-process" <<'SH'
#!/usr/bin/env bash
touch "$STATEFUL_E2E_OAUTH_STARTED_MARKER"
trap 'touch "$STATEFUL_E2E_OAUTH_CLEANUP_MARKER"; exit 0' TERM INT
while :; do read -r -t 1 _ || true; done
SH
cat >"$test_dir/mcp-process" <<'SH'
#!/usr/bin/env bash
touch "$STATEFUL_E2E_MCP_STARTED_MARKER"
trap 'touch "$STATEFUL_E2E_MCP_CLEANUP_MARKER"; exit 0' TERM INT
while :; do read -r -t 1 _ || true; done
SH
cat >"$test_dir/platform-mcp-process" <<'SH'
#!/usr/bin/env bash
touch "$STATEFUL_E2E_PLATFORM_MCP_STARTED_MARKER"
setsid sleep 300 &
printf '%s\n' "$!" >"$STATEFUL_E2E_PLATFORM_MCP_CHILD_PID_FILE"
trap 'touch "$STATEFUL_E2E_PLATFORM_MCP_CLEANUP_MARKER"; exit 0' TERM INT
while :; do read -r -t 1 _ || true; done
SH
cat >"$test_dir/backend-process" <<'SH'
#!/usr/bin/env bash
printf 'backend diagnostic marker\n'
[[ -e "$STATEFUL_E2E_OAUTH_STARTED_MARKER" ]] || exit 31
[[ -e "$STATEFUL_E2E_MCP_STARTED_MARKER" ]] || exit 36
[[ -e "$STATEFUL_E2E_PLATFORM_MCP_STARTED_MARKER" ]] || exit 38
[[ ${STRATUM_E2E_MODE:-} == true ]] || exit 32
[[ ${GITHUB_AUTHORIZE_URL:-} == http://127.0.0.1:19090/login/oauth/authorize ]] || exit 33
[[ ${GITHUB_TOKEN_URL:-} == http://127.0.0.1:19090/login/oauth/access_token ]] || exit 34
[[ ${GITHUB_USER_URL:-} == http://127.0.0.1:19090/user ]] || exit 35
[[ ${QWEN_BASE_URL:-} == http://127.0.0.1:19091/v1 ]] || exit 37
trap 'touch "$STATEFUL_E2E_CLEANUP_MARKER"; exit 0' TERM INT
while :; do read -r -t 1 _ || true; done
SH
cat >"$test_dir/playwright-pass" <<'SH'
#!/usr/bin/env bash
if [[ -v STATEFUL_E2E_EXPECTED_PROFILE ]]; then
  [[ ${STATEFUL_E2E_PROFILE:-} == "$STATEFUL_E2E_EXPECTED_PROFILE" ]] || exit 24
else
  [[ ! -v STATEFUL_E2E_PROFILE ]] || exit 25
fi
cat >"$STATEFUL_E2E_RESULTS_PATH" <<'JSON'
{"status":"passed","cleanup":{"passed":true},"unverified_capabilities":[],"packs":[{"id":"iam","status":"passed"}],"capabilities":[{"id":"iam.login","status":"passed"}]}
JSON
SH
cat >"$test_dir/playwright-skip" <<'SH'
#!/usr/bin/env bash
cat >"$STATEFUL_E2E_RESULTS_PATH" <<'JSON'
{"status":"passed","cleanup":{"passed":true},"unverified_capabilities":[],"packs":[{"id":"iam","status":"passed"}],"capabilities":[{"id":"iam.login","status":"skipped"}]}
JSON
SH
cat >"$test_dir/digest-change" <<'SH'
#!/usr/bin/env bash
count_file=${STATEFUL_E2E_DIGEST_COUNT:?}
count=0; [[ ! -e "$count_file" ]] || count=$(<"$count_file")
count=$((count + 1)); printf '%s' "$count" >"$count_file"
printf '%064d\n' "$count"
SH
cat >"$test_dir/attest" <<'SH'
#!/usr/bin/env bash
[[ ${1:-} == "$STATEFUL_E2E_RESULTS_PATH" ]] || exit 1
[[ ${STATEFUL_E2E_EXPECTED_PROFILE:-} == "${STATEFUL_E2E_PROFILE:-}" ]] || exit 2
touch "$STATEFUL_E2E_ATTESTATION_MARKER"
SH
cat >"$test_dir/migrate" <<'SH'
#!/usr/bin/env bash
[[ -z "${STATEFUL_E2E_MIGRATION_MARKER:-}" ]] || touch "$STATEFUL_E2E_MIGRATION_MARKER"
SH
cat >"$test_dir/migrate-fail" <<'SH'
#!/usr/bin/env bash
printf 'migration prepare failed\n' >&2
exit 23
SH
cat >"$test_dir/milvus-find" <<'SH'
#!/usr/bin/env bash
printf 'stateful-shared-milvus\n'
SH
cat >"$test_dir/milvus-start" <<'SH'
#!/usr/bin/env bash
[[ ${STATEFUL_E2E_SHARED_MILVUS_CONTAINER:-} == stateful-shared-milvus ]] || exit 41
touch "$STATEFUL_E2E_SHARED_MILVUS_READY_MARKER" "$STATEFUL_E2E_SHARED_MILVUS_STARTED_MARKER"
SH
cat >"$test_dir/milvus-stop" <<'SH'
#!/usr/bin/env bash
[[ ${STATEFUL_E2E_SHARED_MILVUS_CONTAINER:-} == stateful-shared-milvus ]] || exit 42
touch "$STATEFUL_E2E_SHARED_MILVUS_STOPPED_MARKER"
SH
chmod +x "$test_dir/digest" "$test_dir/process" "$test_dir/playwright-pass" "$test_dir/playwright-skip" \
  "$test_dir/oauth-process" "$test_dir/mcp-process" "$test_dir/platform-mcp-process" "$test_dir/backend-process" \
  "$test_dir/digest-change" "$test_dir/attest" \
  "$test_dir/migrate" "$test_dir/migrate-fail" "$test_dir/milvus-find" "$test_dir/milvus-start" "$test_dir/milvus-stop"

common=(env TEST_DATABASE_URL="$safe_db" STATEFUL_E2E_DIGEST_COMMAND="$test_dir/digest"
  STATEFUL_E2E_INFRA_UP_COMMAND=true STATEFUL_E2E_INFRA_DOWN_COMMAND=true
  STATEFUL_E2E_DATABASE_PREPARE_COMMAND=true STATEFUL_E2E_ENV_FILE="$test_dir/missing.env"
  STATEFUL_E2E_MIGRATION_COMMAND="$test_dir/migrate"
  STATEFUL_E2E_OAUTH_COMMAND="$test_dir/oauth-process" STATEFUL_E2E_MCP_COMMAND="$test_dir/mcp-process"
  STATEFUL_E2E_PLATFORM_MCP_COMMAND="$test_dir/platform-mcp-process"
  STATEFUL_E2E_BACKEND_COMMAND="$test_dir/backend-process"
  STATEFUL_E2E_FRONTEND_COMMAND="$test_dir/process" STATEFUL_E2E_HEALTH_ATTEMPTS=1
  STATEFUL_E2E_OAUTH_HEALTH_COMMAND=true STATEFUL_E2E_MCP_HEALTH_COMMAND=true
  STATEFUL_E2E_PLATFORM_MCP_HEALTH_COMMAND=true STATEFUL_E2E_FRONTEND_HEALTH_COMMAND=true
  STATEFUL_E2E_OAUTH_STARTED_MARKER="$test_dir/oauth-started"
  STATEFUL_E2E_OAUTH_CLEANUP_MARKER="$test_dir/oauth-cleaned"
  STATEFUL_E2E_MCP_STARTED_MARKER="$test_dir/mcp-started"
  STATEFUL_E2E_MCP_CLEANUP_MARKER="$test_dir/mcp-cleaned"
  STATEFUL_E2E_PLATFORM_MCP_STARTED_MARKER="$test_dir/platform-mcp-started"
  STATEFUL_E2E_PLATFORM_MCP_CLEANUP_MARKER="$test_dir/platform-mcp-cleaned"
  STATEFUL_E2E_PLATFORM_MCP_CHILD_PID_FILE="$test_dir/platform-mcp-child-pid")
cleanup_marker="$test_dir/cleaned"
failure_log_dir="$test_dir/failure-logs"
expect_failure 'backend failed health check' "${common[@]}" STATEFUL_E2E_CLEANUP_MARKER="$cleanup_marker" \
  STATEFUL_E2E_FAILURE_LOG_DIR="$failure_log_dir" \
  STATEFUL_E2E_BACKEND_HEALTH_COMMAND=false bash "$runner" short
grep -q 'backend diagnostic marker' "$failure_log_dir/backend.log" || {
  printf 'runner did not export backend failure log\n' >&2
  exit 1
}
for _ in {1..20}; do [[ -e "$cleanup_marker" ]] && break; sleep 0.05; done
[[ -e "$cleanup_marker" ]] || { printf 'runner did not clean up owned child processes\n' >&2; exit 1; }

oauth_cleanup_marker="$test_dir/oauth-cleaned"
for _ in {1..20}; do [[ -e "$oauth_cleanup_marker" ]] && break; sleep 0.05; done
[[ -e "$oauth_cleanup_marker" ]] || { printf 'runner did not clean up oauth process\n' >&2; exit 1; }

mcp_cleanup_marker="$test_dir/mcp-cleaned"
for _ in {1..20}; do [[ -e "$mcp_cleanup_marker" ]] && break; sleep 0.05; done
[[ -e "$mcp_cleanup_marker" ]] || { printf 'runner did not clean up MCP process\n' >&2; exit 1; }

platform_mcp_cleanup_marker="$test_dir/platform-mcp-cleaned"
for _ in {1..20}; do [[ -e "$platform_mcp_cleanup_marker" ]] && break; sleep 0.05; done
[[ -e "$platform_mcp_cleanup_marker" ]] || { printf 'runner did not clean up Platform MCP process\n' >&2; exit 1; }
platform_mcp_child_pid=$(<"$test_dir/platform-mcp-child-pid")
if kill -0 "$platform_mcp_child_pid" 2>/dev/null; then
  printf 'runner did not clean up Platform MCP child process\n' >&2
  exit 1
fi

expect_failure 'oauth provider failed health check' "${common[@]}" \
  STATEFUL_E2E_OAUTH_HEALTH_COMMAND=false STATEFUL_E2E_BACKEND_HEALTH_COMMAND=true bash "$runner" short

expect_failure 'MCP server failed health check' "${common[@]}" \
  STATEFUL_E2E_MCP_HEALTH_COMMAND=false STATEFUL_E2E_BACKEND_HEALTH_COMMAND=true bash "$runner" short

expect_failure 'Platform MCP server failed health check' "${common[@]}" \
  STATEFUL_E2E_PLATFORM_MCP_HEALTH_COMMAND=false STATEFUL_E2E_BACKEND_HEALTH_COMMAND=true bash "$runner" short

expect_failure 'failed or skipped coverage' "${common[@]}" STATEFUL_E2E_CLEANUP_MARKER="$cleanup_marker" \
  STATEFUL_E2E_BACKEND_HEALTH_COMMAND=true STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-skip" \
  bash "$runner" short

digest_count="$test_dir/digest-count"
expect_failure 'covered source changed' "${common[@]}" STATEFUL_E2E_CLEANUP_MARKER="$cleanup_marker" \
  STATEFUL_E2E_DIGEST_COMMAND="$test_dir/digest-change" STATEFUL_E2E_DIGEST_COUNT="$digest_count" \
  STATEFUL_E2E_BACKEND_HEALTH_COMMAND=true STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-pass" \
  bash "$runner" short

attestation_marker="$test_dir/attested"
migration_marker="$test_dir/migrated"
"${common[@]}" STATEFUL_E2E_CLEANUP_MARKER="$cleanup_marker" STATEFUL_E2E_BACKEND_HEALTH_COMMAND=true \
  STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-pass" STATEFUL_E2E_ATTESTATION_MARKER="$attestation_marker" \
  STATEFUL_E2E_MIGRATION_MARKER="$migration_marker" \
  STATEFUL_E2E_ATTESTATION_COMMAND="$test_dir/attest \"\$STATEFUL_E2E_RESULTS_PATH\"" bash "$runner" short
[[ -e "$migration_marker" ]] || { printf 'public schema migration was not executed\n' >&2; exit 1; }
[[ -e "$attestation_marker" ]] || { printf 'safe result was not forwarded to attestation generation\n' >&2; exit 1; }

test_profile_attestation_marker="$test_dir/test-profile-attested"
"${common[@]}" STATEFUL_E2E_MODE=soak STATEFUL_E2E_EXPECTED_PROFILE=test \
  STATEFUL_E2E_CLEANUP_MARKER="$cleanup_marker" STATEFUL_E2E_BACKEND_HEALTH_COMMAND=true \
  STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-pass" \
  STATEFUL_E2E_ATTESTATION_MARKER="$test_profile_attestation_marker" \
  STATEFUL_E2E_ATTESTATION_COMMAND="$test_dir/attest \"\$STATEFUL_E2E_RESULTS_PATH\"" bash "$runner" soak
[[ -e "$test_profile_attestation_marker" ]] || { printf 'soak did not default to the test profile\n' >&2; exit 1; }

expect_failure 'migration prepare failed' "${common[@]}" STATEFUL_E2E_MIGRATION_COMMAND="$test_dir/migrate-fail" \
  bash "$runner" short

milvus_ready_marker="$test_dir/milvus-ready"
milvus_started_marker="$test_dir/milvus-started"
milvus_stopped_marker="$test_dir/milvus-stopped"
"${common[@]}" STATEFUL_E2E_INFRA_UP_COMMAND= STATEFUL_E2E_BASE_INFRA_HEALTH_COMMAND=true \
  STATEFUL_E2E_MILVUS_HEALTH_COMMAND="test -e '$milvus_ready_marker'" \
  STATEFUL_E2E_SHARED_MILVUS_FIND_COMMAND="$test_dir/milvus-find" \
  STATEFUL_E2E_SHARED_MILVUS_START_COMMAND="$test_dir/milvus-start" \
  STATEFUL_E2E_SHARED_MILVUS_STOP_COMMAND="$test_dir/milvus-stop" \
  STATEFUL_E2E_SHARED_MILVUS_READY_MARKER="$milvus_ready_marker" \
  STATEFUL_E2E_SHARED_MILVUS_STARTED_MARKER="$milvus_started_marker" \
  STATEFUL_E2E_SHARED_MILVUS_STOPPED_MARKER="$milvus_stopped_marker" \
  STATEFUL_E2E_BACKEND_HEALTH_COMMAND=true STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-pass" \
  STATEFUL_E2E_ATTESTATION_MARKER="$test_dir/shared-attested" \
  STATEFUL_E2E_ATTESTATION_COMMAND="$test_dir/attest \"\$STATEFUL_E2E_RESULTS_PATH\"" bash "$runner" short
[[ -e "$milvus_started_marker" ]] || { printf 'runner did not start shared Milvus\n' >&2; exit 1; }
[[ -e "$milvus_stopped_marker" ]] || { printf 'runner did not restore shared Milvus state\n' >&2; exit 1; }

printf 'system stateful runner contract tests passed\n'
