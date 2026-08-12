#!/usr/bin/env bash
# check-baseline-integrity.sh — 防止刷基线绕门禁
#
# 规则：
#   基线只能缩小不能扩大。新增违规条目必须被拒绝——
#   无论是否有 .go 代码变更，新违规不应进入基线。
#   已有条目因代码修复而消失是合法的，自动放行。
#
# 两种模式：
#   默认（pre-commit 钩子，无参）：比较 staged 与 HEAD，基线必须出现在 staged 变更中。
#   --ci：比较 working tree 与 base 版本（PR 取 merge-base origin/main；push 用 BEFORE_REF）。
#
#   CI 语义陷阱：actions/checkout 在 PR 下检出 refs/pull/N/merge 合成 commit，
#   其 tree 已含 PR 改动 —— 比较 HEAD vs working tree 恒相等、守卫永不触发。
#   必须用 merge-base 取 fork 点；GITHUB_BASE_REF 非空但 base 不可用时显式失败
#   （fail-closed），绝不回退 HEAD 静默放行。此模式依赖 code-quality job 的
#   checkout 配置 fetch-depth: 0。
set -euo pipefail

BASELINE="scripts/quality/code-quality-baseline.json"

# baseline_ids 从 stdin 读基线 JSON，输出每条违规的 id。
# 解析失败即非零退出（fail-closed），不允许"JSON 损坏 → 空集放行"。
baseline_ids() {
  python3 -c '
import json, sys
data = json.load(sys.stdin)
for f in data.get("functions", []):
    print(f["id"])
'
}

if [[ "${1:-}" == "--ci" ]]; then
  # ── CI 模式：working tree vs base 分支 ──
  if [[ -n "${GITHUB_BASE_REF:-}" ]]; then
    # PR 事件：以 fork 点为准，避免 base 分支前进造成误报
    base_commit=$(git merge-base origin/main HEAD 2>/dev/null || true)
    if [[ -z "${base_commit}" ]]; then
      cat >&2 <<'EOF'
============================================================
  ❌ 基线防刷守卫无法定位 base 分支（origin/main）。

  该检查需要完整 git 历史：code-quality job 的 checkout 必须
  配置 fetch-depth: 0。base 不可用时禁止静默放行（fail-closed）。
============================================================
EOF
      exit 1
    fi
  else
    # push 事件：以上一 main tip（github.event.before）为基准；
    # 全零（新分支首推）无法比较，显式跳过
    base_commit="${BEFORE_REF:-}"
    if [[ -z "${base_commit}" || "${base_commit}" =~ ^0+$ ]]; then
      echo "push 无 before 提交（新分支首推），跳过基线防刷比对。"
      exit 0
    fi
  fi

  # 快路径：基线文件与 base 一致（未改动）直接通过；
  # base 上不存在基线文件（历史新增）同样放行，避免误拦截
  git cat-file -e "${base_commit}:${BASELINE}" 2>/dev/null || exit 0
  if git show "${base_commit}:${BASELINE}" | cmp -s - "${BASELINE}"; then
    exit 0
  fi

  head_ids=$(git show "${base_commit}:${BASELINE}" | baseline_ids | LC_ALL=C sort)
  candidate_ids=$(< "${BASELINE}" baseline_ids | LC_ALL=C sort)
else
  # ── 本地/pre-commit 模式（staged 语义）──
  if ! git diff --cached --name-only | grep -qFx "${BASELINE}"; then
    exit 0
  fi

  head_ids=$(git show "HEAD:${BASELINE}" | baseline_ids | LC_ALL=C sort)
  candidate_ids=$(git show ":${BASELINE}" | baseline_ids | LC_ALL=C sort)
fi

# 新增条目：candidate 有但 head 没有 → 拦截
new_ids=$(comm -13 <(printf '%s\n' "$head_ids") <(printf '%s\n' "$candidate_ids") | grep -c . || true)
# 修复条目：head 有但 candidate 没有 → 允许
fixed_ids=$(comm -23 <(printf '%s\n' "$head_ids") <(printf '%s\n' "$candidate_ids") | grep -c . || true)

if [[ "${new_ids}" -gt 0 ]]; then
  comm -13 <(printf '%s\n' "$head_ids") <(printf '%s\n' "$candidate_ids") >&2
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
