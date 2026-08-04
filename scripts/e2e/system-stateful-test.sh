#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
runner=$repo_dir/scripts/e2e/system-stateful.sh

bash -n "$runner"
for required in \
  'e2e-run-scope' '--strictPort' 'E2E_FIXTURE_URL' 'cleanup_owned' 'database_dropped' 'lease_removed' \
  'run_topology' 'owned_cleanup' 'mark-infrastructure-owned' \
  'STATEFUL_E2E_INFRA_UP_COMMAND' 'STATEFUL_E2E_INFRA_DOWN_COMMAND' \
  '--owner-pid'; do
  grep -Fq -- "$required" "$runner" || { printf 'runner missing dynamic lifecycle contract: %s\n' "$required" >&2; exit 1; }
done
grep -Fq 'prepare-registry' "$runner" || { printf 'runner must safely prepare the registry\n' >&2; exit 1; }
grep -Fq 'database-url' "$runner" || { printf 'runner must derive the database URL through run-scope\n' >&2; exit 1; }
if grep -Eq 'mkdir -p "?\$registry_root|chmod 700 "?\$registry_root' "$runner"; then
  printf 'runner must not create or chmod the registry root in shell\n' >&2
  exit 1
fi
if grep -Fq 'registry.lock' "$runner"; then
  printf 'runner must lock the validated registry directory without following a lock-file symlink\n' >&2
  exit 1
fi
grep -Fq 'stateful E2E failed during' "$runner" || { printf 'runner must expose the failing lifecycle phase\n' >&2; exit 1; }
grep -Eq -- '--owner-pid.*\$\$' "$runner" || { printf 'runner lease must record the runner PID\n' >&2; exit 1; }
grep -Fq '.schema_version == 2' "$runner" || { printf 'runner must require scope schema v2\n' >&2; exit 1; }
grep -Fq -- "--profile '\$profile'" "$runner" || {
  printf 'soak attestation must bind the acceptance profile\n' >&2
  exit 1
}
promotion_pack=$repo_dir/web/e2e/stateful/packs/evaluation-promotion.ts
system_spec=$repo_dir/web/e2e/system-stateful.spec.ts
grep -Eq 'webURL: string; fixtureURL: string' "$promotion_pack" || {
  printf 'evaluation promotion must require the dynamic fixture URL\n' >&2
  exit 1
}
grep -Eq 'actor: actors\.tenantAdmin, pool, evidence, webURL, fixtureURL' "$system_spec" || {
  printf 'stateful scheduler must pass the fixture URL to evaluation promotion\n' >&2
  exit 1
}
if grep -R -q 'E2E_MCP_BASE_URL' "$repo_dir/web/e2e/stateful/packs"; then
  printf 'stateful packs retain the fixed MCP endpoint\n' >&2
  exit 1
fi
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

test_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-stateful-concurrency.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT
registry=$test_dir/registry
(cd "$repo_dir" && go run ./cmd/e2e-run-scope allocate --repository "$repo_dir" --registry "$registry" --owner-pid "$BASHPID") >"$test_dir/a.json" & a_pid=$!
(cd "$repo_dir" && go run ./cmd/e2e-run-scope allocate --repository "$repo_dir" --registry "$registry" --owner-pid "$BASHPID") >"$test_dir/b.json" & b_pid=$!
wait "$a_pid"; wait "$b_pid"
jq -e --slurp '
  .[0].run_id != .[1].run_id and .[0].database_name != .[1].database_name and
  (([.[0].ports[], .[1].ports[]] | unique | length) == 12)
' "$test_dir/a.json" "$test_dir/b.json" >/dev/null
(cd "$repo_dir" && go run ./cmd/e2e-run-scope release --scope "$test_dir/a.json" --registry "$registry") >/dev/null
[[ -e "$registry/runs/$(jq -r .run_id "$test_dir/b.json").json" ]] || { printf 'run A release removed run B lease\n' >&2; exit 1; }
(cd "$repo_dir" && go run ./cmd/e2e-run-scope release --scope "$test_dir/b.json" --registry "$registry") >/dev/null
[[ -z "$(find "$registry/runs" -type f -name '*.json' -print -quit)" ]] || { printf 'leases remained after both releases\n' >&2; exit 1; }

printf 'stateful runner dynamic lifecycle contract passed\n'
bash "$repo_dir/scripts/e2e/system-stateful-behavior-test.sh"
