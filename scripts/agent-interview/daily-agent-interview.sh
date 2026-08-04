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
AGENT_INTERVIEW_BIN="${AGENT_INTERVIEW_BIN:-claude}"
AGENT_INTERVIEW_MODEL="${AGENT_INTERVIEW_MODEL:-deepseek-v4-pro}"
TIMEOUT_SEC="${AGENT_INTERVIEW_TIMEOUT_SEC:-3600}"
RUN_ID="${AGENT_INTERVIEW_RUN_ID:-$(date '+%Y%m%d-%H%M%S')}"
REPORT_DATE="${AGENT_INTERVIEW_REPORT_DATE:-$(date +%F)}"
REPO_DIGEST=''
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

run_researcher() {
  local output="$1" prompt="$2"
  local -a args=(-p --model "${AGENT_INTERVIEW_MODEL}" \
    --dangerously-skip-permissions --no-session-persistence)
  # claude CLI 无 --cwd/--output-file：子 shell 切工作目录，stdout 重定向写报告，
  # 输出路径经 env 透传给 fake 测试替身（AGENT_INTERVIEW_OUTPUT_FILE）。
  if [[ -n "${output}" ]]; then
    (
      cd "${STRATUM_ROOT}"
      AGENT_INTERVIEW_OUTPUT_FILE="${output}" \
        timeout "${TIMEOUT_SEC}" "${AGENT_INTERVIEW_BIN}" "${args[@]}" \
        "${prompt}" >"${output}"
    )
  else
    (
      cd "${STRATUM_ROOT}"
      timeout "${TIMEOUT_SEC}" "${AGENT_INTERVIEW_BIN}" "${args[@]}" "${prompt}"
    )
  fi
}

repo_change_digest() {
  local since_days="${AGENT_INTERVIEW_REPO_SINCE_DAYS:-14}" digest="${OUT_DIR}/repo-digest-${RUN_ID}.md"
  {
    printf '# Stratum 最近 %s 天仓库变更摘要（全量重构输入）\n\n' "${since_days}"
    printf '## 提交\n\n'
    if git -C "${STRATUM_ROOT}" log \
      --since="${since_days} days ago" --no-merges \
      --pretty=format:'- %h %ad %s' --date=short 2>/dev/null | head -200; then
      :
    else
      printf -- '- git log 失败：仓库变更摘要不可用\n'
    fi
    printf '\n\n## 变更文件\n\n'
    if git -C "${STRATUM_ROOT}" log --since="${since_days} days ago" \
      --name-only --pretty=format: 2>/dev/null | sort -u | head -100; then
      :
    else
      printf -- '- 变更文件列表不可用\n'
    fi
  } >"${digest}" || {
    printf -- '- 仓库变更摘要生成失败\n' >"${digest}"
    log "WARN: repo change digest generation failed: ${digest}"
  }
  REPO_DIGEST="${digest}"
  log "repo change digest: ${digest}"
}

generation_prompt() {
  cat <<EOF
You are the unattended daily Agent interview researcher for the Stratum repository.

Write a Chinese report with 12-20 senior/staff-level questions. Research the latest hot and frontier topics from current public sources, and ground every answer in the CURRENT repository implementation and documentation. Read the repository change digest at ${REPO_DIGEST} when present; verify every cited source path against the current code and do not cite moved or removed paths. Preserve source URLs, trends, Stratum gaps, follow-up questions, source paths, and tracking keywords. Do not print credentials, private URLs, tokens, API keys, or raw secrets.

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
Fully reconstruct the staged long-lived library by fusing one new Agent interview report, the existing library, and the current repository evidence.

Stage directory: ${AGENT_INTERVIEW_STAGE_LIBRARY}
Input report: ${input}
Input SHA-256: ${hash}
Repository change digest: ${REPO_DIGEST}
Coverage manifest to create: ${AGENT_INTERVIEW_COVERAGE_MANIFEST}

This is a FULL RECONSTRUCTION, not an append. Rewrite every category file completely so each article is one coherent, merged, deduplicated whole:

- Integrate the new report's content INTO the canonical entries instead of adding anything to the end of an article. Do not leave new questions, trends, gaps, keywords or sources as an appended block at the bottom of any file.
- Merge complementary evidence into the existing canonical entry and update its 最近更新 date. Create a new stable ID only for genuinely new topics.
- Deduplicate by normalized topic and semantics across the whole library, not title spelling alone. Each topic appears once in exactly one primary category; remove or condense superseded entries while keeping their provenance (source IDs/URLs, source paths, keywords) when still relevant.
- Keep stable IDs unchanged for topics that survive; do not renumber or recreate existing IDs.
- Reconcile every Stratum-grounded claim against the repository change digest: cite current code and documentation paths, and drop or explicitly mark claims that are stale or conflicted. A newer report date alone is not evidence for overwriting existing claims.
- Preserve all source links, trends, questions and answers, Stratum gaps, follow-up questions, source paths, and tracking keywords; merge keywords and sources as normalized sets.

Use exactly this article structure in every category file (do not create new category files):
## 0. 分类边界与融合说明
## 1. 项目关键知识
## 2. 流程与架构图
## 3. 热门面试题与结合项目的答案
## 4. 趋势与观点
## 5. Stratum 可补强点
## 6. 跟踪关键词
## 7. 参考来源

The README is the machine classification contract. Assign exactly one primary category to each item. Put content that cannot be classified confidently in 99-unclassified.md. Update the README: append one processed-report ledger row containing run ID, report date, SHA-256, input count, created count, updated count, duplicate count, and unclassified count; update fusion statistics and dates; add a line to the 稳定 ID 与去重规则 section stating that every fusion fully reconstructs and deduplicates all articles. Create the coverage manifest with one pipe-delimited row per source question:
<input-basename>|<run-id>:Q<original-ordinal>|<stable-id>

00-question-bank-index.md is a by-question-number index, not an article: keep its 分类速览/按题号索引 tables, refresh the 维护说明 timestamp, and update the question-to-document mapping when questions move between categories. Do not apply the 0-7 article structure to it.

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

  if run_researcher '' "$(fusion_prompt "${input}" "${hash}")"; then
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

if [[ "${MODE}" == generate-and-fuse || "${MODE}" == fuse-only ]]; then
  repo_change_digest
fi

case "${MODE}" in
  dry-run)
    command -v "${AGENT_INTERVIEW_BIN}" >/dev/null
    log 'dry run complete'
    exit 0
    ;;
  validate-only)
    log 'library validation complete'
    exit 0
    ;;
  generate-and-fuse)
    generated="${INBOX_DIR}/${RUN_ID}.md"
    run_researcher "${generated}" "$(generation_prompt)"
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
