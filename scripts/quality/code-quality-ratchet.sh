#!/usr/bin/env bash

set -euo pipefail

ROOT="${CODE_QUALITY_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
ANALYZER="${ROOT}/scripts/quality/code-quality-ratchet.go"
BASELINE="${ROOT}/scripts/quality/code-quality-baseline.json"
MODE=check
ALL_MODE=0

case "${1:-}" in
  --refresh-baseline)
    MODE=refresh
    shift
    ;;
  --all)
    ALL_MODE=1
    shift
    ;;
esac

is_production_go() {
  local file="$1"
  [[ "${file}" == *.go ]] || return 1
  [[ "${file}" != *_test.go ]] || return 1
  [[ "${file}" != vendor/* && "${file}" != */vendor/* ]] || return 1
  [[ "${file}" != testdata/* && "${file}" != */testdata/* ]] || return 1
  [[ -f "${ROOT}/${file}" ]] || return 1
  ! head -n 8 "${ROOT}/${file}" | grep -Eq '^//go:build[[:space:]]+ignore([[:space:]]|$)' || return 1
  ! head -n 8 "${ROOT}/${file}" | grep -Eq '^// Code generated .* DO NOT EDIT\.$'
}

tracked_files=()
while IFS= read -r file; do
  [[ -n "${file}" ]] || continue
  if is_production_go "${file}"; then
    tracked_files+=("${file}")
  fi
done < <(git -C "${ROOT}" ls-files '*.go' | LC_ALL=C sort)

if [[ "${MODE}" == refresh ]]; then
  if [[ "$#" -ne 0 ]]; then
    echo '--refresh-baseline does not accept file arguments' >&2
    exit 2
  fi
  go run "${ANALYZER}" refresh --root "${ROOT}" --baseline "${BASELINE}" "${tracked_files[@]}"
  echo "refreshed ${BASELINE} from ${#tracked_files[@]} tracked production Go files"
  exit 0
fi

requested=()
if [[ "${ALL_MODE}" -eq 1 ]]; then
  requested=("${tracked_files[@]}")
elif [[ "$#" -gt 0 ]]; then
  requested=("$@")
else
  while IFS= read -r file; do
    requested+=("${file}")
  done < <(git -C "${ROOT}" diff --cached --name-only --diff-filter=ACMR)
fi

selected=()
for file in "${requested[@]}"; do
  file="${file#./}"
  if git -C "${ROOT}" ls-files --error-unmatch -- "${file}" >/dev/null 2>&1 && is_production_go "${file}"; then
    selected+=("${file}")
  fi
done

if [[ "${#selected[@]}" -eq 0 ]]; then
  echo 'code quality ratchet: no tracked production Go changes'
  exit 0
fi

go run "${ANALYZER}" check --root "${ROOT}" --baseline "${BASELINE}" "${selected[@]}"

production_loc="$(cat "${tracked_files[@]/#/${ROOT}/}" | wc -l)"
test_loc="$(git -C "${ROOT}" ls-files '*_test.go' | sed "s#^#${ROOT}/#" | xargs -r cat | wc -l)"
todo_count="$(rg -n 'TODO|FIXME' "${selected[@]/#/${ROOT}/}" 2>/dev/null | wc -l || true)"
printf 'quality trends: test/prod LOC=%s/%s TODO/FIXME(changed)=%s\n' "${test_loc}" "${production_loc}" "${todo_count}"

for file in "${selected[@]}"; do
  lines="$(wc -l <"${ROOT}/${file}")"
  if [[ "${lines}" -gt 800 ]]; then
    printf 'warning: file length %s lines: %s (target <=800)\n' "${lines}" "${file}" >&2
  fi
done

if command -v golangci-lint >/dev/null 2>&1; then
  duplicate_targets=()
  if [[ "${ALL_MODE}" -eq 1 ]]; then
    duplicate_targets=(./...)
  else
    while IFS= read -r directory; do
      duplicate_targets+=("./${directory}/...")
    done < <(printf '%s\n' "${selected[@]}" | xargs -n1 dirname | LC_ALL=C sort -u)
  fi
  duplicate_output="$(cd "${ROOT}" && golangci-lint run --no-config --enable-only=dupl --tests=false \
    --concurrency "$(bash scripts/quality/go-parallelism.sh)" \
    --max-issues-per-linter=0 --max-same-issues=0 "${duplicate_targets[@]}" 2>&1 || true)"
  if [[ -n "${duplicate_output}" && "${duplicate_output}" != '0 issues.' ]]; then
    echo 'warning: duplicate-code candidates (non-blocking):' >&2
    echo "${duplicate_output}" >&2
  fi
else
  echo 'warning: golangci-lint unavailable; duplicate scan skipped' >&2
fi
