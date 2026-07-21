#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMMON="${ROOT}/docs/agent/instructions.md"
PREFIXES=(
  "${ROOT}/docs/agent/templates/agents-prefix.md"
  "${ROOT}/docs/agent/templates/claude-prefix.md"
)
TARGETS=("${ROOT}/AGENTS.md" "${ROOT}/CLAUDE.md")
NAMES=("AGENTS.md" "CLAUDE.md")

usage() {
  echo 'usage: generate-agent-instructions.sh [--check]' >&2
}

case "$#:${1:-}" in
  0:|1:--check) ;;
  *)
    usage
    exit 2
    ;;
esac

for input in "${COMMON}" "${PREFIXES[@]}"; do
  if [[ ! -r "${input}" ]]; then
    echo "agent instructions: required input is not readable: ${input#"${ROOT}/"}" >&2
    exit 1
  fi
done

TEMP_DIR="$(mktemp -d "${ROOT}/.agent-instructions.XXXXXX")"
INSTALL_TEMP=''

cleanup() {
  [[ -z "${INSTALL_TEMP}" ]] || rm -f "${INSTALL_TEMP}"
  rm -rf "${TEMP_DIR}"
}

trap cleanup EXIT

render() {
  local prefix="$1"
  local output="$2"
  local relative_prefix="${prefix#"${ROOT}/"}"

  {
    printf '%s\n' '<!-- generated; do not edit directly -->'
    printf '<!-- source: docs/agent/instructions.md + %s -->\n\n' "${relative_prefix}"
    cat "${prefix}"
    printf '\n\n---\n\n'
    cat "${COMMON}"
  } >"${output}"
}

RENDERED=("${TEMP_DIR}/AGENTS.md" "${TEMP_DIR}/CLAUDE.md")
CHANGED=(0 0)

for index in "${!TARGETS[@]}"; do
  render "${PREFIXES[index]}" "${RENDERED[index]}"
  [[ -r "${RENDERED[index]}" ]] || {
    echo "agent instructions: failed to render ${NAMES[index]}" >&2
    exit 1
  }
  if [[ ! -f "${TARGETS[index]}" ]] || ! cmp -s "${RENDERED[index]}" "${TARGETS[index]}"; then
    CHANGED[index]=1
  fi
done

if [[ "${1:-}" == "--check" ]]; then
  stale=0
  for index in "${!TARGETS[@]}"; do
    if (( CHANGED[index] )); then
      echo "agent instructions: stale generated entry ${NAMES[index]}"
      stale=1
    fi
  done
  if (( stale )); then
    echo 'run: make agent-instructions'
    exit 1
  fi
  echo 'agent instructions: generated entries are current'
  exit 0
fi

if (( ! CHANGED[0] && ! CHANGED[1] )); then
  echo 'agent instructions: no changes'
  exit 0
fi

BACKUPS=("${TEMP_DIR}/AGENTS.md.backup" "${TEMP_DIR}/CLAUDE.md.backup")
EXISTED=(0 0)
INSTALLED=()

for index in "${!TARGETS[@]}"; do
  if (( CHANGED[index] )) && [[ -e "${TARGETS[index]}" ]]; then
    cp -p "${TARGETS[index]}" "${BACKUPS[index]}"
    EXISTED[index]=1
  fi
done

rollback() {
  local failed=0
  local installed_index index rollback_temp

  for (( installed_index=${#INSTALLED[@]} - 1; installed_index >= 0; installed_index-- )); do
    index="${INSTALLED[installed_index]}"
    if (( EXISTED[index] )); then
      rollback_temp="${ROOT}/.${NAMES[index]}.rollback.$$"
      if ! cp -p "${BACKUPS[index]}" "${rollback_temp}" ||
        ! mv -f "${rollback_temp}" "${TARGETS[index]}"; then
        rm -f "${rollback_temp}"
        echo "agent instructions: rollback failed for ${NAMES[index]}" >&2
        failed=1
      fi
    elif ! rm -f "${TARGETS[index]}"; then
      echo "agent instructions: rollback failed for ${NAMES[index]}" >&2
      failed=1
    fi
  done
  return "${failed}"
}

for index in "${!TARGETS[@]}"; do
  (( CHANGED[index] )) || continue
  INSTALL_TEMP="${ROOT}/.${NAMES[index]}.install.$$"
  if ! cp "${RENDERED[index]}" "${INSTALL_TEMP}" ||
    ! mv -f "${INSTALL_TEMP}" "${TARGETS[index]}"; then
    rm -f "${INSTALL_TEMP}"
    INSTALL_TEMP=''
    echo "agent instructions: failed to install ${NAMES[index]}" >&2
    if ! rollback; then
      echo 'agent instructions: install failed and rollback was incomplete' >&2
    fi
    exit 1
  fi
  INSTALL_TEMP=''
  INSTALLED+=("${index}")
done

for index in "${!TARGETS[@]}"; do
  if (( CHANGED[index] )); then
    echo "agent instructions: generated ${NAMES[index]}"
  fi
done
