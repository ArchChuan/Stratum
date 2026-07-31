#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-before-pr.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT
log=$test_dir/log

write_command() {
  local name=$1 action=$2
  cat >"$test_dir/$name" <<EOF
#!/usr/bin/env bash
printf '%s\\n' '$action' >>'$log'
EOF
  chmod +x "$test_dir/$name"
}

cat >"$test_dir/make" <<EOF
#!/usr/bin/env bash
printf 'make:%s\\n' "\${*: -1}" >>'$log'
EOF
chmod +x "$test_dir/make"
write_command plan plan
write_command focused focused
write_command writer write-report
write_command checker check-report
chmod 0644 "$test_dir/plan" "$test_dir/focused" "$test_dir/writer" "$test_dir/checker"

run_case() {
  local risk=$1 mode=$2 expected=$3
  : >"$log"
  jq -n --arg risk "$risk" --arg mode "$mode" \
    '{version:1,commit:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      manifest_digest:"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      risk_level:$risk,mode:$mode,local_checks:["static"],ci_checks:["static"]}' >"$test_dir/plan.json"
  TEST_VERIFY_PLAN_PATH="$test_dir/plan.json" TEST_VERIFY_PLAN_COMMAND="$test_dir/plan" \
    TEST_VERIFY_CHECKS_COMMAND="$test_dir/focused" TEST_VERIFY_MAKE_COMMAND="$test_dir/make" \
    LOCAL_VERIFY_WRITER="$test_dir/writer" LOCAL_VERIFY_CHECKER="$test_dir/checker" \
    LOCAL_VERIFY_ATTESTATION_PATH="$test_dir/attestation.json" \
    bash "$root/scripts/quality/test-verify-before-pr.sh" >/dev/null
  actual=$(paste -sd' ' "$log")
  if [[ "$actual" != "$expected" ]]; then
    printf '%s orchestration: got %q, want %q\n' "$risk" "$actual" "$expected" >&2
    exit 1
  fi
}

run_case R1 none 'plan focused write-report check-report'
run_case R2 short 'plan focused make:e2e-system-short write-report check-report'
run_case R3 soak 'plan focused make:e2e-system-short make:e2e-system-soak write-report check-report'
run_case R4 release-soak \
  'plan focused make:e2e-system-short make:e2e-system-soak make:e2e-system-release-soak write-report check-report'

printf 'before-PR orchestration behavior passed\n'
