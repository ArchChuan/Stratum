#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${AGENT_INTERVIEW_OUT_DIR:-${ROOT_DIR}/tmp/agent-interview}"
LOG_DIR="${OUT_DIR}/logs"
REPORT_DIR="${OUT_DIR}/reports"
LOCK_FILE="${TMPDIR:-/tmp}/stratum-agent-interview.lock"
TODAY="$(date +%F)"
RUN_ID="$(date '+%Y%m%d-%H%M%S')"
LOG_FILE="${LOG_DIR}/${TODAY}.log"
REPORT_FILE="${REPORT_DIR}/${RUN_ID}.md"
LATEST_REPORT="${REPORT_DIR}/latest.md"
DRY_RUN="${AGENT_INTERVIEW_DRY_RUN:-0}"
CODEX_BIN="${CODEX_BIN:-codex}"
CODEX_MODEL="${AGENT_INTERVIEW_CODEX_MODEL:-}"
TIMEOUT_SEC="${AGENT_INTERVIEW_TIMEOUT_SEC:-3600}"

mkdir -p "${LOG_DIR}" "${REPORT_DIR}"

log() {
  printf '[%s] %s\n' "$(date '+%F %T%z')" "$*" | tee -a "${LOG_FILE}"
}

run() {
  log "+ $*"
  "$@" 2>&1 | tee -a "${LOG_FILE}"
}

cd "${ROOT_DIR}"

exec 9>"${LOCK_FILE}"
if ! flock -n 9; then
  log "another agent interview job is already running; skipping"
  exit 0
fi

log "starting daily agent interview research"

if [[ "${DRY_RUN}" == "1" ]]; then
  run command -v git
  run command -v "${CODEX_BIN}"
  run test -d internal
  run test -d api
  run "${CODEX_BIN}" --version
  log "dry run complete"
  exit 0
fi

if ! command -v "${CODEX_BIN}" >/dev/null 2>&1; then
  log "codex command not found; skipping"
  exit 1
fi

PROMPT="$(cat <<'PROMPT'
You are running as an unattended daily interview-preparation researcher for the stratum repository.

Goal:
- Search the current public market for senior/staff AI Agent developer job requirements and interview topics.
- Extract hot interview questions that are relevant to advanced AI Agent engineering roles.
- Answer each question by grounding it in this repository's architecture, code, and trade-offs.

Repository context:
- The project is stratum: a multi-tenant AI Agent platform with Go backend, React frontend, DDD bounded contexts, PostgreSQL schema tenant isolation, NATS JetStream, Milvus, IAM/OAuth/JWT, LLM gateway, MCP tools, memory, knowledge/RAG, and ReAct-style agent execution.
- Read local code and docs as needed before answering.

Research requirements:
- Use web search/current sources. The market changes, so do not rely only on model memory.
- Prefer job descriptions, engineering blogs, official docs, and recent interview-prep material.
- Include source links for market/job/interview trend claims.
- Do not quote long copyrighted text; summarize.

Output requirements:
- Write in Chinese.
- Focus on senior/staff-level depth, not beginner definitions.
- Provide 12-20 high-value interview questions.
- For each question include:
  1. 题目
  2. 为什么热门
  3. 结合 stratum 的作答要点
  4. 可追问点
  5. 相关源码/文档路径
- Highlight gaps where stratum could improve, but do not modify files.
- Do not print secrets, tokens, API keys, private URLs, or raw credential values.

Output format:
# Agent 高级开发岗位每日面试题

## 日期与来源

## 热门趋势摘要

## 面试题与项目化作答

## stratum 可补强点

## 明日跟踪关键词
PROMPT
)"

CODEX_ARGS=(exec -C "${ROOT_DIR}" -o "${REPORT_FILE}")
if [[ -n "${CODEX_MODEL}" ]]; then
  CODEX_ARGS=(-m "${CODEX_MODEL}" "${CODEX_ARGS[@]}")
fi

set +e
timeout "${TIMEOUT_SEC}" "${CODEX_BIN}" -a never "${CODEX_ARGS[@]}" "${PROMPT}" 2>&1 | tee -a "${LOG_FILE}"
CODEX_STATUS="${PIPESTATUS[0]}"
set -e

if [[ "${CODEX_STATUS}" -ne 0 ]]; then
  log "daily agent interview research failed with exit code ${CODEX_STATUS}"
  exit "${CODEX_STATUS}"
fi

if [[ ! -s "${REPORT_FILE}" ]]; then
  log "daily agent interview research produced an empty report: ${REPORT_FILE}"
  exit 1
fi

ln -sfn "${REPORT_FILE}" "${LATEST_REPORT}"
log "agent interview report written to ${REPORT_FILE}"
log "latest agent interview report linked at ${LATEST_REPORT}"
log "daily agent interview research complete"
