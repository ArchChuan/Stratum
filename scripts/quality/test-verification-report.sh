#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
plan=${TEST_VERIFY_PLAN_PATH:-$root/tmp/test-verification/plan.json}
output=${TEST_VERIFY_REPORT_PATH:-$root/tmp/test-verification/completion-report.json}
attestation_dir=${E2E_ATTESTATION_DIR:-$root/test/e2e/attestations}
review_dir=${TEST_VERIFY_REVIEW_DIR:-$root/tmp/test-verification/reviews}
signature_receipt=${TEST_VERIFY_SIGNATURE_RECEIPT:-}
check_receipt=${TEST_VERIFY_CHECK_RECEIPT:-$root/tmp/test-verification/checks.json}
trusted_repository=byteBuilderX/stratum
oidc_issuer=https://token.actions.githubusercontent.com
[[ -f "$plan" ]] || { printf 'verification plan is missing: %s\n' "$plan" >&2; exit 1; }

mode=$(jq -er .mode "$plan"); risk=$(jq -er .risk_level "$plan")
commit=$(jq -er .commit "$plan")
policy_digest=$(jq -er '.manifest_digest | sub("^sha256:"; "")' "$plan")
required_mode=$mode; required_profile=
[[ "$mode" == release-soak ]] && { required_mode=soak; required_profile=release; }
[[ "$mode" == soak ]] && required_profile=test
selected=; evidence=null
capabilities='{"passed":0,"failed":0,"blocked":0,"skipped":0,"unreconciled":0}'
cleanup='{"complete":true,"residual_entities":0}'
if [[ "$mode" != none ]]; then
  digest=$(go run "$root/cmd/e2e-attestation" digest --root "$root" --ref HEAD)
  mapfile -t candidates < <(find "$attestation_dir" -type f -name "$digest.json" -print | sort)
  for candidate in "${candidates[@]}"; do
    if ! jq -e --arg commit "$commit" --arg policy "$policy_digest" \
      '.tested_git_parent == $commit and .policy_manifest_digest == $policy' "$candidate" >/dev/null; then
      continue
    fi
    args=(verify --root "$root" --ref HEAD --required-mode "$required_mode" --attestation "$candidate")
    [[ -z "$required_profile" ]] || args+=(--required-profile "$required_profile")
    if go run "$root/cmd/e2e-attestation" "${args[@]}" >/dev/null 2>&1; then selected=$candidate; break; fi
  done
  [[ -n "$selected" ]] || { printf 'no verified attestation is available for completion reporting\n' >&2; exit 1; }
  evidence=$(cat "$selected")
  capabilities=$(jq '
    def count_status($s): [.capabilities[] | select(.status == $s)] | length;
    {passed:count_status("passed"),failed:count_status("failed"),blocked:count_status("blocked"),
     skipped:count_status("skipped"),unreconciled:(.unverified_capabilities|length)}' "$selected")
  cleanup=$(jq '{complete:(.cleanup.passed and .owned_cleanup.database_dropped and .owned_cleanup.lease_removed),
    residual_entities:(.cleanup.residual_entity_ids|length)}' "$selected")
fi

reviews='[]'; reviews_ok=true
mapfile -t required_reviews < <(jq -r '.reviews[]' "$plan")
for review_type in "${required_reviews[@]}"; do
  receipt=$review_dir/$review_type.json
  if [[ ! -f "$receipt" ]] || ! jq -e --arg type "$review_type" --arg commit "$commit" '
    .type == $type and .commit == $commit and .status == "passed" and
    (.reviewer | length > 0) and (.evidence | length > 0) and .policy_version >= 1 and
    (.findings | type == "array")' "$receipt" >/dev/null; then
    reviews_ok=false
    continue
  fi
  reviews=$(jq --argjson receipt "$(cat "$receipt")" '. + [$receipt]' <<<"$reviews")
done

checks='[]'; checks_ok=false
if [[ -f "$check_receipt" ]] && jq -e --arg commit "$commit" --arg manifest "$(jq -er .manifest_digest "$plan")" '
  .version == 1 and .commit == $commit and .manifest_digest == $manifest and .issuer == "github-actions" and
  (.results | type == "array" and length > 0 and all(.[];
    .status == "passed" and (.id | length > 0) and (.evidence | length > 0)))' "$check_receipt" >/dev/null; then
  planned=$(jq -cS '.checks | sort' "$plan")
  reported=$(jq -cS '[.results[].id] | sort | unique' "$check_receipt")
  if [[ "$planned" == "$reported" ]]; then
    checks=$(jq -c '.results' "$check_receipt")
    checks_ok=true
  fi
fi

signed=false; bundle=unavailable; signature_ok=false
signer_workflow=github.com/$trusted_repository/.github/workflows/deploy.yml
if [[ -n "$selected" && -n "$signature_receipt" && -f "$signature_receipt" ]] && jq -e '
  (.bundle | length > 0) and (.source_ref | test("^refs/(heads/main|pull/[0-9]+/merge)$"))' \
  "$signature_receipt" >/dev/null; then
  bundle=$(jq -er .bundle "$signature_receipt")
  source_ref=$(jq -er .source_ref "$signature_receipt")
  if [[ -f "$bundle" ]] && gh attestation verify "$selected" --bundle "$bundle" \
    --repo "$trusted_repository" --signer-workflow "$signer_workflow" \
    --source-digest "$commit" --source-ref "$source_ref" --cert-oidc-issuer "$oidc_issuer" \
    --format json >/dev/null; then
    signed=true; signature_ok=true
  fi
fi

artifacts=${TEST_VERIFY_RELEASE_ARTIFACTS_JSON:-[]}
jq -e 'type == "array" and all(.[]; test("^sha256:[0-9a-f]{64}$"))' <<<"$artifacts" >/dev/null
[[ "$risk" != R4 || $(jq length <<<"$artifacts") -gt 0 ]] || reviews_ok=false
status=incomplete
[[ "$reviews_ok" == true && "$checks_ok" == true && "$signature_ok" == true ]] && status=accepted

mkdir -p "$(dirname "$output")"
temporary=$output.tmp
jq -n --argjson plan "$(cat "$plan")" --argjson evidence "$evidence" \
  --argjson capabilities "$capabilities" --argjson cleanup "$cleanup" \
  --argjson reviews "$reviews" --argjson checks "$checks" --argjson artifacts "$artifacts" --arg status "$status" \
  --arg path "$selected" --argjson signed "$signed" --arg bundle "$bundle" '
  def attestation_summary:
    if $evidence == null then null else
      {schema:$evidence.schema_version,verified:true,signed:$signed,issuer:"github-actions-sigstore",path:$path,bundle:$bundle}
    end;
  {version:1,status:$status,commit:$plan.commit,manifest_digest:$plan.manifest_digest,
   risk_level:$plan.risk_level,mode:$plan.mode,reviews:$reviews,checks:$checks,
   capabilities:$capabilities,attestation:attestation_summary,cleanup:$cleanup,
   artifacts:$artifacts}' >"$temporary"
mv "$temporary" "$output"
go run "$root/cmd/verification-schema" --schema "$root/.test/schemas/completion-report.schema.json" --input "$output"
[[ "${TEST_VERIFY_REQUIRE_ACCEPTED:-false}" != true || "$status" == accepted ]] || {
  printf 'CI completion report is not accepted\n' >&2; exit 1
}
printf '%s\n' "$output"
