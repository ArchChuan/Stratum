#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
harness="$script_dir/e2e-observation.sh"

if [[ -z "${MCP_GOVERNOR_GO:-}" ]]; then
  runtime_go=$(go env GOROOT)/bin/go
  if [[ -x "$runtime_go" ]]; then
    export MCP_GOVERNOR_GO="$runtime_go"
  fi
fi

if [[ ! -x "$harness" ]]; then
  printf 'observation E2E harness is missing or not executable: %s\n' "$harness" >&2
  exit 1
fi

output=$("$harness")
if rg -n 'command=.*session|--session.*session' "$harness" >/dev/null; then
  printf 'E2E harness appears to inject a session override\n' >&2
  exit 1
fi
for assertion in \
  'PASS rendered native configs: codex claude vscode lingma' \
  'PASS exact rendered argv launched without explicit session override' \
  'PASS derived isolated sessions: 4 unique hashes' \
  'PASS tool outcomes: 1 cancelled, at least 3 effective, 1 disconnected' \
  'PASS metadata privacy and private modes' \
  'PASS signal and EOF clean exact owned process groups without cross-session kill' \
  'PASS four-client report split' \
  'PASS child processes exited within 10s'; do
  grep -Fqx "$assertion" <<<"$output"
done

printf '%s\n' "$output"
