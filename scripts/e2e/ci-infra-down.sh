#!/usr/bin/env bash
# CI infra teardown for stateful E2E.
# Called via STATEFUL_E2E_INFRA_DOWN_COMMAND after tests complete.
set -euo pipefail

compose_file="${TMPDIR:-/tmp}/stratum-stateful-ci-infra.yml"
project="${STATEFUL_E2E_CI_PROJECT:-stratum-stateful-ci}"

if [[ -f "$compose_file" ]]; then
  docker compose -p "$project" -f "$compose_file" down -v --remove-orphans >/dev/null 2>&1 || true
  rm -f "$compose_file"
fi
