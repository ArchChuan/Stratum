#!/usr/bin/env bash

set -euo pipefail

label="${1:?missing check label}"
shift

printf '%s|%s\n' "${label}" "$*" >> "${RISK_GUARD_COMMAND_LOG:?missing command log}"

if [[ "${label}" == "${RISK_GUARD_FAIL_LABEL:-}" ]]; then
  exit 42
fi
