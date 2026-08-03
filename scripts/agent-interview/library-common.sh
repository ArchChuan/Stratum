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

# 固定文件集合：README + 题库索引 + 10 个分类文章。
# 00-question-bank-index.md 是按题号索引（非文章），只入白名单，
# 不参与章节/稳定 ID/待分类计数校验（CATEGORY_FILES 才是文章集合）。
readonly AGENT_INTERVIEW_LIBRARY_FILES=(
  README.md
  00-question-bank-index.md
  "${AGENT_INTERVIEW_CATEGORY_FILES[@]}"
)

# 语义章节关键词：每个分类文件必须恰好包含一个含该关键词的标题行。
# 兼容旧版式（## 面试题）与全量重构版式（## 3. 热门面试题与结合项目的答案）。
readonly AGENT_INTERVIEW_REQUIRED_HEADINGS=(
  '分类边界'
  '趋势与观点'
  '面试题'
  'Stratum 可补强点'
  '跟踪关键词'
  '参考来源'
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

