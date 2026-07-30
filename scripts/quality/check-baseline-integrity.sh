#!/usr/bin/env bash
# check-baseline-integrity.sh — 防止刷基线绕门禁
#
# 规则：
#   基线只能缩小不能扩大。新增违规条目必须被拒绝——
#   无论是否有 .go 代码变更，新违规不应进入基线。
#   已有条目因代码修复而消失是合法的，自动放行。
set -euo pipefail

BASELINE="scripts/quality/code-quality-baseline.json"

if ! git diff --cached --name-only | grep -qFx "${BASELINE}"; then
  exit 0
fi

head_ids=$(git show "HEAD:${BASELINE}" 2>/dev/null | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    for f in data.get('functions', []):
        print(f['id'])
except Exception:
    pass
" 2>/dev/null | LC_ALL=C sort)

staged_ids=$(git show ":${BASELINE}" 2>/dev/null | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    for f in data.get('functions', []):
        print(f['id'])
except Exception:
    pass
" 2>/dev/null | LC_ALL=C sort)

# 新增条目：staged 有但 HEAD 没有 → 拦截
new_ids=$(comm -13 <(echo "$head_ids") <(echo "$staged_ids") | grep -c . || true)
# 修复条目：HEAD 有但 staged 没有 → 允许
fixed_ids=$(comm -23 <(echo "$head_ids") <(echo "$staged_ids") | grep -c . || true)

if [[ "${new_ids}" -gt 0 ]]; then
  comm -13 <(echo "$head_ids") <(echo "$staged_ids") >&2
  cat >&2 <<EOF
============================================================
  ❌ 基线扩容拦截

  基线新增了 ${new_ids} 个违规条目（见上）。

  基线只允许缩小（修复函数后条目自动消失），不允许扩大。
  新出现的超标函数必须修复代码降低复杂度，不能刷基线掩埋。

  正确做法：修改代码使函数达标，而非 make code-quality-baseline。
============================================================
EOF
  exit 1
fi

if [[ "${fixed_ids}" -gt 0 ]]; then
  echo "基线缩小：${fixed_ids} 个违规条目已修复，通过。"
fi

exit 0
