#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
plan=${TEST_VERIFY_PLAN_PATH:-$root/tmp/test-verification/plan.json}
go_command=${TEST_VERIFY_GO_COMMAND:-go}
bin="$root/bin/e2e-eval-check"

[[ -f "$plan" ]] || { printf 'verification plan is missing: %s\n' "$plan" >&2; exit 1; }

# 未命中 eval-touched 规则则跳过；plan 缺 eval_points 时 fail closed，
# 宁可显式失败也不静默跳过评测（避免把未评测当作已评测合入）。
jq -e '.matched_rules | index("eval-touched")' "$plan" >/dev/null 2>&1 || {
  printf 'eval: no eval-touched rule matched, skipping\n' >&2
  exit 0
}
jq -e '.eval_points | type == "array"' "$plan" >/dev/null 2>&1 ||
  { printf 'eval: eval-touched matched but eval_points missing in plan\n' >&2; exit 1; }

# 优先用预编译二进制（CI 已 build），否则现场编译。
if [[ ! -x "$bin" ]]; then
  "$go_command" build -o "$bin" "$root/cmd/e2e-eval-check"
fi

# base-url/tenant-id/user-id 非必填（必填性由 CLI parseOptions 决定）：
# 仅在显式提供时透传，避免把空字符串语义强加给下游。
common_args=()
[[ -z "${STRATUM_EVAL_BASE_URL:-}" ]] || common_args+=(--base-url "$STRATUM_EVAL_BASE_URL")
[[ -z "${STRATUM_EVAL_TENANT_ID:-}" ]] || common_args+=(--tenant-id "$STRATUM_EVAL_TENANT_ID")
[[ -z "${STRATUM_EVAL_USER_ID:-}" ]] || common_args+=(--user-id "$STRATUM_EVAL_USER_ID")

failed=0
while IFS= read -r point; do
  [[ -z "$point" ]] && continue
  kind=${point%%/*}
  key=${point#*/}
  printf 'eval: running %s/%s\n' "$kind" "$key" >&2
  "$bin" --kind "$kind" --point "$key" --fail-on-warn "${common_args[@]}" ||
    { printf 'eval: FAILED %s/%s\n' "$kind" "$key" >&2; failed=1; }
done < <(jq -er '.eval_points[]' "$plan")

exit "$failed"
