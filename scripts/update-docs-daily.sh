#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_DIR="${ROOT_DIR}/logs/docs-update"
LOCK_FILE="${TMPDIR:-/tmp}/stratum-docs-update.lock"
TODAY="$(date +%F)"
LOG_FILE="${LOG_DIR}/${TODAY}.log"
AUTO_COMMIT="${DOCS_UPDATE_AUTO_COMMIT:-0}"
DRY_RUN="${DOCS_UPDATE_DRY_RUN:-0}"
MAX_BUDGET_USD="${DOCS_UPDATE_MAX_BUDGET_USD:-3}"

mkdir -p "${LOG_DIR}"

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
  log "another docs update is already running; skipping"
  exit 0
fi

log "starting stratum docs update"

if [[ "${DRY_RUN}" == "1" ]]; then
  run command -v git
  run command -v claude
  run test -d docs
  run claude --version
  log "dry run complete"
  exit 0
fi

if ! command -v claude >/dev/null 2>&1; then
  log "claude command not found; skipping"
  exit 1
fi

if [[ -n "$(git status --porcelain -- docs)" ]]; then
  log "docs/ has uncommitted changes; continuing because unattended docs refresh may overwrite them"
  git status --short -- docs | tee -a "${LOG_FILE}"
fi

PROMPT="$(cat <<'PROMPT'
You are running as an unattended daily documentation maintainer for the stratum repository.

Goal:
- Update every documentation file under docs/ so it reflects the current repository state.
- Include docs/**/*.md and docs/**/*.html when they are stale.

Boundaries:
- Only modify files under docs/.
- Do not modify source code, config, tests, migrations, package files, or generated runtime artifacts.
- Do not commit changes.
- Do not record secrets, tokens, API keys, private URLs, or raw credential values.
- Follow AGENTS.md project rules and preserve existing architecture decisions unless the code clearly contradicts them.

Update rules:
- Read the current codebase, configuration, migrations, handlers, domain/application/infrastructure packages, web modules, deployment files, and existing docs as needed.
- Remove or correct stale claims.
- Add missing current facts when docs omit important behavior.
- Keep historical design docs only if they are clearly labeled as historical; otherwise make them describe current state.
- Preserve the existing language and tone of each document where practical.
- Keep changes focused on documentation freshness; do not rewrite for style alone.

Verification:
- Before finishing, summarize which docs changed and why.
PROMPT
)"

set +e
claude -p \
  --permission-mode acceptEdits \
  --max-budget-usd "${MAX_BUDGET_USD}" \
  "${PROMPT}" 2>&1 | tee -a "${LOG_FILE}"
CLAUDE_STATUS="${PIPESTATUS[0]}"
set -e

if [[ "${CLAUDE_STATUS}" -ne 0 ]]; then
  log "claude docs update failed with exit code ${CLAUDE_STATUS}"
  exit "${CLAUDE_STATUS}"
fi

if git diff --quiet -- docs; then
  log "docs are already up to date"
  exit 0
fi

log "docs changed:"
git diff --stat -- docs | tee -a "${LOG_FILE}"

if [[ "${AUTO_COMMIT}" == "1" ]]; then
  run git add docs
  run git commit -m "docs(agent): refresh generated documentation"
  log "docs update committed"
else
  log "auto commit disabled; leaving docs changes in working tree"
fi

log "stratum docs update complete"
