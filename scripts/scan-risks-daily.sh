#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${RISK_SCAN_OUT_DIR:-/tmp/stratum-risk-scan}"
LOG_DIR="${OUT_DIR}/logs"
REPORT_DIR="${OUT_DIR}/reports"
LOCK_FILE="${TMPDIR:-/tmp}/stratum-risk-scan.lock"
TODAY="$(date +%F)"
RUN_ID="$(date '+%Y%m%d-%H%M%S')"
LOG_FILE="${LOG_DIR}/${TODAY}.log"
REPORT_FILE="${REPORT_DIR}/${RUN_ID}.md"
LATEST_REPORT="${REPORT_DIR}/latest.md"
DRY_RUN="${RISK_SCAN_DRY_RUN:-0}"
CODEX_BIN="${CODEX_BIN:-codex}"
CODEX_MODEL="${RISK_SCAN_CODEX_MODEL:-}"
TIMEOUT_SEC="${RISK_SCAN_TIMEOUT_SEC:-3600}"

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
  log "another risk scan is already running; skipping"
  exit 0
fi

log "starting stratum risk scan"

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
You are running as an unattended daily risk scanner for the stratum repository.

Goal:
- Find functional bugs and non-functional risks in the current repository.
- Focus on security, tenant isolation, authorization, concurrency, resource leaks, data consistency,
  migration safety, error handling, observability gaps, deployment risks, and test coverage gaps.

Boundaries:
- Read and analyze the repository only.
- Do not modify files.
- Do not create commits, branches, issues, or pull requests.
- Do not print secrets, tokens, API keys, private URLs, or raw credential values.
- If you encounter sensitive values, report only the file path and the class of exposure.
- Follow AGENTS.md project rules when judging risks.

Scan guidance:
- Prioritize concrete, actionable findings over broad advice.
- Include exact file paths and line numbers where possible.
- Separate confirmed bugs from hypotheses.
- Prefer high-signal findings that could affect production correctness or safety.
- Consider Go backend, React frontend, Helm/Kubernetes, CI/CD, migrations, and scripts.

Output format:
# Stratum Risk Scan

## Executive Summary
- Date/time:
- Overall risk:
- Top findings count:

## Findings
For each finding:
- Severity: Critical | High | Medium | Low
- Category: Functional | Security | Concurrency | Data | Deployment | Testing | Observability
- Evidence: file path and line number when available
- Impact:
- Recommendation:

## Open Questions

## Suggested Verification Commands
PROMPT
)"

CODEX_ARGS=(exec -C "${ROOT_DIR}" -s read-only -o "${REPORT_FILE}")
if [[ -n "${CODEX_MODEL}" ]]; then
  CODEX_ARGS=(-m "${CODEX_MODEL}" "${CODEX_ARGS[@]}")
fi

set +e
timeout "${TIMEOUT_SEC}" "${CODEX_BIN}" -a never "${CODEX_ARGS[@]}" "${PROMPT}" 2>&1 | tee -a "${LOG_FILE}"
CODEX_STATUS="${PIPESTATUS[0]}"
set -e

if [[ "${CODEX_STATUS}" -ne 0 ]]; then
  log "codex risk scan failed with exit code ${CODEX_STATUS}"
  exit "${CODEX_STATUS}"
fi

if [[ ! -s "${REPORT_FILE}" ]]; then
  log "codex risk scan produced an empty report: ${REPORT_FILE}"
  exit 1
fi

ln -sfn "${REPORT_FILE}" "${LATEST_REPORT}"
log "risk scan report written to ${REPORT_FILE}"
log "latest risk scan report linked at ${LATEST_REPORT}"
log "stratum risk scan complete"
