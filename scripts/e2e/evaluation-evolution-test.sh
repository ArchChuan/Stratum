#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-evolution-preflight.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT

mkdir -p "$test_dir/bin"
cat >"$test_dir/bin/npm" <<'SH'
#!/usr/bin/env bash
exit 1
SH
cat >"$test_dir/bin/docker" <<SH
#!/usr/bin/env bash
touch "$test_dir/docker-called"
exit 1
SH
cat >"$test_dir/bin/openssl" <<'SH'
#!/usr/bin/env bash
exit 0
SH
chmod +x "$test_dir/bin/npm" "$test_dir/bin/docker" "$test_dir/bin/openssl"
touch "$test_dir/opik-compose.yaml"

set +e
output=$(PATH="$test_dir/bin:$PATH" OPENAI_API_KEY=e2e-test-key \
  E2E_OPIK_COMPOSE_FILE="$test_dir/opik-compose.yaml" E2E_OPIK_VERSION=2.1.32 \
  bash "$repo_dir/scripts/e2e/evaluation-evolution.sh" 2>&1)
status=$?
set -e

if [[ $status -eq 0 ]]; then
  printf 'expected missing frontend dependency to fail the E2E preflight\n' >&2
  exit 1
fi
if [[ "$output" != *'frontend dependencies are incomplete; run npm ci --prefix web'* ]]; then
  printf 'missing dependency remediation was not reported\n' >&2
  exit 1
fi
if [[ -e "$test_dir/docker-called" ]]; then
  printf 'Docker was invoked before frontend dependency validation\n' >&2
  exit 1
fi

printf 'evaluation evolution dependency preflight test passed\n'
