#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TASK="${ROOT}/scripts/agent-interview/daily-agent-interview.sh"
FAKE_CODEX="${ROOT}/scripts/agent-interview/testdata/fake-codex.sh"
VALIDATOR="${ROOT}/scripts/agent-interview/validate-library.sh"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "${TEST_ROOT}"' EXIT

readonly CATEGORY_FILES=(
  01-agent-runtime-and-workflow.md
  02-tools-mcp-and-approval.md
  03-context-and-memory.md
  04-knowledge-and-rag.md
  05-llm-gateway-and-model-routing.md
  06-reliability-and-streaming.md
  07-evaluation-observability-and-cost.md
  08-security-iam-and-multitenancy.md
  09-architecture-and-production-readiness.md
  99-unclassified.md
)

fail() { echo "$*" >&2; exit 1; }

write_category() {
  local path="$1" id="$2"
  cat >"${path}" <<EOF
# Test Category

## 分类边界

测试边界。

## 趋势与观点

### T-${id}-base 基础趋势

基础趋势。

## 面试题

### Q-${id}-base 基础问题

- 作答要点：基础答案。
- 深挖问题：基础追问。
- Stratum 实现与边界：基础边界。
- 相关源码/文档：\`docs/agent/agent.md\`
- 来源：SRC-base-${id}
- 首次收录：2026-07-09
- 最近更新：2026-07-09

## Stratum 可补强点

### G-${id}-base 基础补强点

基础建议。

## 跟踪关键词

- base keyword

## 参考来源

### SRC-base-${id}

- URL: https://example.com/base-${id}
- 标题: Base Source
- 类型: official
- 首次收录: 2026-07-09
- 最近核验: 2026-07-09
EOF
}

build_library() {
  local out="$1" file id
  mkdir -p "${out}/reports/inbox"
  cat >"${out}/reports/README.md" <<'EOF'
# Agent 高级开发面试资料索引

## 固定分类

固定十类。

## 分类优先级

唯一主分类。

## 稳定 ID 与去重规则

来源身份与内容哈希共同确定幂等。

## 已处理报告

| run_id | report_date | sha256 | input_count | created | updated | duplicate | unclassified |
|---|---|---|---:|---:|---:|---:|---:|

## 融合状态

- 最近成功融合：2026-07-09T00:00:00+08:00
- 原始条目数：1
- 稳定条目数：10
- 重复条目数：0
- 待分类条目数：0
EOF
  for file in "${CATEGORY_FILES[@]}"; do
    id="${file%%-*}"
    id="${id#0}"
    [[ "${file}" == 99-* ]] && id=99
    write_category "${out}/reports/${file}" "${id}"
  done
  cat >"${out}/reports/99-unclassified.md" <<'EOF'
# 待分类

## 分类边界

无法唯一分类的内容。

## 趋势与观点

当前无。

## 面试题

当前无。

## Stratum 可补强点

当前无。

## 跟踪关键词

当前无。

## 参考来源

当前无。
EOF
  ln -s README.md "${out}/reports/latest.md"
}

run_task() {
  local out="$1" mode="${2:-success}"
  STRATUM_ROOT="${ROOT}" \
    AGENT_INTERVIEW_OUT_DIR="${out}" \
    AGENT_INTERVIEW_CODEX_MODEL='' \
    AGENT_INTERVIEW_RUN_ID=20260724-080000 \
    AGENT_INTERVIEW_REPORT_DATE=2026-07-24 \
    CODEX_BIN="${FAKE_CODEX}" \
    FAKE_CODEX_MODE="${mode}" \
    "${TASK}" --fuse-only
}

out="${TEST_ROOT}/success"
build_library "${out}"
AGENT_INTERVIEW_RUN_ID=20260724-080000 \
  AGENT_INTERVIEW_REPORT_DATE=2026-07-24 \
  "${FAKE_CODEX}" -o "${out}/reports/inbox/20260724-080000.md"
run_task "${out}"
"${VALIDATOR}" --library "${out}/reports"
grep -Fq 'Q-1-generated' "${out}/reports/01-agent-runtime-and-workflow.md" || \
  fail 'successful fusion did not publish generated question'
[[ ! -e "${out}/reports/inbox/20260724-080000.md" ]] || fail 'successful fusion retained consumed input'

find "${out}/reports" -maxdepth 1 -type f -name '*.md' -print0 | sort -z | \
  xargs -0 sha256sum >"${TEST_ROOT}/before-repeat.sha"
AGENT_INTERVIEW_RUN_ID=20260724-080000 \
  AGENT_INTERVIEW_REPORT_DATE=2026-07-24 \
  "${FAKE_CODEX}" -o "${out}/reports/inbox/renamed.md"
run_task "${out}"
find "${out}/reports" -maxdepth 1 -type f -name '*.md' -print0 | sort -z | \
  xargs -0 sha256sum >"${TEST_ROOT}/after-repeat.sha"
cmp "${TEST_ROOT}/before-repeat.sha" "${TEST_ROOT}/after-repeat.sha" || \
  fail 'replayed report changed the published library'
[[ ! -e "${out}/reports/inbox/renamed.md" ]] || fail 'replayed input was not consumed'

for mode in fail invalid-output; do
  failure_out="${TEST_ROOT}/${mode}"
  build_library "${failure_out}"
  AGENT_INTERVIEW_RUN_ID=20260724-080000 \
    AGENT_INTERVIEW_REPORT_DATE=2026-07-24 \
    "${FAKE_CODEX}" -o "${failure_out}/reports/inbox/input.md"
  find "${failure_out}/reports" -maxdepth 1 -type f -name '*.md' -print0 | sort -z | \
    xargs -0 sha256sum >"${TEST_ROOT}/${mode}-before.sha"
  if run_task "${failure_out}" "${mode}" >"${TEST_ROOT}/${mode}.out" 2>&1; then
    fail "daily task accepted ${mode} fusion"
  fi
  find "${failure_out}/reports" -maxdepth 1 -type f -name '*.md' -print0 | sort -z | \
    xargs -0 sha256sum >"${TEST_ROOT}/${mode}-after.sha"
  cmp "${TEST_ROOT}/${mode}-before.sha" "${TEST_ROOT}/${mode}-after.sha" || \
    fail "${mode} fusion changed the published library"
  [[ -f "${failure_out}/reports/inbox/input.md" ]] || fail "${mode} fusion consumed its input"
done

conflict_out="${TEST_ROOT}/source-conflict"
build_library "${conflict_out}"
sed -i "/^## 融合状态$/i\\
| 20260724-080000 | 2026-07-24 | aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa | 1 | 1 | 0 | 0 | 0 |\\
" "${conflict_out}/reports/README.md"
AGENT_INTERVIEW_RUN_ID=20260724-080000 \
  AGENT_INTERVIEW_REPORT_DATE=2026-07-24 \
  "${FAKE_CODEX}" -o "${conflict_out}/reports/inbox/conflict.md"
if run_task "${conflict_out}" >"${TEST_ROOT}/source-conflict.out" 2>&1; then
  fail 'daily task accepted the same source identity with a different payload hash'
fi
grep -Fq 'source identity conflict' "${TEST_ROOT}/source-conflict.out" || \
  fail 'source identity conflict did not produce an explicit error'
[[ -f "${conflict_out}/reports/inbox/conflict.md" ]] || \
  fail 'source identity conflict consumed its input'

echo 'daily agent interview fusion tests passed'
