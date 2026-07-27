#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
runner="$repo_dir/scripts/e2e/system-stateful.sh"
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-stateful-runner-test.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT

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
cat >"$test_dir/playwright-pass" <<'SH'
#!/usr/bin/env bash
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
touch "$STATEFUL_E2E_ATTESTATION_MARKER"
SH
chmod +x "$test_dir/digest" "$test_dir/process" "$test_dir/playwright-pass" "$test_dir/playwright-skip" \
  "$test_dir/digest-change" "$test_dir/attest"

common=(env TEST_DATABASE_URL="$safe_db" STATEFUL_E2E_DIGEST_COMMAND="$test_dir/digest"
  STATEFUL_E2E_INFRA_UP_COMMAND=true STATEFUL_E2E_INFRA_DOWN_COMMAND=true
  STATEFUL_E2E_BACKEND_COMMAND="$test_dir/process" STATEFUL_E2E_FRONTEND_COMMAND="$test_dir/process"
  STATEFUL_E2E_HEALTH_ATTEMPTS=1 STATEFUL_E2E_FRONTEND_HEALTH_COMMAND=true)
cleanup_marker="$test_dir/cleaned"
expect_failure 'backend failed health check' "${common[@]}" STATEFUL_E2E_CLEANUP_MARKER="$cleanup_marker" \
  STATEFUL_E2E_BACKEND_HEALTH_COMMAND=false bash "$runner" short
for _ in {1..20}; do [[ -e "$cleanup_marker" ]] && break; sleep 0.05; done
[[ -e "$cleanup_marker" ]] || { printf 'runner did not clean up owned child processes\n' >&2; exit 1; }

expect_failure 'failed or skipped coverage' "${common[@]}" STATEFUL_E2E_CLEANUP_MARKER="$cleanup_marker" \
  STATEFUL_E2E_BACKEND_HEALTH_COMMAND=true STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-skip" \
  bash "$runner" short

digest_count="$test_dir/digest-count"
expect_failure 'covered source changed' "${common[@]}" STATEFUL_E2E_CLEANUP_MARKER="$cleanup_marker" \
  STATEFUL_E2E_DIGEST_COMMAND="$test_dir/digest-change" STATEFUL_E2E_DIGEST_COUNT="$digest_count" \
  STATEFUL_E2E_BACKEND_HEALTH_COMMAND=true STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-pass" \
  bash "$runner" short

attestation_marker="$test_dir/attested"
"${common[@]}" STATEFUL_E2E_CLEANUP_MARKER="$cleanup_marker" STATEFUL_E2E_BACKEND_HEALTH_COMMAND=true \
  STATEFUL_E2E_PLAYWRIGHT_COMMAND="$test_dir/playwright-pass" STATEFUL_E2E_ATTESTATION_MARKER="$attestation_marker" \
  STATEFUL_E2E_ATTESTATION_COMMAND="$test_dir/attest \"\$STATEFUL_E2E_RESULTS_PATH\"" bash "$runner" short
[[ -e "$attestation_marker" ]] || { printf 'safe result was not forwarded to attestation generation\n' >&2; exit 1; }

printf 'system stateful runner contract tests passed\n'
