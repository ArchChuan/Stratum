#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
plan=${TEST_VERIFY_PLAN_PATH:-$root/tmp/test-verification/plan.json}
output=${TEST_VERIFY_REPORT_PATH:-$root/tmp/test-verification/completion-report.json}
attestation_dir=${E2E_ATTESTATION_DIR:-$root/test/e2e/attestations}
review_dir=${TEST_VERIFY_REVIEW_DIR:-$root/tmp/test-verification/reviews}
signature_receipt=${TEST_VERIFY_SIGNATURE_RECEIPT:-}
[[ -f "$plan" ]] || { printf 'verification plan is missing: %s\n' "$plan" >&2; exit 1; }

mode=$(jq -er .mode "$plan"); risk=$(jq -er .risk_level "$plan")
required_mode=$mode; required_profile=
[[ "$mode" == release-soak ]] && { required_mode=soak; required_profile=release; }
[[ "$mode" == soak ]] && required_profile=test
[[ "$mode" == none ]] && { printf 'verification mode none has no attestation report\n' >&2; exit 1; }
digest=$(go run "$root/cmd/e2e-attestation" digest --root "$root" --ref HEAD)
mapfile -t candidates < <(find "$attestation_dir" -type f -name "$digest.json" -print | sort)
selected=
for candidate in "${candidates[@]}"; do
  args=(verify --root "$root" --ref HEAD --required-mode "$required_mode" --attestation "$candidate")
  [[ -z "$required_profile" ]] || args+=(--required-profile "$required_profile")
  if go run "$root/cmd/e2e-attestation" "${args[@]}" >/dev/null 2>&1; then selected=$candidate; break; fi
done
[[ -n "$selected" ]] || { printf 'no verified attestation is available for completion reporting\n' >&2; exit 1; }

commit=$(jq -er .commit "$plan")
policy_digest=$(jq -er '.manifest_digest | sub("^sha256:"; "")' "$plan")
[[ $(jq -er .policy_manifest_digest "$selected") == "$policy_digest" ]] || {
  printf 'attestation verification policy digest does not match plan\n' >&2; exit 1
}

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

signed=false; bundle=unavailable; signature_ok=false
selected_subject="sha256:$(sha256sum "$selected" | cut -d' ' -f1)"
if [[ -n "$signature_receipt" && -f "$signature_receipt" ]] && jq -e --arg commit "$commit" '
  .verified == true and .issuer == "github-actions-sigstore" and .commit == $commit and
  (.bundle | length > 0) and (.subject_digest | test("^sha256:[0-9a-f]{64}$"))' \
  "$signature_receipt" >/dev/null && [[ $(jq -er .subject_digest "$signature_receipt") == "$selected_subject" ]]; then
  signed=true; signature_ok=true; bundle=$(jq -er .bundle "$signature_receipt")
fi

artifacts=${TEST_VERIFY_RELEASE_ARTIFACTS_JSON:-[]}
jq -e 'type == "array" and all(.[]; test("^sha256:[0-9a-f]{64}$"))' <<<"$artifacts" >/dev/null
[[ "$risk" != R4 || $(jq length <<<"$artifacts") -gt 0 ]] || reviews_ok=false
status=incomplete
[[ "${GITHUB_ACTIONS:-false}" == true && "$reviews_ok" == true && "$signature_ok" == true ]] && status=accepted

mkdir -p "$(dirname "$output")"
temporary=$output.tmp
jq -n --argjson plan "$(cat "$plan")" --argjson attestation "$(cat "$selected")" \
  --argjson reviews "$reviews" --argjson artifacts "$artifacts" --arg status "$status" \
  --arg path "$selected" --argjson signed "$signed" --arg bundle "$bundle" '
  def count_status($s): [$attestation.capabilities[] | select(.status == $s)] | length;
  {version:1,status:$status,commit:$plan.commit,manifest_digest:$plan.manifest_digest,
   risk_level:$plan.risk_level,mode:$plan.mode,reviews:$reviews,
   capabilities:{passed:count_status("passed"),failed:count_status("failed"),blocked:count_status("blocked"),skipped:count_status("skipped"),unreconciled:($attestation.unverified_capabilities|length)},
   attestation:{schema:$attestation.schema_version,verified:true,signed:$signed,issuer:"github-actions-sigstore",path:$path,bundle:$bundle},
   cleanup:{complete:($attestation.cleanup.passed and $attestation.owned_cleanup.database_dropped and $attestation.owned_cleanup.lease_removed),residual_entities:($attestation.cleanup.residual_entity_ids|length)},
   artifacts:$artifacts}' >"$temporary"
mv "$temporary" "$output"
go run "$root/cmd/verification-schema" --schema "$root/.test/schemas/completion-report.schema.json" --input "$output"
[[ "${TEST_VERIFY_REQUIRE_ACCEPTED:-false}" != true || "$status" == accepted ]] || {
  printf 'CI completion report is not accepted\n' >&2; exit 1
}
printf '%s\n' "$output"
