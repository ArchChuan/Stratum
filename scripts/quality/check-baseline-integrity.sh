#!/usr/bin/env bash
# check-baseline-integrity.sh — 防止刷基线绕门禁
#
# 规则：
#   基线只能缩小不能扩大。新增违规条目必须被拒绝——
#   无论是否有 .go 代码变更，新违规不应进入基线。
#   已有条目因代码修复而消失是合法的，自动放行。
#   同函数跨文件迁移（函数签名名未变，仅 id/file 变化）视为合法，
#   例如把存量超限函数移动到新拆分的同包文件；函数身份的判定以
#   「签名名（如 AgentService.Method）」为主、body_hash 为辅：
#   候选条目与 head 存在同名签名，或函数体字节相同，即视为同一函数迁移；
#   只有签名名与函数体均为全新才判定为新增违规拦截。
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

# baseline_diff 比较两份基线 JSON（head、candidate，均经文件路径传入），输出：
#   stdout: 真新增违规条目 id（candidate 有而 head 没有，且 body_hash 在 head 中不存在）
#   stderr: 迁移/修复计数（MIGRATED=.. FIXED=..），供人读，不参与判断
# 解析失败即非零退出（fail-closed），不允许"JSON 损坏 → 空集放行"。
baseline_diff() {
  python3 - "$1" "$2" <<'PY'
import json
import sys


def load(path):
    with open(path, encoding="utf-8") as f:
        data = json.load(f)
    return {f["id"]: f.get("body_hash", "") for f in data.get("functions", [])}


head = load(sys.argv[1])
candidate = load(sys.argv[2])
head_names = {fid.split(":", 1)[1] for fid in head}
head_hashes = set(head.values())

new_ids, migrated = [], []
for fid, h in candidate.items():
    if fid in head:
        continue
    name = fid.split(":", 1)[1]
    # 同名签名（跨文件拆分/移动）或同函数体字节 → 视为同一函数迁移；否则视为新增违规。
    if name in head_names or (h and h in head_hashes):
        migrated.append(fid)
    else:
        new_ids.append(fid)
fixed = [fid for fid in head if fid not in candidate]

for fid in new_ids:
    print(fid)
print(f"MIGRATED={len(migrated)} FIXED={len(fixed)}", file=sys.stderr)
PY
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

  head_src=$(git show "${base_commit}:${BASELINE}")
  candidate_src=$(cat "${BASELINE}")
else
  # ── 本地/pre-commit 模式（staged 语义）──
  if ! git diff --cached --name-only | grep -qFx "${BASELINE}"; then
    exit 0
  fi

  head_src=$(git show "HEAD:${BASELINE}")
  candidate_src=$(git show ":${BASELINE}")
fi

new_ids=$(baseline_diff <(printf '%s' "${head_src}") <(printf '%s' "${candidate_src}"))

if [[ -n "${new_ids}" ]]; then
  printf '%s\n' "${new_ids}" >&2
  new_count=$(printf '%s\n' "${new_ids}" | grep -c . || true)
  cat >&2 <<EOF
============================================================
  ❌ 基线扩容拦截

  基线新增了 ${new_count} 个违规条目（见上）。

  基线只允许缩小（修复函数后条目自动消失）或同函数迁移
  （函数体未变、仅换文件），不允许扩大。
  新出现的超标函数必须修复代码降低复杂度，不能刷基线掩埋。

  正确做法：修改代码使函数达标，而非 make code-quality-baseline。
============================================================
EOF
  exit 1
fi

exit 0
