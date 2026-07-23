#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=library-common.sh
source "${SCRIPT_DIR}/library-common.sh"

usage() {
  echo "Usage: $0 --library <dir> [--inbox <dir>] [--coverage-manifest <file>]" >&2
  exit 2
}

fail() {
  echo "agent interview library validation failed: $*" >&2
  exit 1
}

library=''
inbox=''
coverage_manifest=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --library)
      [[ $# -ge 2 ]] || usage
      library="$2"
      shift 2
      ;;
    --inbox)
      [[ $# -ge 2 ]] || usage
      inbox="$2"
      shift 2
      ;;
    --coverage-manifest)
      [[ $# -ge 2 ]] || usage
      coverage_manifest="$2"
      shift 2
      ;;
    *) usage ;;
  esac
done

[[ -n "${library}" ]] || usage
[[ -d "${library}" ]] || fail "library directory does not exist: ${library}"
[[ -z "${inbox}" || -d "${inbox}" ]] || fail "inbox directory does not exist: ${inbox}"
[[ -z "${coverage_manifest}" || -f "${coverage_manifest}" ]] || \
  fail "coverage manifest does not exist: ${coverage_manifest}"

for expected in "${AGENT_INTERVIEW_LIBRARY_FILES[@]}"; do
  [[ -f "${library}/${expected}" && ! -L "${library}/${expected}" ]] || \
    fail "missing library file: ${expected}"
done

while IFS= read -r path; do
  name="${path##*/}"
  agent_interview_is_library_file "${name}" || fail "unexpected Markdown file: ${name}"
done < <(find "${library}" -maxdepth 1 -type f -name '*.md' -print | sort)

while IFS= read -r path; do
  name="${path##*/}"
  if [[ "${name}" != latest.md || "$(readlink "${path}")" != README.md ]]; then
    fail "unexpected symlink: ${name}"
  fi
done < <(find "${library}" -maxdepth 1 -type l -print | sort)

for category in "${AGENT_INTERVIEW_CATEGORY_FILES[@]}"; do
  for heading in "${AGENT_INTERVIEW_REQUIRED_HEADINGS[@]}"; do
    count="$(grep -Fxc "${heading}" "${library}/${category}" || true)"
    [[ "${count}" -eq 1 ]] || fail "missing required heading '${heading}' in ${category}"
  done
done

ids_file="$(mktemp)"
trap 'rm -f "${ids_file}"' EXIT
grep -hE '^### (Q|T|G)-[a-zA-Z0-9][a-zA-Z0-9-]* ' \
  "${AGENT_INTERVIEW_CATEGORY_FILES[@]/#/${library}/}" | awk '{print $2}' | sort >"${ids_file}"
duplicate_id="$(uniq -d "${ids_file}" | head -1)"
[[ -z "${duplicate_id}" ]] || fail "duplicate stable ID: ${duplicate_id}"

ledger_rows="$(grep -E '^\| [^|]+ \| [0-9]{4}-[0-9]{2}-[0-9]{2} \| [0-9a-f]{64} \|' \
  "${library}/README.md" || true)"
while IFS= read -r row; do
  [[ -z "${row}" ]] && continue
  hash="$(awk -F'|' '{gsub(/ /, "", $4); print $4}' <<<"${row}")"
  [[ "${hash}" =~ ^[0-9a-f]{64}$ ]] || fail "invalid processed report SHA-256: ${hash}"
done <<<"${ledger_rows}"

declared_unclassified="$(sed -n 's/^- 待分类条目数：\([0-9][0-9]*\)$/\1/p' "${library}/README.md")"
[[ "${declared_unclassified}" =~ ^[0-9]+$ ]] || fail 'missing unclassified count in README.md'
actual_unclassified="$(grep -Ec '^### (Q|T|G)-' "${library}/99-unclassified.md" || true)"
[[ "${declared_unclassified}" -eq "${actual_unclassified}" ]] || \
  fail "unclassified count mismatch: declared=${declared_unclassified} actual=${actual_unclassified}"

if [[ -n "${coverage_manifest}" ]]; then
  while IFS='|' read -r source source_id stable_id extra; do
    [[ -n "${source}" && -n "${source_id}" && -n "${stable_id}" && -z "${extra}" ]] || \
      fail "invalid coverage row: ${source}|${source_id}|${stable_id}${extra:+|${extra}}"
    grep -Fxq "${stable_id}" "${ids_file}" || \
      fail "coverage references unknown stable ID: ${stable_id}"
  done <"${coverage_manifest}"
fi

echo "agent interview library validation passed"
