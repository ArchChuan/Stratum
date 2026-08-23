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
  local checks=$1 expected=$2 ci=${3:-}
  : >"$log"
  if [[ -n "$ci" ]]; then
    jq -n --argjson checks "$checks" --argjson ci "$ci" \
      '{local_checks:$checks,ci_checks:$ci}' >"$test_dir/plan.json"
  else
    jq -n --argjson checks "$checks" '{local_checks:$checks}' >"$test_dir/plan.json"
  fi
  if [[ -n "$ci" ]]; then
    PATH="$test_dir:$PATH" TEST_VERIFY_PLAN_PATH="$test_dir/plan.json" CI_OWNED=1 \
      bash "$root/scripts/quality/run-planned-checks.sh"
  else
    PATH="$test_dir:$PATH" TEST_VERIFY_PLAN_PATH="$test_dir/plan.json" \
      bash "$root/scripts/quality/run-planned-checks.sh"
  fi
  # `-p N` 是负载感知动态值（CI 2 核→1，本地 12 核→4），契约只守结构不守具体值。
  # 分组并行执行后组间顺序不确定，契约只守"执行了哪些命令"而非先后次序，先排序再比较。
  actual=$(sort "$log" | paste -sd' ' | sed -E 's/-p [0-9]+/-p N/g')
  if [[ "$actual" != "$expected" ]]; then
    printf 'planned checks: got %q, want %q\n' "$actual" "$expected" >&2
    exit 1
  fi
}

run_case '["docs-lint"]' 'make:agent-instructions-check'
run_case '["static","unit","build","code-quality"]' \
  'go:build -p N ./cmd/server go:list ./... go:test -short -p N github.com/byteBuilderX/stratum/internal/agent go:vet ./... make:-o proto-gen fe-lint fe-build make:-o proto-gen risk-guardrails code-quality make:proto-gen'
run_case '["static","unit","integration","contract","domain-failure-paths","e2e-short","e2e-soak"]' \
  'go:list ./... go:test -short -p N github.com/byteBuilderX/stratum/internal/agent go:vet ./... make:-o proto-gen contract-test make:-o proto-gen risk-guardrails code-quality make:proto-gen'

# CI_OWNED=1：CI 兜底的单元全部跳过，本地只保留非 CI 项（E2E 由 before-pr 脚本的 run_browser_mode 执行）
run_case '["static","unit","integration","contract","code-quality","e2e-short"]' \
  '' '["static","unit","integration","contract","build"]'
# CI_OWNED=1：docs-lint 不在 ci_checks，保留执行；static/build 由 CI 兜底跳过
run_case '["docs-lint","static","build"]' \
  'make:agent-instructions-check' '["static","unit","integration","contract","build","security","risk-guardrails"]'
# CI_OWNED=1：R3 全量 local 集（含 domain-failure-paths），全部由 CI 兜底 → 无本地 make/go 调用
run_case '["static","unit","integration","contract","domain-failure-paths","e2e-soak"]' \
  '' '["static","unit","integration","contract","build","security","risk-guardrails"]'

# CI_OWNED=1 但 plan 缺 ci_checks 声明 → fail closed（宁可全跑也不误跳过）
jq -n '{local_checks:["static"]}' >"$test_dir/plan.json"
if PATH="$test_dir:$PATH" TEST_VERIFY_PLAN_PATH="$test_dir/plan.json" CI_OWNED=1 \
  bash "$root/scripts/quality/run-planned-checks.sh" 2>/dev/null; then
  printf 'CI_OWNED without ci_checks must fail closed\n' >&2
  exit 1
fi

printf 'planned local check behavior passed\n'
