#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-eval-checks.XXXXXX")

bin_dir="$root/bin"
bin="$bin_dir/e2e-eval-check"
backup=""
if [[ -e "$bin" ]]; then
  backup="$test_dir/e2e-eval-check.real"
  mv "$bin" "$backup"
fi
mkdir -p "$bin_dir"

restore() {
  rm -f "$bin"
  [[ -z "$backup" ]] || mv "$backup" "$bin"
}
trap 'restore; rm -rf "$test_dir"' EXIT

stub="$test_dir/e2e-eval-check"
cat >"$stub" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_LOG"
exit "${STUB_EXIT:-0}"
EOF
chmod +x "$stub"
ln -s "$stub" "$bin"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

# Case 1: 未命中 eval-touched → 跳过，exit 0，不调用 stub。
jq -n '{matched_rules:["tenant-boundary"],eval_points:["knowledge/retrieval"]}' >"$test_dir/plan-no-eval.json"
: >"$test_dir/log1"
STUB_LOG="$test_dir/log1" TEST_VERIFY_PLAN_PATH="$test_dir/plan-no-eval.json" \
  bash "$root/scripts/quality/run-eval-checks.sh" >/dev/null 2>&1 || fail "no eval-touched must exit 0"
[[ ! -s "$test_dir/log1" ]] || fail "no eval-touched must not invoke the binary"

# Case 2: eval-touched 命中但缺 eval_points → fail closed，exit 1。
jq -n '{matched_rules:["eval-touched"]}' >"$test_dir/plan-no-points.json"
: >"$test_dir/log2"
STUB_LOG="$test_dir/log2" TEST_VERIFY_PLAN_PATH="$test_dir/plan-no-points.json" \
  bash "$root/scripts/quality/run-eval-checks.sh" >/dev/null 2>&1 && fail "missing eval_points must fail closed"
[[ ! -s "$test_dir/log2" ]] || fail "fail-closed path must not invoke the binary"

# Case 3: eval_points 枚举 → 每个 point 调一次 stub；nested knowledge 传 --kind knowledge --point retrieval。
jq -n '{matched_rules:["eval-touched"],eval_points:["knowledge/retrieval","mcp/weather-mcp"]}' \
  >"$test_dir/plan-eval.json"
: >"$test_dir/log3"
STUB_LOG="$test_dir/log3" TEST_VERIFY_PLAN_PATH="$test_dir/plan-eval.json" \
  STRATUM_EVAL_BASE_URL="http://localhost:8080" \
  bash "$root/scripts/quality/run-eval-checks.sh" >/dev/null 2>&1 || fail "eval_points enumeration must exit 0"
grep -q -- '--kind knowledge --point retrieval --fail-on-warn --base-url http://localhost:8080' "$test_dir/log3" \
  || { printf 'knowledge point invocation missing\n' >&2; cat "$test_dir/log3" >&2; exit 1; }
grep -q -- '--kind mcp --point weather-mcp --fail-on-warn --base-url http://localhost:8080' "$test_dir/log3" \
  || { printf 'mcp point invocation missing\n' >&2; cat "$test_dir/log3" >&2; exit 1; }

# Case 4: 单个 point 失败 → 整体 exit 1 且继续执行后续 point（失败传播不吞错）。
cat >"$stub" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_LOG"
[[ "$*" != *"--point retrieval"* ]]
EOF
chmod +x "$stub"
: >"$test_dir/log4"
STUB_LOG="$test_dir/log4" TEST_VERIFY_PLAN_PATH="$test_dir/plan-eval.json" \
  bash "$root/scripts/quality/run-eval-checks.sh" >/dev/null 2>&1 && fail "point failure must fail the eval run"
grep -q -- '--point weather-mcp' "$test_dir/log4" || fail "must continue running remaining points after a failure"

printf 'run-eval-checks orchestration behavior passed\n'
