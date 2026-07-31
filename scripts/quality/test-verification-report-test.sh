#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
subject=$root/scripts/quality/test-verification-report.sh
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/stratum-verification-report.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT
mkdir -p "$test_dir/bin" "$test_dir/attestations"

commit=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
other_commit=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
digest=cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
policy=dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd

cat >"$test_dir/bin/go" <<EOF
#!/usr/bin/env bash
case "\$*" in
  *'e2e-attestation digest'*) printf '%s\n' '$digest' ;;
  *'e2e-attestation verify'*) exit 0 ;;
  *'verification-schema'*) exit 0 ;;
  *) exit 2 ;;
esac
EOF
chmod +x "$test_dir/bin/go"

write_plan() {
  local risk=$1 mode=$2 reviews=$3
  jq -n --arg commit "$commit" --arg policy "$policy" --arg risk "$risk" --arg mode "$mode" \
    --argjson reviews "$reviews" \
    '{version:1,commit:$commit,manifest_digest:("sha256:"+$policy),risk_level:$risk,mode:$mode,
      checks:["static"],reviews:$reviews}' >"$test_dir/plan.json"
}

run_report() {
  PATH="$test_dir/bin:$PATH" TEST_VERIFY_PLAN_PATH="$test_dir/plan.json" \
    TEST_VERIFY_REPORT_PATH="$test_dir/report.json" E2E_ATTESTATION_DIR="$test_dir/attestations" \
    TEST_VERIFY_REVIEW_DIR="$test_dir/reviews" bash "$subject" >/dev/null
}

write_plan R0 none '[]'
run_report
jq -e '.status == "incomplete" and .mode == "none" and .attestation == null and
  ([.capabilities[]] | all(. == 0)) and .cleanup.complete == true' "$test_dir/report.json" >/dev/null

mkdir -p "$test_dir/reviews"
jq -n --arg commit "$commit" \
  '{type:"code-quality",status:"passed",reviewer:"github-environment:code-quality-review",
    commit:$commit,policy_version:1,findings:[],evidence:"github-run:1"}' \
  >"$test_dir/reviews/code-quality.json"
write_plan R1 none '["code-quality"]'
run_report
jq -e '.risk_level == "R1" and .attestation == null and
  (.reviews | map(.type) == ["code-quality"])' "$test_dir/report.json" >/dev/null

write_attestation() {
  local directory=$1 parent=$2
  mkdir -p "$test_dir/attestations/$directory"
  jq -n --arg parent "$parent" --arg policy "$policy" \
    '{tested_git_parent:$parent,policy_manifest_digest:$policy,schema_version:2,
      capabilities:[{id:"dashboard",status:"passed"}],unverified_capabilities:[],
      cleanup:{passed:true,residual_entity_ids:[]},
      owned_cleanup:{database_dropped:true,lease_removed:true}}' \
    >"$test_dir/attestations/$directory/$digest.json"
}

write_plan R3 soak '["specification","code-quality"]'
write_attestation old "$other_commit"
if run_report 2>/dev/null; then
  printf 'completion report accepted an attestation for a different commit\n' >&2
  exit 1
fi
write_attestation current "$commit"
run_report
jq -e --arg commit "$commit" '(.commit == $commit) and (.attestation.path | contains("/current/"))' \
  "$test_dir/report.json" >/dev/null

mkdir -p "$test_dir/signatures"
: >"$test_dir/signatures/bundle.json"
jq -n --arg bundle "$test_dir/signatures/bundle.json" \
  '{bundle:$bundle,source_ref:"refs/heads/main"}' >"$test_dir/signature.json"
cat >"$test_dir/bin/gh" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$test_dir/bin/gh"
jq -n --arg commit "$commit" \
  '{type:"specification",status:"passed",reviewer:"github-environment:specification-review",
    commit:$commit,policy_version:1,findings:[],evidence:"github-run:1"}' \
  >"$test_dir/reviews/specification.json"
if PATH="$test_dir/bin:$PATH" TEST_VERIFY_PLAN_PATH="$test_dir/plan.json" \
  TEST_VERIFY_REPORT_PATH="$test_dir/report.json" E2E_ATTESTATION_DIR="$test_dir/attestations" \
  TEST_VERIFY_REVIEW_DIR="$test_dir/reviews" TEST_VERIFY_SIGNATURE_RECEIPT="$test_dir/signature.json" \
  TEST_VERIFY_REQUIRE_ACCEPTED=true bash "$subject" >/dev/null 2>&1; then
  printf 'completion report accepted without planned check evidence\n' >&2
  exit 1
fi
GITHUB_SHA="$commit" GITHUB_RUN_ID=1 GITHUB_REPOSITORY=byteBuilderX/stratum \
  bash "$root/scripts/quality/write-verification-check-receipt.sh" \
  "$test_dir/plan.json" "$test_dir/checks.json" static
PATH="$test_dir/bin:$PATH" TEST_VERIFY_PLAN_PATH="$test_dir/plan.json" \
  TEST_VERIFY_REPORT_PATH="$test_dir/report.json" E2E_ATTESTATION_DIR="$test_dir/attestations" \
  TEST_VERIFY_REVIEW_DIR="$test_dir/reviews" TEST_VERIFY_SIGNATURE_RECEIPT="$test_dir/signature.json" \
  TEST_VERIFY_CHECK_RECEIPT="$test_dir/checks.json" TEST_VERIFY_REQUIRE_ACCEPTED=true \
  bash "$subject" >/dev/null
jq -e '.status == "accepted" and (.checks | map(.id) == ["static"])' "$test_dir/report.json" >/dev/null

jq '.checks += ["unexecuted-check"]' "$test_dir/plan.json" >"$test_dir/plan-with-unexecuted-check.json"
if GITHUB_SHA="$commit" GITHUB_RUN_ID=1 GITHUB_REPOSITORY=byteBuilderX/stratum \
  bash "$root/scripts/quality/write-verification-check-receipt.sh" \
  "$test_dir/plan-with-unexecuted-check.json" "$test_dir/checks.json" static 2>/dev/null; then
  printf 'check receipt certified a planned check that was not executed\n' >&2
  exit 1
fi

printf 'verification completion report behavior passed\n'
