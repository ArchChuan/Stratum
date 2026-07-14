#!/usr/bin/env bash
set -euo pipefail

acceptance_file="${STRATUM_E2E_ACCEPTANCE_FILE:-}"
if [[ -z "$acceptance_file" || ! -s "$acceptance_file" ]]; then
  echo "Stratum E2E acceptance contract is required via STRATUM_E2E_ACCEPTANCE_FILE" >&2
  exit 2
fi

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

if [[ ! -f .archon/e2e-runner.sh ]]; then
  echo "Project-specific E2E runner missing: .archon/e2e-runner.sh" >&2
  echo "Create it for the approved feature using the stratum-e2e-development skill; unit tests are not a substitute." >&2
  exit 2
fi

exec .archon/e2e-runner.sh "$acceptance_file"
