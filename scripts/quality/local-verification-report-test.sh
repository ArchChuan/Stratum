#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-local-verification.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT
repo=$test_dir/repo
mkdir -p "$repo" "$test_dir/bin" "$test_dir/attestations"
git -C "$repo" init -q
git -C "$repo" config user.email test@example.com
git -C "$repo" config user.name Test
printf 'version: 1\n' >"$repo/manifest.yaml"
printf 'package fixture\n' >"$repo/source.go"
git -C "$repo" add .
git -C "$repo" commit -qm initial
commit=$(git -C "$repo" rev-parse HEAD)
manifest_digest=sha256:$(sha256sum "$repo/manifest.yaml" | awk '{print $1}')

cat >"$test_dir/bin/go" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$test_dir/bin/go"

jq -n --arg commit "$commit" --arg digest "$manifest_digest" '
  {version:1,commit:$commit,manifest_digest:$digest,risk_level:"R3",mode:"soak",
   local_checks:["e2e-short","e2e-soak"],ci_checks:["static"]}' >"$test_dir/plan.json"
jq -n --arg commit "$commit" --arg digest "${manifest_digest#sha256:}" '
  {schema_version:2,tested_git_parent:$commit,policy_manifest_digest:$digest,
   capabilities:[{id:"platform-assistant",status:"passed"}],unverified_capabilities:[],
   cleanup:{passed:true,residual_entity_ids:[]},owned_cleanup:{database_dropped:true,lease_removed:true}}' \
  >"$test_dir/attestation.json"

run_writer() {
  PATH="$test_dir/bin:$PATH" TEST_VERIFY_PLAN_PATH="$test_dir/plan.json" \
    LOCAL_VERIFY_REPORT_PATH="$test_dir/report.json" LOCAL_VERIFY_HISTORY_DIR="$test_dir/history" \
    LOCAL_VERIFY_ATTESTATION_PATH="$test_dir/attestation.json" LOCAL_VERIFY_SOURCE_ROOT="$repo" \
    LOCAL_VERIFY_MANIFEST_PATH="$repo/manifest.yaml" LOCAL_VERIFY_SCHEMA_PATH=/dev/null \
    LOCAL_VERIFY_OUTCOME="${1:-passed}" bash "$root/scripts/quality/write-local-verification-report.sh" >/dev/null
}

run_checker() {
  PATH="$test_dir/bin:$PATH" LOCAL_VERIFY_REPORT_PATH="$test_dir/report.json" \
    LOCAL_VERIFY_SOURCE_ROOT="$repo" LOCAL_VERIFY_MANIFEST_PATH="$repo/manifest.yaml" \
    LOCAL_VERIFY_SCHEMA_PATH=/dev/null bash "$root/scripts/quality/check-local-verification-report.sh" >/dev/null
}

run_writer passed
jq -e '.status == "passed" and .source_clean and .capabilities.passed == 1 and
  .cleanup.complete and .cleanup.residual_entities == 0' "$test_dir/report.json" >/dev/null
run_checker

printf 'dirty\n' >>"$repo/source.go"
if run_writer passed 2>/dev/null; then
  printf 'writer accepted dirty source as passed\n' >&2
  exit 1
fi
jq -e '.status == "failed" and (.source_clean | not)' "$test_dir/report.json" >/dev/null

git -C "$repo" restore source.go
run_writer infra_failed
run_writer failed
find "$test_dir/history" -type f -name '*.json' | grep -q .

run_writer passed
printf 'package fixture\n\n' >"$repo/source.go"
git -C "$repo" add source.go
git -C "$repo" commit -qm changed
if run_checker 2>/dev/null; then
  printf 'checker accepted report for an older HEAD\n' >&2
  exit 1
fi
jq -e '.status == "stale"' "$test_dir/report.json" >/dev/null

printf 'local verification report behavior passed\n'
