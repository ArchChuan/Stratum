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

sed -i "/^## Stratum 可补强点$/i\\
### Q-1-generated 新问题\\
\\
- 作答要点：新答案。\\
- 深挖问题：如何验证？\\
- Stratum 实现与边界：测试融合。\\
- 相关源码/文档：\`docs/agent/agent.md\`\\
- 来源：SRC-new\\
- 首次收录：${report_date}\\
- 最近更新：${report_date}\\
" "${target}"
sed -i "/^## 参考来源$/a\\
\\
### SRC-new\\
\\
- URL: https://example.com/new\\
- 标题: New Source\\
- 类型: official\\
- 首次收录: ${report_date}\\
- 最近核验: ${report_date}" "${target}"
sed -i "/^## 融合状态$/i\\
| ${run_id} | ${report_date} | ${hash} | 1 | 1 | 0 | 0 | 0 |\\
" "${library}/README.md"
sed -i 's/^- 原始条目数：[0-9][0-9]*$/- 原始条目数：2/' "${library}/README.md"
sed -i 's/^- 稳定条目数：[0-9][0-9]*$/- 稳定条目数：11/' "${library}/README.md"
printf '%s|%s:Q1|Q-1-generated\n' "$(basename "${input}")" "${run_id}" >"${coverage}"

