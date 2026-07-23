#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -f "${SCRIPT_DIR}/library-common.sh" ]]; then
  HELPER_DIR="${SCRIPT_DIR}"
else
  STRATUM_ROOT="${STRATUM_ROOT:-$(cd "${SCRIPT_DIR}/../.." && pwd)}"
  HELPER_DIR="${STRATUM_ROOT}/scripts/agent-interview"
fi
# shellcheck source=library-common.sh
source "${HELPER_DIR}/library-common.sh"

STRATUM_ROOT="${STRATUM_ROOT:-$(agent_interview_root)}"
OUT_DIR="${AGENT_INTERVIEW_OUT_DIR:-${STRATUM_ROOT}/tmp/agent-interview}"
REPORT_DIR="${OUT_DIR}/reports"
INBOX_DIR="${REPORT_DIR}/inbox"
LOG_DIR="${OUT_DIR}/logs"
LOCK_FILE="${AGENT_INTERVIEW_LOCK_FILE:-${OUT_DIR}/agent-interview.lock}"
CODEX_BIN="${CODEX_BIN:-codex}"
CODEX_MODEL="${AGENT_INTERVIEW_CODEX_MODEL:-}"
TIMEOUT_SEC="${AGENT_INTERVIEW_TIMEOUT_SEC:-3600}"
RUN_ID="${AGENT_INTERVIEW_RUN_ID:-$(date '+%Y%m%d-%H%M%S')}"
REPORT_DATE="${AGENT_INTERVIEW_REPORT_DATE:-$(date +%F)}"
MODE=generate-and-fuse

usage() {
  echo "Usage: $0 [--generate-and-fuse|--fuse-only|--validate-only|--dry-run]" >&2
  exit 2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --generate-and-fuse) MODE=generate-and-fuse ;;
    --fuse-only) MODE=fuse-only ;;
    --validate-only) MODE=validate-only ;;
    --dry-run) MODE=dry-run ;;
    *) usage ;;
  esac
  shift
done

mkdir -p "${LOG_DIR}" "${INBOX_DIR}"
LOG_FILE="${LOG_DIR}/${REPORT_DATE}.log"

log() {
  printf '[%s] %s\n' "$(date '+%F %T%z')" "$*" | tee -a "${LOG_FILE}"
}

run_codex() {
  local output="$1" prompt="$2"
  local -a args=(-a never)
  [[ -n "${CODEX_MODEL}" ]] && args+=(-m "${CODEX_MODEL}")
  args+=(exec -C "${STRATUM_ROOT}")
  [[ -n "${output}" ]] && args+=(-o "${output}")
  timeout "${TIMEOUT_SEC}" "${CODEX_BIN}" "${args[@]}" "${prompt}"
}

generation_prompt() {
  cat <<EOF
You are the unattended daily Agent interview researcher for the Stratum repository.

Write a Chinese report with 12-20 senior/staff-level questions. Research current public sources and ground answers in current repository code and documentation. Preserve source URLs, trends, Stratum gaps, follow-up questions, source paths, and tracking keywords. Do not print credentials, private URLs, tokens, API keys, or raw secrets.

Use exactly these top-level sections:
# Agent 高级开发岗位每日面试题
## 输入元数据
- run_id: ${RUN_ID}
- report_date: ${REPORT_DATE}
## 日期与来源
## 热门趋势摘要
## 面试题与项目化作答
## stratum 可补强点
## 明日跟踪关键词

For every question include why it is current, the Stratum-grounded answer, follow-up questions, and relevant source or documentation paths. Research only; do not modify repository files.
EOF
}

fusion_prompt() {
  local input="$1" hash="$2"
  cat <<EOF
Fuse one new Agent interview report into the staged long-lived library.

Stage directory: ${AGENT_INTERVIEW_STAGE_LIBRARY}
Input report: ${input}
Input SHA-256: ${hash}
Coverage manifest to create: ${AGENT_INTERVIEW_COVERAGE_MANIFEST}

The README is the machine classification contract. Modify files only inside the stage directory. Do not create category files. Preserve all source links, trends, questions and answers, Stratum gaps, follow-up questions, source paths, and keywords. Assign exactly one primary category to each item. Put content that cannot be classified confidently in 99-unclassified.md.

Deduplicate by normalized topic and semantics, not title spelling alone. Merge complementary evidence into the canonical entry. Do not silently overwrite conflicts or definition differences; retain the boundary or mark it pending review. A newer date alone is not evidence. Keep stable IDs unchanged for existing topics.

Append one processed-report ledger row containing run ID, report date, SHA-256, input count, created count, updated count, duplicate count, and unclassified count. Update fusion statistics and dates. Create the coverage manifest with one pipe-delimited row per source question:
<input-basename>|<run-id>:Q<original-ordinal>|<stable-id>

Do not delete the input. Deterministic code validates and publishes the result.
EOF
}

