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
if [[ "\$1" == list ]]; then
  printf '%s\\n' github.com/byteBuilderX/stratum/internal/agent \
    github.com/byteBuilderX/stratum/test/e2e \
    github.com/byteBuilderX/stratum/web/node_modules/example
fi
EOF
chmod +x "$test_dir/make" "$test_dir/go"

run_case() {
  local checks=$1 expected=$2
  : >"$log"
  jq -n --argjson checks "$checks" '{local_checks:$checks}' >"$test_dir/plan.json"
  PATH="$test_dir:$PATH" TEST_VERIFY_PLAN_PATH="$test_dir/plan.json" \
    bash "$root/scripts/quality/run-planned-checks.sh"
  # `-p N` 是负载感知动态值（CI 2 核→1，本地 12 核→4），契约只守结构不守具体值
  actual=$(paste -sd' ' "$log" | sed -E 's/-p [0-9]+/-p N/g')
  if [[ "$actual" != "$expected" ]]; then
    printf 'planned checks: got %q, want %q\n' "$actual" "$expected" >&2
    exit 1
  fi
}

run_case '["docs-lint"]' 'make:agent-instructions-check'
run_case '["static","unit","build","code-quality"]' \
  'make:risk-guardrails code-quality go:vet ./... go:list ./... go:test -short -p N github.com/byteBuilderX/stratum/internal/agent go:build -p N ./cmd/server make:fe-lint fe-build'
run_case '["static","unit","integration","contract","domain-failure-paths","e2e-short","e2e-soak"]' \
  'make:risk-guardrails code-quality go:vet ./... go:list ./... go:test -short -p N github.com/byteBuilderX/stratum/internal/agent make:contract-test'

printf 'planned local check behavior passed\n'
