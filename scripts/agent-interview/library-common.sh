#!/usr/bin/env bash

readonly AGENT_INTERVIEW_CATEGORY_FILES=(
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

readonly AGENT_INTERVIEW_LIBRARY_FILES=(
  README.md
  "${AGENT_INTERVIEW_CATEGORY_FILES[@]}"
)

readonly AGENT_INTERVIEW_REQUIRED_HEADINGS=(
  '## 分类边界'
  '## 趋势与观点'
  '## 面试题'
  '## Stratum 可补强点'
  '## 跟踪关键词'
  '## 参考来源'
)

agent_interview_root() {
  cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd
}

agent_interview_sha256() {
  sha256sum "$1" | awk '{print $1}'
}

agent_interview_is_library_file() {
  local candidate="$1" file
  for file in "${AGENT_INTERVIEW_LIBRARY_FILES[@]}"; do
    [[ "${candidate}" == "${file}" ]] && return 0
  done
  return 1
}

