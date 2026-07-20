#!/usr/bin/env bash

set -euo pipefail

source_dir=''
report_path=''
redacted=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --source) source_dir="$2"; shift 2 ;;
    --report-path) report_path="$2"; shift 2 ;;
    --redact) redacted=1; shift ;;
    *) shift ;;
  esac
done

if grep -R -q "${FAKE_GITLEAKS_SENTINEL}" "${source_dir}"; then
  if [[ "${redacted}" != 1 ]]; then
    printf '%s\n' "${FAKE_GITLEAKS_SENTINEL}"
  fi
  printf '[{"RuleID":"sentinel"}]\n' > "${report_path}"
  exit 1
fi
printf '[]\n' > "${report_path}"