extract_metadata() {
  local input="$1"
  input_run_id="$(sed -n 's/^- run_id:[[:space:]]*//p' "${input}" | head -1)"
  input_report_date="$(sed -n 's/^- report_date:[[:space:]]*//p' "${input}" | head -1)"
  [[ "${input_run_id}" =~ ^[0-9]{8}-[0-9]{6}$ ]] || input_run_id="${RUN_ID}"
  [[ "${input_report_date}" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || input_report_date="${REPORT_DATE}"
}

copy_library_to_stage() {
  local stage_library="$1" file
  mkdir -p "${stage_library}"
  for file in "${AGENT_INTERVIEW_LIBRARY_FILES[@]}"; do
    cp "${REPORT_DIR}/${file}" "${stage_library}/${file}"
  done
  ln -s README.md "${stage_library}/latest.md"
}

publish_stage() {
  local stage_library="$1" consumed_name="$2" backup="${OUT_DIR}/.reports-backup-${RUN_ID}"
  local path name
  [[ ! -e "${backup}" ]] || {
    log "publication backup already exists: ${backup}"
    return 1
  }
  mv "${REPORT_DIR}" "${backup}"
  if ! mv "${stage_library}" "${REPORT_DIR}"; then
    mv "${backup}" "${REPORT_DIR}"
    log 'failed to publish staged library; restored previous library'
    return 1
  fi
  mkdir -p "${REPORT_DIR}/inbox"
  if [[ -d "${backup}/inbox" ]]; then
    while IFS= read -r path; do
      name="${path##*/}"
      [[ "${name}" == "${consumed_name}" ]] && continue
      cp "${path}" "${REPORT_DIR}/inbox/${name}"
    done < <(find "${backup}/inbox" -maxdepth 1 -type f -name '*.md' -print | sort)
  fi
  rm -rf "${backup}"
}

fuse_input() {
  local input="$1" hash stage_root stage_library coverage existing_row existing_hash
  hash="$(agent_interview_sha256 "${input}")"
  extract_metadata "${input}"

  if grep -Fq "| ${hash} |" "${REPORT_DIR}/README.md"; then
    log "already processed input hash; consuming duplicate file $(basename "${input}")"
    rm -f "${input}"
    return 0
  fi
  existing_row="$(grep -F "| ${input_run_id} |" "${REPORT_DIR}/README.md" | head -1 || true)"
  if [[ -n "${existing_row}" ]]; then
    existing_hash="$(awk -F'|' '{gsub(/ /, "", $4); print $4}' <<<"${existing_row}")"
    log "source identity conflict for run ${input_run_id}: existing hash ${existing_hash}, new hash ${hash}"
    return 1
  fi

  stage_root="$(mktemp -d "${OUT_DIR}/.fusion.XXXXXX")"
  stage_library="${stage_root}/library"
  coverage="${stage_root}/coverage.tsv"
  copy_library_to_stage "${stage_library}"

  export AGENT_INTERVIEW_STAGE_LIBRARY="${stage_library}"
  export AGENT_INTERVIEW_INPUT_REPORT="${input}"
  export AGENT_INTERVIEW_INPUT_HASH="${hash}"
  export AGENT_INTERVIEW_COVERAGE_MANIFEST="${coverage}"
  export AGENT_INTERVIEW_RUN_ID="${input_run_id}"
  export AGENT_INTERVIEW_REPORT_DATE="${input_report_date}"

  if run_codex '' "$(fusion_prompt "${input}" "${hash}")"; then
    :
  else
    status=$?
    rm -rf "${stage_root}"
    log "fusion failed for $(basename "${input}") with exit code ${status}"
    return "${status}"
  fi
  if ! "${HELPER_DIR}/validate-library.sh" --library "${stage_library}" \
    --coverage-manifest "${coverage}"; then
    rm -rf "${stage_root}"
    log "staged library validation failed for $(basename "${input}")"
    return 1
  fi
  grep -Fq "| ${hash} |" "${stage_library}/README.md" || {
    rm -rf "${stage_root}"
    log "staged library omitted processed hash for $(basename "${input}")"
    return 1
  }
  publish_stage "${stage_library}" "$(basename "${input}")" || {
    rm -rf "${stage_root}"
    return 1
  }
  rm -rf "${stage_root}"
  log "fused and consumed $(basename "${input}")"
}

exec 9>"${LOCK_FILE}"
if ! flock -n 9; then
  log 'another agent interview job is already running; skipping'
  exit 0
fi

"${HELPER_DIR}/validate-library.sh" --library "${REPORT_DIR}"

case "${MODE}" in
  dry-run)
    command -v "${CODEX_BIN}" >/dev/null
    log 'dry run complete'
    exit 0
    ;;
  validate-only)
    log 'library validation complete'
    exit 0
    ;;
  generate-and-fuse)
    generated="${INBOX_DIR}/${RUN_ID}.md"
    run_codex "${generated}" "$(generation_prompt)"
    [[ -s "${generated}" ]] || {
      log "research produced an empty report: ${generated}"
      exit 1
    }
    ;;
  fuse-only) ;;
esac

while IFS= read -r input; do
  fuse_input "${input}"
done < <(find "${INBOX_DIR}" -maxdepth 1 -type f -name '*.md' -print | sort)

log 'daily agent interview research and fusion complete'
