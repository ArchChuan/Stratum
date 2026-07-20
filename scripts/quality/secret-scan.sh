#!/usr/bin/env bash

set -euo pipefail

STRATUM_ROOT="${STRATUM_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
OUT_DIR="${SECRET_SCAN_OUT_DIR:-${STRATUM_ROOT}/tmp/secret-scan}"
LOG_DIR="${OUT_DIR}/logs"
REPORT_DIR="${OUT_DIR}/reports"
LOCK_FILE="${SECRET_SCAN_LOCK_FILE:-${OUT_DIR}/secret-scan.lock}"
TODAY="$(date +%F)"
RUN_ID="$(date '+%Y%m%d-%H%M%S')"
LOG_FILE="${LOG_DIR}/${TODAY}.log"
REPORT_FILE="${REPORT_DIR}/${RUN_ID}.md"
LATEST_REPORT="${REPORT_DIR}/latest.md"
JSON_FILE="${REPORT_DIR}/${RUN_ID}.json"
DRY_RUN="${SECRET_SCAN_DRY_RUN:-0}"
GITLEAKS_BIN="${GITLEAKS_BIN:-gitleaks}"
SCAN_HISTORY="${SECRET_SCAN_HISTORY:-0}"
SCAN_ROOT=''

mkdir -p "${LOG_DIR}" "${REPORT_DIR}"

cleanup() {
  if [[ -n "${SCAN_ROOT}" && -d "${SCAN_ROOT}" ]]; then
    rm -rf -- "${SCAN_ROOT}"
  fi
}
trap cleanup EXIT

log() { printf '[%s] %s\n' "$(date '+%F %T%z')" "$*" | tee -a "${LOG_FILE}"; }

cd "${STRATUM_ROOT}"
exec 9>"${LOCK_FILE}"
if ! flock -n 9; then
  log 'another secret scan is already running; skipping'
  exit 0
fi

if [[ "${DRY_RUN}" == "1" ]]; then
  command -v "${GITLEAKS_BIN}" >/dev/null
  "${GITLEAKS_BIN}" version >> "${LOG_FILE}" 2>&1
  log 'dry run complete'
  exit 0
fi
if ! command -v "${GITLEAKS_BIN}" >/dev/null 2>&1; then
  log 'gitleaks not found'
  exit 1
fi

GITLEAKS_EXTRA_ARGS=()
if [[ "${SCAN_HISTORY}" == "1" ]]; then
  MODE='git'
  SOURCE="${STRATUM_ROOT}"
else
  MODE='tracked-worktree'
  SCAN_ROOT="$(mktemp -d)"
  while IFS= read -r -d '' path; do
    if [[ ! -f "${path}" && ! -L "${path}" ]]; then
      continue
    fi
    mkdir -p "${SCAN_ROOT}/$(dirname "${path}")"
    cp -P -- "${path}" "${SCAN_ROOT}/${path}"
  done < <(git ls-files -z --cached)
  SOURCE="${SCAN_ROOT}"
  GITLEAKS_EXTRA_ARGS=(--no-git)
fi

log "starting secret scan (mode=${MODE})"
set +e
"${GITLEAKS_BIN}" detect \
  --source "${SOURCE}" \
  "${GITLEAKS_EXTRA_ARGS[@]}" \
  --redact \
  --report-format json \
  --report-path "${JSON_FILE}" \
  --no-banner >> "${LOG_FILE}" 2>&1
GITLEAKS_STATUS=$?
set -e

LEAK_COUNT=0
if [[ -f "${JSON_FILE}" ]]; then
  LEAK_COUNT="$(grep -c '"RuleID"' "${JSON_FILE}" 2>/dev/null || true)"
fi

{
  printf '# Stratum Secret Scan\n\n'
  printf -- '- 生成时间: %s\n' "$(date '+%F %T%z')"
  printf -- '- 扫描模式: %s\n' "${MODE}"
  printf -- '- gitleaks 退出码: %s\n' "${GITLEAKS_STATUS}"
  printf -- '- 发现疑似密钥条数: %s\n\n' "${LEAK_COUNT}"
  if [[ "${LEAK_COUNT}" -gt 0 ]]; then
    printf '## 结论: 发现疑似密钥，请按脱敏 JSON 中的文件与行号核查\n'
  else
    printf '## 结论: 未发现泄露\n'
  fi
} > "${REPORT_FILE}"

ln -sfn "${REPORT_FILE}" "${LATEST_REPORT}"
log "secret scan complete (leaks=${LEAK_COUNT})"

if [[ "${GITLEAKS_STATUS}" -gt 1 ]]; then
  exit "${GITLEAKS_STATUS}"
fi
