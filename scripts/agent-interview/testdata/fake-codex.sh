#!/usr/bin/env bash

set -euo pipefail

output=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o)
      output="$2"
      shift 2
      ;;
    *) shift ;;
  esac
done

# claude CLI 生成分支：stdout 重定向 + env 透传输出路径（保留 -o 兼容旧调用）。
if [[ -z "${output}" && -n "${AGENT_INTERVIEW_OUTPUT_FILE:-}" ]]; then
  output="${AGENT_INTERVIEW_OUTPUT_FILE}"
fi

if [[ -n "${output}" ]]; then
  cat >"${output}" <<EOF
# Agent 高级开发岗位每日面试题

## 输入元数据

- run_id: ${AGENT_INTERVIEW_RUN_ID:?}
- report_date: ${AGENT_INTERVIEW_REPORT_DATE:?}

## 日期与来源

- https://example.com/new

## 热门趋势摘要

- 新趋势。

## 面试题与项目化作答

### 1. 新问题

新答案。

## stratum 可补强点

- 新建议。

## 明日跟踪关键词

- new keyword
EOF
  exit 0
fi

case "${FAKE_CODEX_MODE:-success}" in
  fail) exit 42 ;;
  invalid-output)
    printf '# unexpected\n' >"${AGENT_INTERVIEW_STAGE_LIBRARY:?}/10-unexpected.md"
    exit 0
    ;;
  success) ;;
  *) echo "unknown fake mode: ${FAKE_CODEX_MODE}" >&2; exit 2 ;;
esac

library="${AGENT_INTERVIEW_STAGE_LIBRARY:?}"
input="${AGENT_INTERVIEW_INPUT_REPORT:?}"
hash="${AGENT_INTERVIEW_INPUT_HASH:?}"
coverage="${AGENT_INTERVIEW_COVERAGE_MANIFEST:?}"
run_id="${AGENT_INTERVIEW_RUN_ID:?}"
report_date="${AGENT_INTERVIEW_REPORT_DATE:?}"
target="${library}/01-agent-runtime-and-workflow.md"

# 模拟全量重构：整文件重写为 0-7 版式，既有稳定条目块逐字保留，
# 新报告内容融入对应章节，而不是追加到文件底部。
extract_section() {
  awk -v marker="$1" '
    /^#/ {sec = ($0 ~ /^### / && $0 ~ marker) ? 1 : 0}
    sec {print}
  ' "${target}"
}

{
  cat <<'HDR'
# Test Category（全量重构）

## 0. 分类边界与融合说明

全量重构：既有条目与输入报告融合去重后整体重写。

## 1. 项目关键知识

重建后的项目关键知识。

## 2. 流程与架构图

重建后的流程与架构图。

## 3. 热门面试题与结合项目的答案

HDR
  extract_section '^### Q-'
  cat <<'NEW'
### Q-1-generated 新问题

- 作答要点：新答案。
- 深挖问题：如何验证？
- Stratum 实现与边界：测试融合。
- 相关源码/文档：`docs/agent/agent.md`
- 来源：SRC-new
- 首次收录：@@REPORT_DATE@@
- 最近更新：@@REPORT_DATE@@

## 4. 趋势与观点

NEW
  extract_section '^### T-'
  cat <<'HDR2'
## 5. Stratum 可补强点

HDR2
  extract_section '^### G-'
  cat <<'HDR3'
## 6. 跟踪关键词

- base keyword
- new keyword

## 7. 参考来源

HDR3
  extract_section '^### SRC-'
  cat <<'SRCNEW'
### SRC-new

- URL: https://example.com/new
- 标题: New Source
- 类型: official
- 首次收录: @@REPORT_DATE@@
- 最近核验: @@REPORT_DATE@@
SRCNEW
} >"${target}.tmp"
sed -i "s/@@REPORT_DATE@@/${report_date}/g" "${target}.tmp"
mv "${target}.tmp" "${target}"

sed -i "/^## 融合状态$/i\\
| ${run_id} | ${report_date} | ${hash} | 1 | 1 | 0 | 0 | 0 |\\
" "${library}/README.md"
sed -i 's/^- 原始条目数：[0-9][0-9]*$/- 原始条目数：2/' "${library}/README.md"
sed -i 's/^- 稳定条目数：[0-9][0-9]*$/- 稳定条目数：11/' "${library}/README.md"
printf '%s|%s:Q1|Q-1-generated\n' "$(basename "${input}")" "${run_id}" >"${coverage}"
