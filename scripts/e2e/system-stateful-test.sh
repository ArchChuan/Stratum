#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
runner=$repo_dir/scripts/e2e/system-stateful.sh

bash -n "$runner"
for required in 'e2e-run-scope' '--strictPort' 'E2E_FIXTURE_URL' 'cleanup_owned' 'database_dropped' 'lease_removed' 'run_topology' 'owned_cleanup' 'registry.lock'; do
  grep -Fq -- "$required" "$runner" || { printf 'runner missing dynamic lifecycle contract: %s\n' "$required" >&2; exit 1; }
done
if grep -Eq '15173|18080|19090|19091|STATEFUL_E2E_LOCK_FILE|STATEFUL_E2E_PORT_PREFLIGHT' "$runner"; then
  printf 'runner retains fixed topology or whole-run lock behavior\n' >&2
  exit 1
fi

expect_failure() {
  local expected=$1; shift
  local output status
  set +e; output=$("$@" 2>&1); status=$?; set -e
  ((status != 0)) || { printf 'expected failure containing %s\n' "$expected" >&2; exit 1; }
  [[ "$output" == *"$expected"* ]] || { printf 'missing failure %s in %s\n' "$expected" "$output" >&2; exit 1; }
}

expect_failure 'unsupported stateful E2E mode' bash "$runner" invalid
expect_failure 'between 600 and 14400' env TEST_DATABASE_URL=postgres://u:p@127.0.0.1/e2e STATEFUL_E2E_DURATION_SEC=599 bash "$runner" short
expect_failure 'short mode cannot declare' env TEST_DATABASE_URL=postgres://u:p@127.0.0.1/e2e STATEFUL_E2E_PROFILE=test bash "$runner" short
expect_failure 'unknown stateful E2E pack' env TEST_DATABASE_URL=postgres://u:p@127.0.0.1/e2e STATEFUL_E2E_PACKS=unknown bash "$runner" short

printf 'stateful runner dynamic lifecycle contract passed\n'
