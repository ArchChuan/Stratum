#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-planned-checks.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT
log=$test_dir/log

cat >"$test_dir/make" <<EOF
#!/usr/bin/env bash
printf 'make:%s\\n' "\$*" >>'$log'
EOF
cat >"$test_dir/go" <<EOF
#!/usr/bin/env bash
printf 'go:%s\\n' "\$*" >>'$log'
EOF
chmod +x "$test_dir/make" "$test_dir/go"

run_case() {
  local checks=$1 expected=$2
  : >"$log"
  jq -n --argjson checks "$checks" '{local_checks:$checks}' >"$test_dir/plan.json"
  PATH="$test_dir:$PATH" TEST_VERIFY_PLAN_PATH="$test_dir/plan.json" \
    bash "$root/scripts/quality/run-planned-checks.sh"
  actual=$(paste -sd' ' "$log")
  if [[ "$actual" != "$expected" ]]; then
    printf 'planned checks: got %q, want %q\n' "$actual" "$expected" >&2
    exit 1
  fi
}

run_case '["docs-lint"]' 'make:agent-instructions-check'
run_case '["static","unit","build","code-quality"]' \
  'make:risk-guardrails code-quality go:vet ./... go:test -short ./... go:build ./cmd/server make:fe-lint fe-build'
run_case '["static","unit","integration","contract","domain-failure-paths","e2e-short","e2e-soak"]' \
  'make:risk-guardrails code-quality go:vet ./... go:test -short ./... make:contract-test'

printf 'planned local check behavior passed\n'
